package fsrouter

import (
	"fmt"
	"math"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	billy "github.com/go-git/go-billy/v5"
)

// VFS implements billy.Filesystem by dispatching operations to the
// Router's registered handlers.
type VFS struct {
	router *Router
}

var _ billy.Filesystem = (*VFS)(nil)
var _ billy.Change = (*VFS)(nil)

// --------------------------------------------------------------------------
// billy.Basic
// --------------------------------------------------------------------------

// Create is called by the NFS CREATE RPC. go-nfs immediately Close()s the
// returned file — actual data arrives later in WRITE RPCs via OpenFile.
func (v *VFS) Create(filename string) (billy.File, error) {
	filename = v.clean(filename)

	// Must have a Create or Write handler to accept data.
	hasCreate, _ := v.router.resolve(VerbCreate, filename)
	hasWrite, _ := v.router.resolve(VerbWrite, filename)
	if hasCreate == nil && hasWrite == nil {
		return nil, os.ErrPermission
	}

	v.router.addPending(filename)
	return newNoOpFile(filename), nil
}

// Open opens an existing file for reading.
func (v *VFS) Open(filename string) (billy.File, error) {
	filename = v.clean(filename)

	if handler, ctx := v.router.resolve(VerbRead, filename); handler != nil {
		return newReadFile(filename, handler.(ReadHandler), ctx), nil
	}
	if v.router.isImplicitDir(filename) || v.router.isPending(filename) {
		return newNoOpFile(filename), nil
	}
	return nil, os.ErrNotExist
}

// OpenFile opens a file with the given flags and permissions.
func (v *VFS) OpenFile(filename string, flag int, perm os.FileMode) (billy.File, error) {
	filename = v.clean(filename)

	isWrite := flag&(os.O_WRONLY|os.O_RDWR|os.O_APPEND|os.O_CREATE|os.O_TRUNC) != 0

	if isWrite {
		pending := v.router.isPending(filename)
		router := v.router
		cleanup := func() { router.removePending(filename) }

		if pending {
			// New file: buffer writes, deliver to Create handler on close.
			if handler, ctx := v.router.resolve(VerbCreate, filename); handler != nil {
				f := newBufferedFile(filename, handler.(CreateHandler), ctx)
				f.onClose = cleanup
				return f, nil
			}
			// No Create handler but Write handler exists: use ranged write.
			if handler, ctx := v.router.resolve(VerbWrite, filename); handler != nil {
				f := newWriteFile(filename, handler.(WriteHandler), ctx)
				f.onClose = cleanup
				return f, nil
			}
			return newNoOpFile(filename), nil
		}

		// Existing file: pwrite per chunk.
		if handler, ctx := v.router.resolve(VerbWrite, filename); handler != nil {
			return newWriteFile(filename, handler.(WriteHandler), ctx), nil
		}
	}

	// Read path.
	if handler, ctx := v.router.resolve(VerbRead, filename); handler != nil {
		return newReadFile(filename, handler.(ReadHandler), ctx), nil
	}
	if v.router.isImplicitDir(filename) || v.router.isPending(filename) {
		return newNoOpFile(filename), nil
	}
	return nil, os.ErrNotExist
}

// Stat returns file information.
func (v *VFS) Stat(filename string) (os.FileInfo, error) {
	return v.stat(filename)
}

func (v *VFS) stat(filename string) (os.FileInfo, error) {
	filename = v.clean(filename)

	// 1. Pending file — just created, not yet written. Must be visible
	//    before any handler checks so that NFS SETATTR after CREATE succeeds.
	if v.router.isPending(filename) {
		return v.statToInfo(filename, &FileStat{
			Size:    0,
			Mode:    0644,
			ModTime: v.router.bootTime,
		}), nil
	}

	// 2. Explicit stat handler.
	if handler, ctx := v.router.resolve(VerbStat, filename); handler != nil {
		stat, err := handler.(StatHandler)(ctx)
		if err != nil {
			return nil, os.ErrNotExist
		}
		return v.statToInfo(filename, stat), nil
	}

	// 3. Implicit directory.
	if v.router.isImplicitDir(filename) {
		return v.dirInfo(filename), nil
	}

	// 4. List handler → directory.
	if handler, _ := v.router.resolve(VerbList, filename+"/"); handler != nil {
		return v.dirInfo(filename), nil
	}

	// 5. Read handler → file exists; call the handler to determine size.
	if handler, ctx := v.router.resolve(VerbRead, filename); handler != nil {
		size := int64(0)
		if data, err := handler.(ReadHandler)(ctx, 0, math.MaxInt); err == nil {
			size = int64(len(data))
		}
		return v.statToInfo(filename, &FileStat{
			Size:    size,
			Mode:    0644,
			ModTime: v.router.bootTime,
		}), nil
	}

	return nil, os.ErrNotExist
}

// Rename renames a file.
func (v *VFS) Rename(oldpath, newpath string) error {
	oldpath = v.clean(oldpath)
	newpath = v.clean(newpath)

	handler, ctx := v.router.resolve(VerbRename, oldpath)
	if handler == nil {
		return os.ErrPermission
	}
	return handler.(RenameHandler)(ctx, newpath)
}

// Remove removes a file.
func (v *VFS) Remove(filename string) error {
	filename = v.clean(filename)

	handler, ctx := v.router.resolve(VerbRemove, filename)
	if handler == nil {
		return os.ErrPermission
	}
	return handler.(RemoveHandler)(ctx)
}

// Join joins path elements.
func (v *VFS) Join(elem ...string) string {
	return path.Join(elem...)
}

// TempFile is not supported.
func (v *VFS) TempFile(dir, prefix string) (billy.File, error) {
	return nil, os.ErrPermission
}

// --------------------------------------------------------------------------
// billy.Dir
// --------------------------------------------------------------------------

// ReadDir returns the contents of a directory.
func (v *VFS) ReadDir(dirPath string) ([]os.FileInfo, error) {
	dirPath = v.clean(dirPath)

	listPath := dirPath
	if !strings.HasSuffix(listPath, "/") {
		listPath += "/"
	}

	if handler, ctx := v.router.resolve(VerbList, listPath); handler != nil {
		entries, err := handler.(ListHandler)(ctx)
		if err != nil {
			return nil, err
		}
		infos := make([]os.FileInfo, len(entries))
		for i, e := range entries {
			infos[i] = &dirEntryInfo{entry: e}
		}
		return infos, nil
	}

	children := v.router.implicitChildren(dirPath)
	if len(children) == 0 && !v.router.isImplicitDir(dirPath) {
		return nil, os.ErrNotExist
	}

	sort.Strings(children)
	infos := make([]os.FileInfo, 0, len(children))
	for _, name := range children {
		childPath := path.Join(dirPath, name)
		info, err := v.stat(childPath)
		if err != nil {
			info = v.dirInfo(childPath)
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// MkdirAll creates a directory.
func (v *VFS) MkdirAll(filename string, perm os.FileMode) error {
	filename = v.clean(filename)

	handler, ctx := v.router.resolve(VerbMkdir, filename)
	if handler != nil {
		return handler.(MkdirHandler)(ctx)
	}
	if v.router.isImplicitDir(filename) {
		return nil
	}
	return os.ErrPermission
}

// --------------------------------------------------------------------------
// billy.Symlink (not supported)
// --------------------------------------------------------------------------

func (v *VFS) Lstat(filename string) (os.FileInfo, error) { return v.Stat(filename) }
func (v *VFS) Symlink(target, link string) error          { return os.ErrPermission }
func (v *VFS) Readlink(link string) (string, error)       { return "", os.ErrInvalid }

// --------------------------------------------------------------------------
// billy.Chroot
// --------------------------------------------------------------------------

func (v *VFS) Chroot(p string) (billy.Filesystem, error) {
	return nil, fmt.Errorf("fsrouter: chroot not supported")
}
func (v *VFS) Root() string { return "/" }

// --------------------------------------------------------------------------
// billy.Change — no-ops so NFS SETATTR succeeds
// --------------------------------------------------------------------------

func (v *VFS) Chmod(name string, mode os.FileMode) error         { return nil }
func (v *VFS) Lchown(name string, uid, gid int) error            { return nil }
func (v *VFS) Chown(name string, uid, gid int) error             { return nil }
func (v *VFS) Chtimes(name string, atime, mtime time.Time) error { return nil }

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

func (v *VFS) clean(p string) string {
	return path.Clean("/" + p)
}

func (v *VFS) statToInfo(name string, stat *FileStat) os.FileInfo {
	mode := stat.Mode
	if mode == 0 {
		if stat.IsDir {
			mode = os.ModeDir | 0755
		} else {
			mode = 0644
		}
	}
	if stat.IsDir {
		mode |= os.ModeDir
	}
	modTime := stat.ModTime
	if modTime.IsZero() {
		modTime = v.router.bootTime
	}
	return &fileInfo{
		name: path.Base(name),
		stat: FileStat{
			Size:    stat.Size,
			Mode:    mode,
			ModTime: modTime,
			IsDir:   stat.IsDir,
		},
	}
}

func (v *VFS) dirInfo(dirPath string) os.FileInfo {
	name := path.Base(dirPath)
	if dirPath == "/" {
		name = "/"
	}
	return &fileInfo{
		name: name,
		stat: FileStat{
			Size:    4096,
			Mode:    os.ModeDir | 0755,
			ModTime: v.router.bootTime,
			IsDir:   true,
		},
	}
}

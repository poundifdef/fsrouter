package fsrouter

import (
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	billy "github.com/go-git/go-billy/v5"
)

// VFS implements billy.Filesystem by dispatching operations to the
// Router's registered handlers. This is the bridge between NFS protocol
// operations and your handler functions.
type VFS struct {
	router *Router
}

var _ billy.Filesystem = (*VFS)(nil)

// --------------------------------------------------------------------------
// billy.Basic
// --------------------------------------------------------------------------

// Create creates a new file. If a CreateHandler is registered, it is called first.
// The returned File buffers writes and delivers them to the WriteHandler on Close().
func (v *VFS) Create(filename string) (billy.File, error) {
	filename = v.clean(filename)

	// Check for create handler.
	if handler, ctx := v.router.resolve(VerbCreate, filename); handler != nil {
		if err := handler.(CreateHandler)(ctx); err != nil {
			return nil, err
		}
	}

	// Find a write handler.
	handler, ctx := v.router.resolve(VerbWrite, filename)
	if handler == nil {
		return nil, os.ErrPermission
	}

	return newWriteFile(filename, handler.(WriteHandler), ctx), nil
}

// Open opens an existing file for reading.
func (v *VFS) Open(filename string) (billy.File, error) {
	filename = v.clean(filename)

	handler, ctx := v.router.resolve(VerbRead, filename)
	if handler == nil {
		// Check if it's a directory — NFS sometimes opens directories.
		if v.router.isImplicitDir(filename) {
			return newReadFile(filename, func(c *Context) ([]byte, error) {
				return nil, nil
			}, newContext(filename, nil)), nil
		}
		return nil, os.ErrNotExist
	}

	return newReadFile(filename, handler.(ReadHandler), ctx), nil
}

// OpenFile opens a file with the given flags and permissions.
func (v *VFS) OpenFile(filename string, flag int, perm os.FileMode) (billy.File, error) {
	filename = v.clean(filename)

	isWrite := flag&(os.O_WRONLY|os.O_RDWR|os.O_APPEND|os.O_CREATE|os.O_TRUNC) != 0
	isRead := flag&os.O_WRONLY == 0 // O_RDONLY is 0, so anything without O_WRONLY can read.

	var rh ReadHandler
	var rctx *Context
	var wh WriteHandler
	var wctx *Context

	if isRead {
		if h, c := v.router.resolve(VerbRead, filename); h != nil {
			rh = h.(ReadHandler)
			rctx = c
		}
	}

	if isWrite {
		// Check create handler for new files.
		if flag&os.O_CREATE != 0 {
			if h, c := v.router.resolve(VerbCreate, filename); h != nil {
				if err := h.(CreateHandler)(c); err != nil {
					return nil, err
				}
			}
		}

		if h, c := v.router.resolve(VerbWrite, filename); h != nil {
			wh = h.(WriteHandler)
			wctx = c
		}
	}

	if rh == nil && wh == nil {
		// Check if it's a directory.
		if v.router.isImplicitDir(filename) {
			return newReadFile(filename, func(c *Context) ([]byte, error) {
				return nil, nil
			}, newContext(filename, nil)), nil
		}
		return nil, os.ErrNotExist
	}

	if rh != nil && wh != nil {
		return newReadWriteFile(filename, rh, rctx, wh, wctx), nil
	}
	if wh != nil {
		return newWriteFile(filename, wh, wctx), nil
	}
	return newReadFile(filename, rh, rctx), nil
}

// Stat returns file information. It checks handlers in order:
// 1. Explicit StatHandler
// 2. If it's an implicit directory, return directory info
// 3. Fall back to calling the ReadHandler to determine size
func (v *VFS) Stat(filename string) (os.FileInfo, error) {
	filename = v.clean(filename)
	return v.stat(filename)
}

func (v *VFS) stat(filename string) (os.FileInfo, error) {
	// 1. Check explicit stat handler.
	if handler, ctx := v.router.resolve(VerbStat, filename); handler != nil {
		stat, err := handler.(StatHandler)(ctx)
		if err != nil {
			return nil, err
		}
		return v.statToInfo(filename, stat), nil
	}

	// 2. Check if it's an implicit directory.
	if v.router.isImplicitDir(filename) {
		return v.dirInfo(filename), nil
	}

	// 3. Check if there's a List handler that matches (for directory-like paths).
	if handler, _ := v.router.resolve(VerbList, filename+"/"); handler != nil {
		return v.dirInfo(filename), nil
	}

	// 4. Fall back to the read handler to infer file size.
	if handler, ctx := v.router.resolve(VerbRead, filename); handler != nil {
		data, err := handler.(ReadHandler)(ctx)
		if err != nil {
			return nil, err
		}
		return v.statToInfo(filename, &FileStat{
			Size:    int64(len(data)),
			Mode:    0644,
			ModTime: time.Now(),
		}), nil
	}

	// 5. Check if there's a write handler (file exists but is write-only).
	if handler, _ := v.router.resolve(VerbWrite, filename); handler != nil {
		return v.statToInfo(filename, &FileStat{
			Size:    0,
			Mode:    0644,
			ModTime: time.Now(),
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

// TempFile is not supported in virtual filesystems.
func (v *VFS) TempFile(dir, prefix string) (billy.File, error) {
	return nil, os.ErrPermission
}

// --------------------------------------------------------------------------
// billy.Dir
// --------------------------------------------------------------------------

// ReadDir returns the contents of a directory.
// It checks for an explicit ListHandler first, then falls back to the
// implicit directory tree (static children from registered routes).
func (v *VFS) ReadDir(dirPath string) ([]os.FileInfo, error) {
	dirPath = v.clean(dirPath)

	// Normalize: try both with and without trailing slash for List matching.
	listPath := dirPath
	if !strings.HasSuffix(listPath, "/") {
		listPath += "/"
	}

	// 1. Check for an explicit List handler.
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

	// 2. Fall back to implicit children from the route tree.
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
			// If stat fails, create a synthetic directory entry.
			info = v.dirInfo(childPath)
		}
		infos = append(infos, info)
	}

	return infos, nil
}

// MkdirAll creates a directory (and parents). Calls MkdirHandler if registered.
func (v *VFS) MkdirAll(filename string, perm os.FileMode) error {
	filename = v.clean(filename)

	handler, ctx := v.router.resolve(VerbMkdir, filename)
	if handler != nil {
		return handler.(MkdirHandler)(ctx)
	}

	// If the directory is already implicit, succeed silently.
	if v.router.isImplicitDir(filename) {
		return nil
	}

	return os.ErrPermission
}

// --------------------------------------------------------------------------
// billy.Symlink (not supported — return appropriate errors)
// --------------------------------------------------------------------------

func (v *VFS) Lstat(filename string) (os.FileInfo, error) {
	return v.Stat(filename)
}

func (v *VFS) Symlink(target, link string) error {
	return os.ErrPermission
}

func (v *VFS) Readlink(link string) (string, error) {
	return "", os.ErrInvalid
}

// --------------------------------------------------------------------------
// billy.Chroot
// --------------------------------------------------------------------------

func (v *VFS) Chroot(chrootPath string) (billy.Filesystem, error) {
	return nil, fmt.Errorf("fsrouter: chroot not supported")
}

func (v *VFS) Root() string {
	return "/"
}

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

func (v *VFS) clean(p string) string {
	return path.Clean("/" + p)
}

func (v *VFS) statToInfo(name string, stat *FileStat) os.FileInfo {
	base := path.Base(name)
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
		modTime = time.Now()
	}
	return &fileInfo{
		name: base,
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
			ModTime: time.Now(),
			IsDir:   true,
		},
	}
}

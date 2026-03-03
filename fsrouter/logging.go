package fsrouter

import (
	"os"
	"time"

	"github.com/rs/zerolog"
	billy "github.com/go-git/go-billy/v5"
)

// LoggingMiddleware returns a Middleware that logs every handler invocation
// using the provided zerolog.Logger. It records the verb, path, duration,
// and any error returned by the handler.
func LoggingMiddleware(logger zerolog.Logger) Middleware {
	return func(verb Verb, path string, next func() error) error {
		start := time.Now()
		err := next()
		ev := logger.Info()
		if err != nil {
			ev = logger.Warn().Err(err)
		}
		ev.Str("verb", verb.String()).
			Str("path", path).
			Dur("duration", time.Since(start)).
			Msg("handler")
		return err
	}
}

// LoggingVFS wraps a billy.Filesystem and logs every VFS operation.
type LoggingVFS struct {
	fs     billy.Filesystem
	logger zerolog.Logger
}

// NewLoggingVFS wraps fs with zerolog-based operation logging.
func NewLoggingVFS(fs billy.Filesystem, logger zerolog.Logger) *LoggingVFS {
	return &LoggingVFS{fs: fs, logger: logger}
}

// Compile-time interface checks.
var _ billy.Filesystem = (*LoggingVFS)(nil)
var _ billy.Change = (*LoggingVFS)(nil)

func (l *LoggingVFS) Create(filename string) (billy.File, error) {
	start := time.Now()
	f, err := l.fs.Create(filename)
	l.log("Create", filename, err, start)
	return f, err
}

func (l *LoggingVFS) Open(filename string) (billy.File, error) {
	start := time.Now()
	f, err := l.fs.Open(filename)
	l.log("Open", filename, err, start)
	return f, err
}

func (l *LoggingVFS) OpenFile(filename string, flag int, perm os.FileMode) (billy.File, error) {
	start := time.Now()
	f, err := l.fs.OpenFile(filename, flag, perm)
	l.logger.Debug().
		Str("op", "OpenFile").
		Str("path", filename).
		Int("flag", flag).
		Dur("duration", time.Since(start)).
		Err(err).
		Msg("vfs")
	return f, err
}

func (l *LoggingVFS) Stat(filename string) (os.FileInfo, error) {
	start := time.Now()
	info, err := l.fs.Stat(filename)
	l.log("Stat", filename, err, start)
	return info, err
}

func (l *LoggingVFS) Rename(oldpath, newpath string) error {
	start := time.Now()
	err := l.fs.Rename(oldpath, newpath)
	l.logger.Debug().
		Str("op", "Rename").
		Str("from", oldpath).
		Str("to", newpath).
		Dur("duration", time.Since(start)).
		Err(err).
		Msg("vfs")
	return err
}

func (l *LoggingVFS) Remove(filename string) error {
	start := time.Now()
	err := l.fs.Remove(filename)
	l.log("Remove", filename, err, start)
	return err
}

func (l *LoggingVFS) Join(elem ...string) string {
	return l.fs.Join(elem...)
}

func (l *LoggingVFS) TempFile(dir, prefix string) (billy.File, error) {
	return l.fs.TempFile(dir, prefix)
}

func (l *LoggingVFS) ReadDir(path string) ([]os.FileInfo, error) {
	start := time.Now()
	infos, err := l.fs.ReadDir(path)
	l.logger.Debug().
		Str("op", "ReadDir").
		Str("path", path).
		Int("entries", len(infos)).
		Dur("duration", time.Since(start)).
		Err(err).
		Msg("vfs")
	return infos, err
}

func (l *LoggingVFS) MkdirAll(filename string, perm os.FileMode) error {
	start := time.Now()
	err := l.fs.MkdirAll(filename, perm)
	l.log("MkdirAll", filename, err, start)
	return err
}

func (l *LoggingVFS) Lstat(filename string) (os.FileInfo, error) {
	start := time.Now()
	info, err := l.fs.Lstat(filename)
	l.log("Lstat", filename, err, start)
	return info, err
}

func (l *LoggingVFS) Symlink(target, link string) error    { return l.fs.Symlink(target, link) }
func (l *LoggingVFS) Readlink(link string) (string, error) { return l.fs.Readlink(link) }

func (l *LoggingVFS) Chroot(path string) (billy.Filesystem, error) {
	return l.fs.Chroot(path)
}

func (l *LoggingVFS) Root() string { return l.fs.Root() }

// billy.Change
func (l *LoggingVFS) Chmod(name string, mode os.FileMode) error {
	return l.fs.(billy.Change).Chmod(name, mode)
}
func (l *LoggingVFS) Lchown(name string, uid, gid int) error {
	return l.fs.(billy.Change).Lchown(name, uid, gid)
}
func (l *LoggingVFS) Chown(name string, uid, gid int) error {
	return l.fs.(billy.Change).Chown(name, uid, gid)
}
func (l *LoggingVFS) Chtimes(name string, atime, mtime time.Time) error {
	return l.fs.(billy.Change).Chtimes(name, atime, mtime)
}

func (l *LoggingVFS) log(op, path string, err error, start time.Time) {
	l.logger.Debug().
		Str("op", op).
		Str("path", path).
		Dur("duration", time.Since(start)).
		Err(err).
		Msg("vfs")
}

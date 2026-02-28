package fsrouter

import (
	"io"
	"os"

	billy "github.com/go-git/go-billy/v5"
)

// Compile-time checks.
var _ billy.File = (*noOpFile)(nil)
var _ billy.File = (*bufferedFile)(nil)
var _ billy.File = (*rangedFile)(nil)

// --------------------------------------------------------------------------
// noOpFile — returned by VFS.Create() for the NFS CREATE RPC.
// go-nfs immediately Close()s this file; actual data arrives via WRITE RPCs.
// --------------------------------------------------------------------------

type noOpFile struct{ name string }

func newNoOpFile(name string) *noOpFile { return &noOpFile{name: name} }

func (f *noOpFile) Name() string                              { return f.name }
func (f *noOpFile) Read(p []byte) (int, error)                { return 0, io.EOF }
func (f *noOpFile) ReadAt(p []byte, off int64) (int, error)   { return 0, io.EOF }
func (f *noOpFile) Write(p []byte) (int, error)               { return len(p), nil }
func (f *noOpFile) Seek(off int64, whence int) (int64, error) { return 0, nil }
func (f *noOpFile) Close() error                              { return nil }
func (f *noOpFile) Truncate(size int64) error                 { return nil }
func (f *noOpFile) Lock() error                               { return nil }
func (f *noOpFile) Unlock() error                             { return nil }

// --------------------------------------------------------------------------
// bufferedFile — buffers all writes, calls CreateHandler with the complete
// data on Close. Used for the CREATE→WRITE→CLOSE lifecycle.
// --------------------------------------------------------------------------

type bufferedFile struct {
	name    string
	buf     []byte
	handler CreateHandler
	ctx     *Context
	onClose func()
	closed  bool
}

func newBufferedFile(name string, handler CreateHandler, ctx *Context) *bufferedFile {
	return &bufferedFile{
		name:    name,
		handler: handler,
		ctx:     ctx,
	}
}

func (f *bufferedFile) Name() string { return f.name }

func (f *bufferedFile) Read(p []byte) (int, error)              { return 0, io.EOF }
func (f *bufferedFile) ReadAt(p []byte, off int64) (int, error) { return 0, io.EOF }

func (f *bufferedFile) Write(p []byte) (int, error) {
	f.buf = append(f.buf, p...)
	return len(p), nil
}

func (f *bufferedFile) Seek(off int64, whence int) (int64, error) { return 0, nil }

func (f *bufferedFile) Close() error {
	if f.closed {
		return nil
	}
	f.closed = true
	var err error
	if f.handler != nil {
		err = f.handler(f.ctx, f.buf)
	}
	if f.onClose != nil {
		f.onClose()
	}
	return err
}

func (f *bufferedFile) Truncate(size int64) error { return nil }
func (f *bufferedFile) Lock() error               { return nil }
func (f *bufferedFile) Unlock() error             { return nil }

// --------------------------------------------------------------------------
// rangedFile — dispatches each Read/Write call directly to the handler.
// No buffering. Each NFS RPC opens the file, does one operation, closes it.
// --------------------------------------------------------------------------

type rangedFile struct {
	name   string
	offset int64

	readHandler  ReadHandler
	readCtx      *Context
	writeHandler WriteHandler
	writeCtx     *Context

	onClose func()
	closed  bool
}

func newReadFile(name string, handler ReadHandler, ctx *Context) *rangedFile {
	return &rangedFile{
		name:        name,
		readHandler: handler,
		readCtx:     ctx,
	}
}

func newWriteFile(name string, handler WriteHandler, ctx *Context) *rangedFile {
	return &rangedFile{
		name:         name,
		writeHandler: handler,
		writeCtx:     ctx,
	}
}

func (f *rangedFile) Name() string { return f.name }

func (f *rangedFile) Read(p []byte) (int, error) {
	n, err := f.ReadAt(p, f.offset)
	f.offset += int64(n)
	return n, err
}

func (f *rangedFile) ReadAt(p []byte, off int64) (int, error) {
	if f.readHandler == nil {
		return 0, os.ErrPermission
	}
	data, err := f.readHandler(f.readCtx, off, len(p))
	if err != nil {
		return 0, err
	}
	n := copy(p, data)
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (f *rangedFile) Write(p []byte) (int, error) {
	if f.writeHandler == nil {
		return 0, os.ErrPermission
	}
	err := f.writeHandler(f.writeCtx, p, f.offset)
	if err != nil {
		return 0, err
	}
	f.offset += int64(len(p))
	return len(p), nil
}

func (f *rangedFile) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		f.offset = offset
	case io.SeekCurrent:
		f.offset += offset
	case io.SeekEnd:
		f.offset = offset
	}
	if f.offset < 0 {
		f.offset = 0
		return 0, os.ErrInvalid
	}
	return f.offset, nil
}

func (f *rangedFile) Close() error {
	if f.closed {
		return nil
	}
	f.closed = true
	if f.onClose != nil {
		f.onClose()
	}
	return nil
}

func (f *rangedFile) Truncate(size int64) error { return nil }
func (f *rangedFile) Lock() error               { return nil }
func (f *rangedFile) Unlock() error             { return nil }

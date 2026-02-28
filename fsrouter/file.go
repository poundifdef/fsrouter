package fsrouter

import (
	"bytes"
	"io"
	"os"
	"sync"
)

// virtualFile implements billy.File, bridging NFS file operations to
// fsrouter handlers. For reads, it lazily invokes the ReadHandler and
// serves data from a buffer. For writes, it buffers all data and invokes
// the WriteHandler on Close().
type virtualFile struct {
	name string
	flag int

	mu sync.Mutex

	// Read side: populated lazily on first Read/ReadAt/Seek.
	readHandler ReadHandler
	readCtx     *Context
	readBuf     *bytes.Reader
	readLoaded  bool

	// Write side: buffered until Close().
	writeHandler WriteHandler
	writeCtx     *Context
	writeBuf     bytes.Buffer

	// Current offset for Read/Write interleaving.
	offset int64
	closed bool
}

func newReadFile(name string, handler ReadHandler, ctx *Context) *virtualFile {
	return &virtualFile{
		name:        name,
		flag:        os.O_RDONLY,
		readHandler: handler,
		readCtx:     ctx,
	}
}

func newWriteFile(name string, handler WriteHandler, ctx *Context) *virtualFile {
	return &virtualFile{
		name:         name,
		flag:         os.O_WRONLY | os.O_CREATE,
		writeHandler: handler,
		writeCtx:     ctx,
	}
}

func newReadWriteFile(name string, rh ReadHandler, rctx *Context, wh WriteHandler, wctx *Context) *virtualFile {
	return &virtualFile{
		name:         name,
		flag:         os.O_RDWR,
		readHandler:  rh,
		readCtx:      rctx,
		writeHandler: wh,
		writeCtx:     wctx,
	}
}

// loadReadBuf lazily invokes the ReadHandler to populate the read buffer.
func (f *virtualFile) loadReadBuf() error {
	if f.readLoaded {
		return nil
	}
	if f.readHandler == nil {
		return os.ErrPermission
	}

	data, err := f.readHandler(f.readCtx)
	if err != nil {
		return err
	}
	f.readBuf = bytes.NewReader(data)
	f.readLoaded = true
	return nil
}

// --------------------------------------------------------------------------
// billy.File interface
// --------------------------------------------------------------------------

func (f *virtualFile) Name() string {
	return f.name
}

func (f *virtualFile) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := f.loadReadBuf(); err != nil {
		return 0, err
	}
	n, err := f.readBuf.Read(p)
	f.offset += int64(n)
	return n, err
}

func (f *virtualFile) ReadAt(p []byte, off int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := f.loadReadBuf(); err != nil {
		return 0, err
	}
	return f.readBuf.ReadAt(p, off)
}

func (f *virtualFile) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.writeHandler == nil {
		return 0, os.ErrPermission
	}
	n, err := f.writeBuf.Write(p)
	f.offset += int64(n)
	return n, err
}

func (f *virtualFile) Seek(offset int64, whence int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.readHandler != nil {
		if err := f.loadReadBuf(); err != nil {
			return 0, err
		}
		pos, err := f.readBuf.Seek(offset, whence)
		if err != nil {
			return 0, err
		}
		f.offset = pos
		return pos, nil
	}

	// For write-only files, compute the new offset.
	var newOffset int64
	switch whence {
	case io.SeekStart:
		newOffset = offset
	case io.SeekCurrent:
		newOffset = f.offset + offset
	case io.SeekEnd:
		newOffset = int64(f.writeBuf.Len()) + offset
	}
	if newOffset < 0 {
		return 0, os.ErrInvalid
	}
	f.offset = newOffset
	return newOffset, nil
}

func (f *virtualFile) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed {
		return nil
	}
	f.closed = true

	// Flush buffered writes to the handler.
	if f.writeHandler != nil && f.writeBuf.Len() > 0 {
		return f.writeHandler(f.writeCtx, f.writeBuf.Bytes())
	}
	return nil
}

func (f *virtualFile) Truncate(size int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.writeHandler == nil {
		return os.ErrPermission
	}
	if size == 0 {
		f.writeBuf.Reset()
	}
	return nil
}

func (f *virtualFile) Lock() error   { return nil }
func (f *virtualFile) Unlock() error { return nil }

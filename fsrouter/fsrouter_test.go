package fsrouter

import (
	"fmt"
	"os"
	"testing"
)

func TestPatternMatch(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		match   bool
		params  map[string]string
	}{
		{"/readme.txt", "/readme.txt", true, map[string]string{}},
		{"/readme.txt", "/other.txt", false, nil},
		{"/", "/", true, map[string]string{}},
		{"/", "/anything", false, nil},
		{"/users/{id}", "/users/alice", true, map[string]string{"id": "alice"}},
		{"/users/{id}", "/users/", false, nil},
		{"/users/{id}", "/users/alice/extra", false, nil},
		{"/users/{id}.json", "/users/alice.json", true, map[string]string{"id": "alice"}},
		{"/users/{id}.json", "/users/alice.txt", false, nil},
		{"/users/{id}.json", "/users/.json", false, nil},
		{"/files/file_{name}", "/files/file_report", true, map[string]string{"name": "report"}},
		{"/orgs/{org}/users/{id}.json", "/orgs/acme/users/alice.json", true, map[string]string{"org": "acme", "id": "alice"}},
		{"/echo/{path...}", "/echo/a/b/c", true, map[string]string{"path": "a/b/c"}},
		{"/echo/{path...}", "/echo/single", true, map[string]string{"path": "single"}},
		{"/api/v1/status.json", "/api/v1/status.json", true, map[string]string{}},
		{"/api/v1/status.json", "/api/v2/status.json", false, nil},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"→"+tt.path, func(t *testing.T) {
			pat := parsePattern(tt.pattern)
			params, ok := pat.match(tt.path)
			if ok != tt.match {
				t.Errorf("match = %v, want %v", ok, tt.match)
				return
			}
			if !ok {
				return
			}
			for k, want := range tt.params {
				if got := params[k]; got != want {
					t.Errorf("param[%q] = %q, want %q", k, got, want)
				}
			}
		})
	}
}

func TestPatternParentDirs(t *testing.T) {
	pat := parsePattern("/a/b/c.json")
	dirs := pat.parentDirs()
	want := []string{"/", "/a", "/a/b"}
	if len(dirs) != len(want) {
		t.Fatalf("parentDirs = %v, want %v", dirs, want)
	}
	for i, d := range dirs {
		if d != want[i] {
			t.Errorf("parentDirs[%d] = %q, want %q", i, d, want[i])
		}
	}
}

func TestRouterResolve(t *testing.T) {
	r := New()
	called := false

	r.Read("/users/{id}.json", func(c *Context, offset int64, length int) ([]byte, error) {
		called = true
		if c.Param("id") != "alice" {
			t.Errorf("param id = %q, want alice", c.Param("id"))
		}
		return []byte("ok"), nil
	})

	handler, ctx := r.resolve(VerbRead, "/users/alice.json")
	if handler == nil {
		t.Fatal("expected handler, got nil")
	}
	data, err := handler.(ReadHandler)(ctx, 0, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("handler was not called")
	}
	if string(data) != "ok" {
		t.Errorf("data = %q, want %q", string(data), "ok")
	}
}

func TestImplicitDirectories(t *testing.T) {
	r := New()
	r.Read("/a/b/c.json", func(c *Context, offset int64, length int) ([]byte, error) { return nil, nil })

	if !r.isImplicitDir("/") {
		t.Error("/ should be implicit dir")
	}
	if !r.isImplicitDir("/a") {
		t.Error("/a should be implicit dir")
	}
	if !r.isImplicitDir("/a/b") {
		t.Error("/a/b should be implicit dir")
	}
	if r.isImplicitDir("/a/b/c.json") {
		t.Error("/a/b/c.json should NOT be implicit dir")
	}
}

func TestImplicitChildren(t *testing.T) {
	r := New()
	r.Read("/a/b/c.json", func(c *Context, offset int64, length int) ([]byte, error) { return nil, nil })
	r.Read("/a/b/d.json", func(c *Context, offset int64, length int) ([]byte, error) { return nil, nil })
	r.Read("/a/x.txt", func(c *Context, offset int64, length int) ([]byte, error) { return nil, nil })

	rootChildren := r.implicitChildren("/")
	if len(rootChildren) != 1 || rootChildren[0] != "a" {
		t.Errorf("root children = %v, want [a]", rootChildren)
	}
	aChildren := r.implicitChildren("/a")
	if len(aChildren) != 2 {
		t.Errorf("a children = %v, want [b, x.txt]", aChildren)
	}
}

func TestGroupRoutes(t *testing.T) {
	r := New()
	api := r.Group("/api/v1")
	api.Read("/status.json", func(c *Context, offset int64, length int) ([]byte, error) {
		return []byte("ok"), nil
	})
	handler, _ := r.resolve(VerbRead, "/api/v1/status.json")
	if handler == nil {
		t.Fatal("expected handler for /api/v1/status.json")
	}
}

func TestGlobImplicitDirectories(t *testing.T) {
	r := New()
	r.Read("/echo/{path...}", func(c *Context, offset int64, length int) ([]byte, error) {
		return []byte("echo: " + c.Param("path")), nil
	})

	if !r.isImplicitDir("/echo") {
		t.Error("/echo should be implicit dir")
	}
	if !r.isImplicitDir("/echo/foo") {
		t.Error("/echo/foo should be implicit dir")
	}
}

func TestStatWithHandler(t *testing.T) {
	r := New()

	r.Stat("/items/{id}.json", func(c *Context) (*FileStat, error) {
		if c.Param("id") == "known" {
			return &FileStat{Size: 42}, nil
		}
		return nil, fmt.Errorf("not found")
	})
	r.Create("/items/{id}.json", func(c *Context, data []byte) error { return nil })

	fs := r.Filesystem()

	info, err := fs.Stat("/items/known.json")
	if err != nil {
		t.Fatalf("stat known: %v", err)
	}
	if info.Size() != 42 {
		t.Errorf("size = %d, want 42", info.Size())
	}

	_, err = fs.Stat("/items/unknown.json")
	if err == nil {
		t.Fatal("stat unknown: expected error, got nil")
	}
}

func TestStatInferredFromRead(t *testing.T) {
	r := New()

	r.Read("/data.txt", func(c *Context, offset int64, length int) ([]byte, error) {
		return []byte("hello"), nil
	})

	fs := r.Filesystem()
	info, err := fs.Stat("/data.txt")
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("size = %d, want 0 (can't infer from pread)", info.Size())
	}
}

// TestCreateReceivesData verifies Create handler gets the full buffered data.
func TestCreateReceivesData(t *testing.T) {
	r := New()

	var created string
	r.Create("/data/{id}.json", func(c *Context, data []byte) error {
		created = string(data)
		return nil
	})

	fs := r.Filesystem()

	// NFS CREATE RPC.
	f, err := fs.Create("/data/new.json")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	f.Close()

	// Pending: stat should succeed.
	info, err := fs.Stat("/data/new.json")
	if err != nil {
		t.Fatalf("stat pending: %v", err)
	}
	if info.IsDir() {
		t.Error("pending file should not be a directory")
	}

	// NFS WRITE RPC: open, write data, close.
	wf, err := fs.OpenFile("/data/new.json", os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("openfile: %v", err)
	}
	wf.Write([]byte(`{"name":"alice"}`))
	wf.Close()

	if created != `{"name":"alice"}` {
		t.Errorf("create handler got %q, want %q", created, `{"name":"alice"}`)
	}

	// After close, pending cleared.
	_, err = fs.Stat("/data/new.json")
	if !os.IsNotExist(err) {
		t.Errorf("after create: expected ErrNotExist, got %v", err)
	}
}

// TestCreateEmptyFile verifies Create handler is called with nil for empty files.
func TestCreateEmptyFile(t *testing.T) {
	r := New()

	createCalled := false
	var createData []byte
	r.Create("/items/{id}", func(c *Context, data []byte) error {
		createCalled = true
		createData = data
		return nil
	})

	fs := r.Filesystem()

	// CREATE then immediately open+close with no writes.
	f, _ := fs.Create("/items/empty")
	f.Close()

	wf, _ := fs.OpenFile("/items/empty", os.O_RDWR, 0644)
	wf.Close() // close with no Write calls

	if !createCalled {
		t.Error("Create handler was not called")
	}
	if createData != nil {
		t.Errorf("data = %v, want nil", createData)
	}
}

// TestWriteExistingFile verifies Write handler is called per-chunk for existing files.
func TestWriteExistingFile(t *testing.T) {
	r := New()

	var lastWrite string
	r.Read("/users/{id}.txt", func(c *Context, offset int64, length int) ([]byte, error) {
		return []byte("original"), nil
	})
	r.Write("/users/{id}.txt", func(c *Context, data []byte, offset int64) error {
		lastWrite = string(data)
		return nil
	})

	fs := r.Filesystem()

	_, err := fs.Stat("/users/alice.txt")
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	wf, err := fs.OpenFile("/users/alice.txt", os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("openfile: %v", err)
	}
	wf.Write([]byte("updated"))
	wf.Close()

	if lastWrite != "updated" {
		t.Errorf("got %q, want %q", lastWrite, "updated")
	}
}

func TestReadAtOffset(t *testing.T) {
	r := New()

	r.Read("/bigfile.bin", func(c *Context, offset int64, length int) ([]byte, error) {
		end := offset + int64(length)
		if end > 100 {
			end = 100
		}
		if offset >= 100 {
			return nil, nil
		}
		data := make([]byte, end-offset)
		for i := range data {
			data[i] = byte(offset + int64(i))
		}
		return data, nil
	})

	r.Stat("/bigfile.bin", func(c *Context) (*FileStat, error) {
		return &FileStat{Size: 100}, nil
	})

	fs := r.Filesystem()

	f, err := fs.Open("/bigfile.bin")
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	buf := make([]byte, 10)
	n, err := f.ReadAt(buf, 50)
	if err != nil {
		t.Fatalf("readat: %v", err)
	}
	if n != 10 || buf[0] != 50 || buf[9] != 59 {
		t.Errorf("readat: n=%d data=%v, want [50..59]", n, buf)
	}
	f.Close()

	// Sequential reads advance offset.
	f2, _ := fs.Open("/bigfile.bin")
	buf2 := make([]byte, 5)
	f2.Read(buf2)
	if buf2[0] != 0 || buf2[4] != 4 {
		t.Errorf("read 1: %v, want [0..4]", buf2)
	}
	f2.Read(buf2)
	if buf2[0] != 5 || buf2[4] != 9 {
		t.Errorf("read 2: %v, want [5..9]", buf2)
	}
	f2.Close()
}

func TestWriteAtOffset(t *testing.T) {
	r := New()

	type chunk struct {
		data   string
		offset int64
	}
	var chunks []chunk

	r.Read("/log.bin", func(c *Context, offset int64, length int) ([]byte, error) {
		return []byte("existing"), nil
	})
	r.Write("/log.bin", func(c *Context, data []byte, offset int64) error {
		chunks = append(chunks, chunk{string(data), offset})
		return nil
	})

	fs := r.Filesystem()

	f1, _ := fs.OpenFile("/log.bin", os.O_RDWR, 0644)
	f1.Write([]byte("chunk1"))
	f1.Close()

	f2, _ := fs.OpenFile("/log.bin", os.O_RDWR, 0644)
	f2.Seek(100, 0)
	f2.Write([]byte("chunk2"))
	f2.Close()

	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2", len(chunks))
	}
	if chunks[0].offset != 0 || chunks[0].data != "chunk1" {
		t.Errorf("chunk[0] = {%q, %d}", chunks[0].data, chunks[0].offset)
	}
	if chunks[1].offset != 100 || chunks[1].data != "chunk2" {
		t.Errorf("chunk[1] = {%q, %d}", chunks[1].data, chunks[1].offset)
	}
}

// TestCreateFallsBackToWrite verifies that if only Write is registered,
// Create still works (Write handles the data per-chunk).
func TestCreateFallsBackToWrite(t *testing.T) {
	r := New()

	var written string
	r.Write("/items/{id}", func(c *Context, data []byte, offset int64) error {
		written = string(data)
		return nil
	})

	fs := r.Filesystem()

	f, err := fs.Create("/items/new")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	f.Close()

	wf, _ := fs.OpenFile("/items/new", os.O_RDWR, 0644)
	wf.Write([]byte("hello"))
	wf.Close()

	if written != "hello" {
		t.Errorf("got %q, want %q", written, "hello")
	}
}

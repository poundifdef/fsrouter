package fsrouter

// ReadHandler is called when a file's contents are read.
// It is the filesystem equivalent of an HTTP GET handler.
// Return the full file contents as a byte slice.
//
//	router.Read("/config/{name}.json", func(c *Context) ([]byte, error) {
//	    return json.Marshal(configs[c.Param("name")])
//	})
type ReadHandler func(ctx *Context) ([]byte, error)

// WriteHandler is called when data is written to a file and the file is closed.
// It receives the complete written data. This is the equivalent of HTTP PUT/POST.
//
//	router.Write("/config/{name}.json", func(c *Context, data []byte) error {
//	    return saveConfig(c.Param("name"), data)
//	})
type WriteHandler func(ctx *Context, data []byte) error

// StatHandler is called to retrieve file metadata.
// This is the equivalent of HTTP HEAD — you describe the resource without returning its body.
// If no StatHandler is registered, fsrouter infers metadata from ReadHandler results.
//
//	router.Stat("/config/{name}.json", func(c *Context) (*FileStat, error) {
//	    return &FileStat{Size: 1024, Mode: 0644}, nil
//	})
type StatHandler func(ctx *Context) (*FileStat, error)

// RemoveHandler is called when a file is deleted.
// This is the equivalent of HTTP DELETE.
//
//	router.Remove("/config/{name}.json", func(c *Context) error {
//	    return deleteConfig(c.Param("name"))
//	})
type RemoveHandler func(ctx *Context) error

// ListHandler is called to enumerate a directory's contents.
// This is the equivalent of HTTP GET on a collection/index endpoint.
//
//	router.List("/config/", func(c *Context) ([]DirEntry, error) {
//	    names := listConfigs()
//	    entries := make([]DirEntry, len(names))
//	    for i, n := range names {
//	        entries[i] = DirEntry{Name: n + ".json", Size: 100}
//	    }
//	    return entries, nil
//	})
type ListHandler func(ctx *Context) ([]DirEntry, error)

// CreateHandler is called when a new file is created (before data is written).
// Return an error to reject the creation. If nil, WriteHandler receives the data on close.
//
//	router.Create("/uploads/{filename}", func(c *Context) error {
//	    if !validFilename(c.Param("filename")) {
//	        return errors.New("invalid filename")
//	    }
//	    return nil
//	})
type CreateHandler func(ctx *Context) error

// MkdirHandler is called when a directory is created.
//
//	router.Mkdir("/projects/{name}/", func(c *Context) error {
//	    return createProject(c.Param("name"))
//	})
type MkdirHandler func(ctx *Context) error

// RenameHandler is called when a file is moved/renamed.
// The context path is the source; newPath is the destination.
//
//	router.Rename("/documents/{name}", func(c *Context, newPath string) error {
//	    return moveDocument(c.Param("name"), newPath)
//	})
type RenameHandler func(ctx *Context, newPath string) error

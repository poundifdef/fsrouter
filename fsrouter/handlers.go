package fsrouter

// ReadHandler is called for each NFS READ RPC.
// Receives the byte offset and requested length; returns the data for that range.
// Matches pread(2).
//
//	router.Read("/users/{id}.json", func(c *Context, offset int64, length int) ([]byte, error) {
//	    data, _ := json.Marshal(db.Get(c.Param("id")))
//	    return sliceRange(data, offset, length), nil
//	})
type ReadHandler func(ctx *Context, offset int64, length int) ([]byte, error)

// WriteHandler is called for each NFS WRITE RPC.
// Receives the data and the byte offset it should be written at.
// Called for both new and existing files.
// Matches pwrite(2).
//
//	router.Write("/users/{id}.json", func(c *Context, data []byte, offset int64) error {
//	    return db.Put(c.Param("id"), data)
//	})
type WriteHandler func(ctx *Context, data []byte, offset int64) error

// CreateHandler is called when a new file is created (NFS CREATE + WRITE sequence).
// The handler receives the complete file contents once the client finishes writing.
// If the client creates an empty file, data will be nil.
//
//	router.Create("/users/{id}.json", func(c *Context, data []byte) error {
//	    return db.Insert(c.Param("id"), data)
//	})
type CreateHandler func(ctx *Context, data []byte) error

// StatHandler returns file metadata.
// Matches stat(2) / fstat(2).
//
//	router.Stat("/users/{id}.json", func(c *Context) (*FileStat, error) {
//	    return &FileStat{Size: 1024, Mode: 0644}, nil
//	})
type StatHandler func(ctx *Context) (*FileStat, error)

// RemoveHandler is called when a file is deleted.
// Matches unlink(2).
//
//	router.Remove("/users/{id}.json", func(c *Context) error {
//	    return db.Delete(c.Param("id"))
//	})
type RemoveHandler func(ctx *Context) error

// ListHandler enumerates a directory's contents.
// Matches readdir(3).
//
//	router.List("/users/", func(c *Context) ([]DirEntry, error) {
//	    return listUsers()
//	})
type ListHandler func(ctx *Context) ([]DirEntry, error)

// MkdirHandler is called when a directory is created.
// Matches mkdir(2).
type MkdirHandler func(ctx *Context) error

// RenameHandler is called when a file is moved/renamed.
// Matches rename(2).
type RenameHandler func(ctx *Context, newPath string) error

# fsrouter

An HTTP-router-style API for building virtual filesystems served over NFS.

```
┌─────────────────────────────────────────────────┐
│               Your Go code                      │
│   Read / Write / Create / Stat / List / …       │
│   router.Read("/users/{id}.json", handler)      │
├─────────────────────────────────────────────────┤
│               fsrouter                          │
│   pattern matching · implicit dirs · VFS        │
├─────────────────────────────────────────────────┤
│               go-nfs (NFSv3)                    │
│   wire protocol · file handles · caching        │
├─────────────────────────────────────────────────┤
│               NFS client                        │
│   mount -t nfs ... / Finder / Explorer          │
└─────────────────────────────────────────────────┘
```

## Quick Start

```go
package main

import (
    "net"
    "github.com/yourorg/fsrouter"
)

func main() {
    router := fsrouter.New()

    router.Read("/hello.txt", func(c *fsrouter.Context, offset int64, length int) ([]byte, error) {
        data := []byte("Hello, world!\n")
        return data[offset:min(offset+int64(length), int64(len(data)))], nil
    })

    ln, _ := net.Listen("tcp", ":2049")
    router.ServeListener(ln)
}
```

Mount it:
```bash
sudo mount -t nfs -o port=2049,mountport=2049,nfsvers=3,tcp,nolock 127.0.0.1:/ ./m
cat ./m/hello.txt   # Hello, world!
```

## Verbs

Each verb maps directly to a POSIX syscall:

| Verb       | Signature                                                    | Syscall     |
|------------|--------------------------------------------------------------|-------------|
| **Read**   | `func(c *Context, offset int64, length int) ([]byte, error)` | pread(2)    |
| **Write**  | `func(c *Context, data []byte, offset int64) error`          | pwrite(2)   |
| **Create** | `func(c *Context, data []byte) error`                        | creat(2)    |
| **Stat**   | `func(c *Context) (*FileStat, error)`                        | stat(2)     |
| **List**   | `func(c *Context) ([]DirEntry, error)`                       | readdir(3)  |
| **Remove** | `func(c *Context) error`                                     | unlink(2)   |
| **Mkdir**  | `func(c *Context) error`                                     | mkdir(2)    |
| **Rename** | `func(c *Context, newPath string) error`                     | rename(2)   |

**Read** is called for every NFS READ RPC with the byte offset and requested length.
**Write** is called for every NFS WRITE RPC with the data and its byte offset. For existing files only.
**Create** is called once when a new file is created, with the complete buffered contents.

## Patterns

```go
router.Read("/readme.txt", handler)                    // exact path
router.Read("/users/{id}.json", handler)               // path parameter
router.Read("/orgs/{org}/users/{id}.json", handler)    // multiple parameters
router.Read("/echo/{path...}", handler)                // catch-all glob
```

Access parameters via `c.Param("id")`.

## CRUD Example

```go
router.Read("/users/{id}.json", func(c *fsrouter.Context, offset int64, length int) ([]byte, error) {
    data, _ := json.Marshal(db.Get(c.Param("id")))
    end := offset + int64(length)
    if end > int64(len(data)) { end = int64(len(data)) }
    return data[offset:end], nil
})

router.Stat("/users/{id}.json", func(c *fsrouter.Context) (*fsrouter.FileStat, error) {
    size := db.Size(c.Param("id"))
    return &fsrouter.FileStat{Size: size}, nil
})

router.Create("/users/{id}.json", func(c *fsrouter.Context, data []byte) error {
    return db.Insert(c.Param("id"), data)
})

router.Write("/users/{id}.json", func(c *fsrouter.Context, data []byte, offset int64) error {
    return db.Put(c.Param("id"), data)
})

router.Remove("/users/{id}.json", func(c *fsrouter.Context) error {
    return db.Delete(c.Param("id"))
})

router.List("/users/", func(c *fsrouter.Context) ([]fsrouter.DirEntry, error) {
    return db.ListUsers()
})
```

### Stat

If you don't register a Stat handler, fsrouter synthesizes one:
- Read handler exists → returns size 0 (can't know without reading).
- Implicit directory → returns directory info.

Register a Stat handler when clients need accurate file sizes (e.g. for `ls -l` or seeking).

### Create vs Write

Create is for new files. Write is for existing files.

- **Create** buffers all writes and delivers the complete contents to your handler.
  Good for small files like JSON, config, etc.
- **Write** is called per NFS WRITE RPC with data + offset (pwrite semantics).
  Good for large files, append logs, or anything where you need offset control.

The NFS protocol lifecycle for a new file:

1. `CREATE` RPC → file marked as pending
2. `WRITE` RPC(s) → data buffered (if Create handler) or delivered per-chunk (if only Write handler)
3. Client closes → Create handler called with complete data, pending cleared

For overwriting existing files, only Write is called (no Create).

If you register both Create and Write for the same pattern, Create handles new files and Write handles overwrites. If you only register Write, it handles both.

## Groups

```go
api := router.Group("/api/v1")
api.Read("/status.json", statusHandler)   // → /api/v1/status.json
api.Write("/config.json", configHandler)  // → /api/v1/config.json
```

## Implicit Directories

Registering `/a/b/c.json` automatically makes `/`, `/a`, and `/a/b` appear as directories.
No Mkdir handler needed for intermediate paths.
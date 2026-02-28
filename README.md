# fsrouter

**Build virtual filesystems as easily as HTTP routes.**

`fsrouter` maps the familiar HTTP router pattern onto filesystem operations. Just as `net/http` lets you register handlers for URL paths with verbs like GET, POST, DELETE — `fsrouter` lets you register handlers for file paths with verbs like **Read**, **Write**, **List**, **Remove**.

The result is served over **NFSv3** via [`go-nfs`](https://github.com/willscott/go-nfs) and can be mounted by any standard NFS client.

```
 HTTP                          Filesystem
┌──────────────────────┐      ┌──────────────────────────┐
│ GET    /users/:id    │  ──▶ │ Read   /users/{id}.json  │
│ POST   /users/:id    │  ──▶ │ Write  /users/{id}.json  │
│ HEAD   /users/:id    │  ──▶ │ Stat   /users/{id}.json  │
│ DELETE /users/:id    │  ──▶ │ Remove /users/{id}.json  │
│ GET    /users/       │  ──▶ │ List   /users/           │
└──────────────────────┘      └──────────────────────────┘
```

## Quick Start

```go
package main

import (
    "encoding/json"
    "log"
    "github.com/yourorg/fsrouter"
)

func main() {
    router := fsrouter.New()

    // READ — like HTTP GET
    router.Read("/hello.txt", func(c *fsrouter.Context) ([]byte, error) {
        return []byte("Hello, World!\n"), nil
    })

    // READ with path parameters — like /users/:id
    router.Read("/users/{id}.json", func(c *fsrouter.Context) ([]byte, error) {
        user := getUser(c.Param("id"))
        return json.Marshal(user)
    })

    // WRITE — like HTTP PUT/POST
    router.Write("/users/{id}.json", func(c *fsrouter.Context, data []byte) error {
        var user User
        json.Unmarshal(data, &user)
        return saveUser(c.Param("id"), user)
    })

    // LIST — like HTTP GET on a collection
    router.List("/users/", func(c *fsrouter.Context) ([]fsrouter.DirEntry, error) {
        names := listUserIDs()
        entries := make([]fsrouter.DirEntry, len(names))
        for i, n := range names {
            entries[i] = fsrouter.DirEntry{Name: n + ".json", Size: 100}
        }
        return entries, nil
    })

    // REMOVE — like HTTP DELETE
    router.Remove("/users/{id}.json", func(c *fsrouter.Context) error {
        return deleteUser(c.Param("id"))
    })

    log.Fatal(router.Serve(":2049"))
}
```

Mount it:

```bash
# macOS
mkdir -p /tmp/mnt
mount -o port=2049,mountport=2049,nfsvers=3,noacl,tcp -t nfs localhost:/mount /tmp/mnt

# Linux
mkdir -p /tmp/mnt
mount -o port=2049,mountport=2049,vers=3,tcp -t nfs localhost:/ /tmp/mnt
```

Now use standard tools:

```bash
ls /tmp/mnt/users/              # → alice.json  bob.json
cat /tmp/mnt/users/alice.json   # → {"name":"alice","role":"admin"}
echo '{"role":"new"}' > /tmp/mnt/users/dave.json   # Creates a user
rm /tmp/mnt/users/bob.json      # Deletes a user
cat /tmp/mnt/hello.txt          # → Hello, World!
```

## Verb Reference

| Verb       | Signature                                              | Filesystem Op   | HTTP Equivalent |
|------------|--------------------------------------------------------|-----------------|-----------------|
| **Read**   | `func(c *Context) ([]byte, error)`                     | `open` + `read` | GET             |
| **Write**  | `func(c *Context, data []byte) error`                  | `write`+`close` | PUT / POST      |
| **Stat**   | `func(c *Context) (*FileStat, error)`                  | `stat`          | HEAD            |
| **List**   | `func(c *Context) ([]DirEntry, error)`                 | `readdir`       | GET (collection)|
| **Remove** | `func(c *Context) error`                               | `unlink`        | DELETE          |
| **Create** | `func(c *Context) error`                               | `create`        | POST (create)   |
| **Mkdir**  | `func(c *Context) error`                               | `mkdir`         | MKCOL           |
| **Rename** | `func(c *Context, newPath string) error`               | `rename`        | MOVE            |

## Path Patterns

Patterns follow the same conventions as HTTP routers:

```go
"/config.json"          // Exact match
"/users/{id}"           // Captures path segment into "id"
"/users/{id}.json"      // Captures "alice" from "alice.json"
"/files/{path...}"      // Glob — captures "a/b/c" from "/files/a/b/c"
"/users/"               // Directory (trailing slash) — used with List
```

## Route Groups

Group routes under a shared prefix, exactly like HTTP sub-routers:

```go
api := router.Group("/api/v1")
api.Read("/status.json", statusHandler)    // /api/v1/status.json
api.List("/users/", listUsersHandler)      // /api/v1/users/

admin := api.Group("/admin")
admin.Read("/config.json", configHandler)  // /api/v1/admin/config.json
```

## Middleware

Add cross-cutting concerns just like HTTP middleware:

```go
router.Use(func(verb fsrouter.Verb, path string, next func() error) error {
    start := time.Now()
    err := next()
    log.Printf("[%v] %s took %v", verb, path, time.Since(start))
    return err
})
```

## Implicit Directories

Registering any route implicitly creates its parent directories. If you register:

```go
router.Read("/a/b/c.json", handler)
```

Then `ls /`, `ls /a/`, and `ls /a/b/` all work automatically. The directories `/`, `/a/`, and `/a/b/` are synthesized from the route tree without needing explicit `List` handlers.

To provide **dynamic** directory listings (where entries come from a database, API, etc.), register an explicit `List` handler:

```go
router.List("/a/b/", func(c *fsrouter.Context) ([]fsrouter.DirEntry, error) {
    // Return entries from your data source
})
```

## Stat Inference

If you don't register an explicit `Stat` handler, `fsrouter` infers file metadata automatically:

1. For files with a `Read` handler: calls the handler and uses the result length as the file size
2. For files with only a `Write` handler: reports size 0
3. For directories: reports standard directory metadata (mode 0755, size 4096)

Register a `Stat` handler when calling `Read` is expensive and you can provide metadata more cheaply:

```go
router.Stat("/large-files/{id}", func(c *fsrouter.Context) (*fsrouter.FileStat, error) {
    size, _ := db.GetFileSize(c.Param("id"))
    return &fsrouter.FileStat{Size: size}, nil
})
```

## Architecture

```
┌─────────────────────────────────────────────────┐
│                  NFS Client                     │
│          (mount, ls, cat, echo, rm)             │
└───────────────────┬─────────────────────────────┘
                    │ NFSv3 protocol
┌───────────────────▼─────────────────────────────┐
│              go-nfs server                      │
│         (willscott/go-nfs)                      │
└───────────────────┬─────────────────────────────┘
                    │ billy.Filesystem interface
┌───────────────────▼─────────────────────────────┐
│                VFS layer                        │
│   Dispatches billy operations to matched routes │
└───────────────────┬─────────────────────────────┘
                    │ pattern matching
┌───────────────────▼─────────────────────────────┐
│                Router                           │
│   Read / Write / Stat / List / Remove / ...     │
│   Path patterns: /users/{id}.json               │
│   Groups: router.Group("/api/v1")               │
│   Middleware chain                              │
└───────────────────┬─────────────────────────────┘
                    │ your handler functions
┌───────────────────▼─────────────────────────────┐
│            Your Application Logic               │
│   (databases, APIs, generators, etc.)           │
└─────────────────────────────────────────────────┘
```

## Use Cases

- **Database-as-a-filesystem**: Expose database records as JSON files
- **API gateway**: Mount a REST API as a local filesystem
- **Config management**: Virtual config files backed by etcd/Consul
- **Build systems**: Generate files on-the-fly from templates
- **Dev tools**: Hot-reload virtual assets during development
- **IoT dashboards**: Sensor readings as readable files
- **Log aggregation**: Stream logs by reading virtual files

## License

Apache 2.0

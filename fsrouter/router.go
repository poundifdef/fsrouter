package fsrouter

import (
	"fmt"
	"log"
	"net"
	"path"
	"strings"
	"sync"

	nfs "github.com/willscott/go-nfs"
	nfshelper "github.com/willscott/go-nfs/helpers"
)

// Router is the central type of fsrouter. It maps filesystem paths to handler
// functions, just as an HTTP router maps URL paths to HTTP handlers.
//
// Usage mirrors the familiar HTTP router pattern:
//
//	router := fsrouter.New()
//
//	router.Read("/users/{id}.json", func(c *fsrouter.Context) ([]byte, error) { ... })
//	router.Write("/users/{id}.json", func(c *fsrouter.Context, data []byte) error { ... })
//	router.List("/users/", func(c *fsrouter.Context) ([]fsrouter.DirEntry, error) { ... })
//
//	log.Fatal(router.Serve(":2049"))
type Router struct {
	mu sync.RWMutex

	// Route tables, keyed by verb.
	reads   []*route
	writes  []*route
	stats   []*route
	removes []*route
	lists   []*route
	creates []*route
	mkdirs  []*route
	renames []*route

	// dirTree tracks the implicit directory structure derived from registered routes.
	// This allows the VFS to resolve intermediate directories (e.g., registering
	// "/a/b/c.txt" implicitly creates directories "/", "/a", "/a/b").
	dirTree *dirNode

	// Middleware applied to every handler invocation.
	middleware []Middleware

	// Logger for filesystem operations. Defaults to log.Default().
	Logger *log.Logger

	// HandleCacheSize controls how many NFS file handles are cached.
	// Defaults to 1024.
	HandleCacheSize int
}

// Middleware wraps a handler invocation, allowing cross-cutting concerns
// like logging, auth checks, or metrics.
type Middleware func(verb Verb, path string, next func() error) error

// route binds a pattern to a handler for a specific verb.
type route struct {
	pattern *pattern
	handler interface{}
}

// dirNode is a node in the implicit directory tree.
type dirNode struct {
	children  map[string]*dirNode
	isDynamic bool   // true if this segment is a {param}
	paramName string // non-empty if isDynamic
	isExplicit bool  // true if a List/Mkdir handler is registered here
}

// New creates a new Router with sensible defaults.
func New() *Router {
	return &Router{
		dirTree: &dirNode{
			children: make(map[string]*dirNode),
		},
		HandleCacheSize: 1024,
		Logger:          log.Default(),
	}
}

// --------------------------------------------------------------------------
// Route registration — the "verbs"
// --------------------------------------------------------------------------

// Read registers a handler invoked when a file at the given pattern is read.
// This is the filesystem equivalent of http.HandleFunc with GET.
//
//	router.Read("/greet/{name}.txt", func(c *Context) ([]byte, error) {
//	    return []byte("Hello, " + c.Param("name") + "!"), nil
//	})
func (r *Router) Read(pattern string, handler ReadHandler) {
	r.addRoute(VerbRead, pattern, handler)
}

// Write registers a handler invoked when a file at the given pattern is written and closed.
// The handler receives the complete data that was written.
//
//	router.Write("/notes/{id}.txt", func(c *Context, data []byte) error {
//	    return db.Save(c.Param("id"), data)
//	})
func (r *Router) Write(pattern string, handler WriteHandler) {
	r.addRoute(VerbWrite, pattern, handler)
}

// Stat registers a handler that returns metadata about a file.
// If no Stat handler is registered, fsrouter synthesizes metadata by calling
// the Read handler (if one exists) and reporting the result length.
//
//	router.Stat("/notes/{id}.txt", func(c *Context) (*FileStat, error) {
//	    size, _ := db.Size(c.Param("id"))
//	    return &FileStat{Size: size, Mode: 0644}, nil
//	})
func (r *Router) Stat(pattern string, handler StatHandler) {
	r.addRoute(VerbStat, pattern, handler)
}

// Remove registers a handler invoked when a file is deleted.
//
//	router.Remove("/notes/{id}.txt", func(c *Context) error {
//	    return db.Delete(c.Param("id"))
//	})
func (r *Router) Remove(pattern string, handler RemoveHandler) {
	r.addRoute(VerbRemove, pattern, handler)
}

// List registers a handler that returns directory entries for the given path.
// Patterns for List should end with "/" to indicate a directory.
//
//	router.List("/notes/", func(c *Context) ([]DirEntry, error) {
//	    ids := db.ListIDs()
//	    entries := make([]DirEntry, len(ids))
//	    for i, id := range ids {
//	        entries[i] = DirEntry{Name: id + ".txt", Size: 64}
//	    }
//	    return entries, nil
//	})
func (r *Router) List(pattern string, handler ListHandler) {
	r.addRoute(VerbList, pattern, handler)
}

// Create registers a handler invoked when a new file is created.
// Return an error to reject the creation. On success, the subsequent write
// data will be delivered to the matching Write handler.
func (r *Router) Create(pattern string, handler CreateHandler) {
	r.addRoute(VerbCreate, pattern, handler)
}

// Mkdir registers a handler invoked when a directory is created.
func (r *Router) Mkdir(pattern string, handler MkdirHandler) {
	r.addRoute(VerbMkdir, pattern, handler)
}

// Rename registers a handler invoked when a file is renamed/moved.
func (r *Router) Rename(pattern string, handler RenameHandler) {
	r.addRoute(VerbRename, pattern, handler)
}

// Use adds middleware that wraps every handler invocation.
func (r *Router) Use(mw Middleware) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.middleware = append(r.middleware, mw)
}

// --------------------------------------------------------------------------
// Route groups — like http.ServeMux groups / chi.Router.Route
// --------------------------------------------------------------------------

// Group creates a sub-router with a shared path prefix.
// This is the filesystem equivalent of mounting a sub-router at a prefix.
//
//	api := router.Group("/api/v1")
//	api.Read("/users/{id}.json", handler)  // matches /api/v1/users/{id}.json
//	api.List("/users/", listHandler)       // matches /api/v1/users/
func (r *Router) Group(prefix string) *Group {
	return &Group{
		router: r,
		prefix: path.Clean("/" + prefix),
	}
}

// Group is a sub-router that prefixes all registered patterns.
type Group struct {
	router *Router
	prefix string
}

func (g *Group) Read(pattern string, handler ReadHandler)       { g.router.Read(g.prefix+pattern, handler) }
func (g *Group) Write(pattern string, handler WriteHandler)     { g.router.Write(g.prefix+pattern, handler) }
func (g *Group) Stat(pattern string, handler StatHandler)       { g.router.Stat(g.prefix+pattern, handler) }
func (g *Group) Remove(pattern string, handler RemoveHandler)   { g.router.Remove(g.prefix+pattern, handler) }
func (g *Group) List(pattern string, handler ListHandler)       { g.router.List(g.prefix+pattern, handler) }
func (g *Group) Create(pattern string, handler CreateHandler)   { g.router.Create(g.prefix+pattern, handler) }
func (g *Group) Mkdir(pattern string, handler MkdirHandler)     { g.router.Mkdir(g.prefix+pattern, handler) }
func (g *Group) Rename(pattern string, handler RenameHandler)   { g.router.Rename(g.prefix+pattern, handler) }

// Group creates a nested group.
func (g *Group) Group(prefix string) *Group {
	return &Group{
		router: g.router,
		prefix: path.Clean(g.prefix + "/" + prefix),
	}
}

// --------------------------------------------------------------------------
// Serving
// --------------------------------------------------------------------------

// Serve starts an NFS server on the given address and blocks until it exits.
// The address should be in "host:port" form (e.g., ":2049").
//
//	log.Fatal(router.Serve(":2049"))
func (r *Router) Serve(addr string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("fsrouter: listen %s: %w", addr, err)
	}
	return r.ServeListener(listener)
}

// ServeListener starts an NFS server on an existing net.Listener.
// This is useful when you need control over the listener (e.g., for testing).
func (r *Router) ServeListener(listener net.Listener) error {
	fs := r.Filesystem()
	handler := nfshelper.NewNullAuthHandler(fs)
	cacheHandler := nfshelper.NewCachingHandler(handler, r.HandleCacheSize)

	r.Logger.Printf("fsrouter: NFS server listening on %s", listener.Addr())
	return nfs.Serve(listener, cacheHandler)
}

// Filesystem returns the billy.Filesystem implementation backed by this router.
// Useful if you want to embed the filesystem in another context without NFS.
func (r *Router) Filesystem() *VFS {
	return &VFS{router: r}
}

// --------------------------------------------------------------------------
// Internal route management
// --------------------------------------------------------------------------

func (r *Router) addRoute(verb Verb, rawPattern string, handler interface{}) {
	r.mu.Lock()
	defer r.mu.Unlock()

	pat := parsePattern(rawPattern)
	rt := &route{pattern: pat, handler: handler}

	switch verb {
	case VerbRead:
		r.reads = append(r.reads, rt)
	case VerbWrite:
		r.writes = append(r.writes, rt)
	case VerbStat:
		r.stats = append(r.stats, rt)
	case VerbRemove:
		r.removes = append(r.removes, rt)
	case VerbList:
		r.lists = append(r.lists, rt)
	case VerbCreate:
		r.creates = append(r.creates, rt)
	case VerbMkdir:
		r.mkdirs = append(r.mkdirs, rt)
	case VerbRename:
		r.renames = append(r.renames, rt)
	}

	// Update the implicit directory tree.
	r.registerDirs(pat)
}

// registerDirs adds implicit directory entries for a pattern's static prefix.
func (r *Router) registerDirs(pat *pattern) {
	node := r.dirTree
	for _, seg := range pat.segments {
		name := seg.literal
		if seg.isParam {
			name = "{" + seg.param + "}"
		}
		if seg.isGlob {
			break
		}

		child, ok := node.children[name]
		if !ok {
			child = &dirNode{
				children:  make(map[string]*dirNode),
				isDynamic: seg.isParam,
				paramName: seg.param,
			}
			node.children[name] = child
		}
		node = child
	}

	if pat.isDir {
		node.isExplicit = true
	}
}

// resolve finds the first matching route for the given verb and path.
func (r *Router) resolve(verb Verb, filePath string) (interface{}, *Context) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var routes []*route
	switch verb {
	case VerbRead:
		routes = r.reads
	case VerbWrite:
		routes = r.writes
	case VerbStat:
		routes = r.stats
	case VerbRemove:
		routes = r.removes
	case VerbList:
		routes = r.lists
	case VerbCreate:
		routes = r.creates
	case VerbMkdir:
		routes = r.mkdirs
	case VerbRename:
		routes = r.renames
	}

	for _, rt := range routes {
		if params, ok := rt.pattern.match(filePath); ok {
			return rt.handler, newContext(filePath, params)
		}
	}

	return nil, nil
}

// isImplicitDir checks whether filePath is a directory implied by registered routes.
func (r *Router) isImplicitDir(filePath string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	filePath = path.Clean("/" + filePath)
	if filePath == "/" {
		return true
	}

	parts := strings.Split(strings.Trim(filePath, "/"), "/")
	node := r.dirTree
	for _, part := range parts {
		child, ok := node.children[part]
		if ok {
			node = child
			continue
		}
		// Try dynamic children.
		found := false
		for _, c := range node.children {
			if c.isDynamic {
				node = c
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// It's a directory if it has children or is explicitly registered.
	return len(node.children) > 0 || node.isExplicit
}

// implicitChildren returns the static child names for a given directory path.
func (r *Router) implicitChildren(dirPath string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	dirPath = path.Clean("/" + dirPath)
	node := r.dirTree

	if dirPath != "/" {
		parts := strings.Split(strings.Trim(dirPath, "/"), "/")
		for _, part := range parts {
			child, ok := node.children[part]
			if ok {
				node = child
				continue
			}
			found := false
			for _, c := range node.children {
				if c.isDynamic {
					node = c
					found = true
					break
				}
			}
			if !found {
				return nil
			}
		}
	}

	var names []string
	for name, child := range node.children {
		if child.isDynamic {
			continue // Can't enumerate dynamic children.
		}
		names = append(names, name)
	}
	return names
}

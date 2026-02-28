// Example: A virtual NFS filesystem that serves a REST-like API as files.
//
// Mount with:
//
//	# macOS:
//	mount -o port=2049,mountport=2049,nfsvers=3,noacl,tcp -t nfs localhost:/mount /tmp/mnt
//
//	# Linux:
//	mount -o port=2049,mountport=2049,vers=3,tcp -t nfs localhost:/ /tmp/mnt
//
// Then interact with standard filesystem tools:
//
//	ls /tmp/mnt/users/                    # Lists all users
//	cat /tmp/mnt/users/alice.json         # Reads a user profile
//	echo '{"role":"admin"}' > /tmp/mnt/users/bob.json  # Creates/updates a user
//	rm /tmp/mnt/users/alice.json          # Deletes a user
//	cat /tmp/mnt/greet/world.txt          # Reads "Hello, world!"
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	"f/fsrouter"
)

// Simple in-memory user store.
type User struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

var (
	users   = map[string]*User{}
	usersMu sync.RWMutex
)

func init() {
	users["alice"] = &User{Name: "alice", Role: "admin"}
	users["bob"] = &User{Name: "bob", Role: "viewer"}
	users["charlie"] = &User{Name: "charlie", Role: "editor"}
}

func main() {
	router := fsrouter.New()

	// ---------------------------------------------------------------
	// Static files — like serving GET /readme.txt
	// ---------------------------------------------------------------
	router.Read("/readme.txt", func(c *fsrouter.Context) ([]byte, error) {
		return []byte("Welcome to the virtual filesystem!\n"), nil
	})

	// ---------------------------------------------------------------
	// Dynamic routes with parameters — just like HTTP path params
	// ---------------------------------------------------------------
	router.Read("/greet/{name}.txt", func(c *fsrouter.Context) ([]byte, error) {
		return []byte(fmt.Sprintf("Hello, %s!\n", c.Param("name"))), nil
	})

	// ---------------------------------------------------------------
	// CRUD on /users/ — the full verb set
	// ---------------------------------------------------------------

	// LIST — ls /users/
	router.List("/users/", func(c *fsrouter.Context) ([]fsrouter.DirEntry, error) {
		usersMu.RLock()
		defer usersMu.RUnlock()

		entries := make([]fsrouter.DirEntry, 0, len(users))
		for name, u := range users {
			data, _ := json.Marshal(u)
			entries = append(entries, fsrouter.DirEntry{
				Name: name + ".json",
				Size: int64(len(data)),
			})
		}
		return entries, nil
	})

	// READ — cat /users/alice.json
	router.Read("/users/{id}.json", func(c *fsrouter.Context) ([]byte, error) {
		usersMu.RLock()
		defer usersMu.RUnlock()

		u, ok := users[c.Param("id")]
		if !ok {
			return nil, fmt.Errorf("user %q not found", c.Param("id"))
		}
		return json.MarshalIndent(u, "", "  ")
	})

	// STAT — efficient metadata without reading the full file
	router.Stat("/users/{id}.json", func(c *fsrouter.Context) (*fsrouter.FileStat, error) {
		usersMu.RLock()
		defer usersMu.RUnlock()

		u, ok := users[c.Param("id")]
		if !ok {
			return nil, fmt.Errorf("user %q not found", c.Param("id"))
		}
		data, _ := json.Marshal(u)
		return &fsrouter.FileStat{Size: int64(len(data))}, nil
	})

	// WRITE — echo '{"name":"dave","role":"viewer"}' > /users/dave.json
	router.Write("/users/{id}.json", func(c *fsrouter.Context, data []byte) error {
		var u User
		if err := json.Unmarshal(data, &u); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		u.Name = c.Param("id")

		usersMu.Lock()
		defer usersMu.Unlock()
		users[c.Param("id")] = &u

		log.Printf("wrote user: %s", c.Param("id"))
		return nil
	})

	// REMOVE — rm /users/alice.json
	router.Remove("/users/{id}.json", func(c *fsrouter.Context) error {
		usersMu.Lock()
		defer usersMu.Unlock()

		id := c.Param("id")
		if _, ok := users[id]; !ok {
			return fmt.Errorf("user %q not found", id)
		}
		delete(users, id)
		log.Printf("deleted user: %s", id)
		return nil
	})

	// ---------------------------------------------------------------
	// Route groups — like http.Router.Group("/api/v1")
	// ---------------------------------------------------------------
	api := router.Group("/api/v1")

	api.Read("/status.json", func(c *fsrouter.Context) ([]byte, error) {
		usersMu.RLock()
		count := len(users)
		usersMu.RUnlock()

		return json.Marshal(map[string]interface{}{
			"status":     "ok",
			"user_count": count,
		})
	})

	api.List("/", func(c *fsrouter.Context) ([]fsrouter.DirEntry, error) {
		return []fsrouter.DirEntry{
			{Name: "status.json", Size: 50},
		}, nil
	})

	// ---------------------------------------------------------------
	// Glob routes — catch-all for deep paths
	// ---------------------------------------------------------------
	router.Read("/echo/{path...}", func(c *fsrouter.Context) ([]byte, error) {
		return []byte(fmt.Sprintf("You accessed: /%s\n", c.Param("path"))), nil
	})

	// ---------------------------------------------------------------
	// Middleware — logging, just like HTTP middleware
	// ---------------------------------------------------------------
	router.Use(func(verb fsrouter.Verb, path string, next func() error) error {
		verbNames := map[fsrouter.Verb]string{
			fsrouter.VerbRead: "READ", fsrouter.VerbWrite: "WRITE",
			fsrouter.VerbStat: "STAT", fsrouter.VerbRemove: "REMOVE",
			fsrouter.VerbList: "LIST",
		}
		name := verbNames[verb]
		if name == "" {
			name = fmt.Sprintf("VERB(%d)", verb)
		}
		log.Printf("[%s] %s", name, path)
		return next()
	})

	// ---------------------------------------------------------------
	// Print the filesystem tree for clarity
	// ---------------------------------------------------------------
	fmt.Println(strings.Repeat("─", 50))
	fmt.Println("fsrouter virtual filesystem")
	fmt.Println(strings.Repeat("─", 50))
	fmt.Println("Routes:")
	fmt.Println("  READ    /readme.txt")
	fmt.Println("  READ    /greet/{name}.txt")
	fmt.Println("  LIST    /users/")
	fmt.Println("  READ    /users/{id}.json")
	fmt.Println("  STAT    /users/{id}.json")
	fmt.Println("  WRITE   /users/{id}.json")
	fmt.Println("  REMOVE  /users/{id}.json")
	fmt.Println("  READ    /api/v1/status.json")
	fmt.Println("  LIST    /api/v1/")
	fmt.Println("  READ    /echo/{path...}")
	fmt.Println(strings.Repeat("─", 50))

	log.Fatal(router.Serve(":2049"))
}

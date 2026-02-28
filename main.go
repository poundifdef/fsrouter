package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"

	"f/fsrouter"
)

type User struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

var (
	mu    sync.RWMutex
	users = map[string]*User{
		"alice": {ID: "alice", Name: "Alice Smith", Email: "alice@example.com"},
		"bob":   {ID: "bob", Name: "Bob Jones", Email: "bob@example.com"},
	}
)

func main() {
	router := fsrouter.New()

	// --- Static files ---

	router.Read("/readme.txt", func(c *fsrouter.Context, offset int64, length int) ([]byte, error) {
		data := []byte("Welcome to the virtual filesystem.\nPowered by fsrouter.\n")
		return sliceRange(data, offset, length), nil
	})

	router.Read("/greet/{name}.txt", func(c *fsrouter.Context, offset int64, length int) ([]byte, error) {
		data := []byte(fmt.Sprintf("Hello, %s!\n", c.Param("name")))
		return sliceRange(data, offset, length), nil
	})

	// --- Users CRUD ---

	router.List("/users/", func(c *fsrouter.Context) ([]fsrouter.DirEntry, error) {
		mu.RLock()
		defer mu.RUnlock()
		entries := make([]fsrouter.DirEntry, 0, len(users))
		for id := range users {
			entries = append(entries, fsrouter.DirEntry{Name: id + ".json", Size: 100})
		}
		return entries, nil
	})

	router.Read("/users/{id}.json", func(c *fsrouter.Context, offset int64, length int) ([]byte, error) {
		mu.RLock()
		defer mu.RUnlock()
		u, ok := users[c.Param("id")]
		if !ok {
			return nil, fmt.Errorf("not found")
		}
		data, _ := json.MarshalIndent(u, "", "  ")
		return sliceRange(data, offset, length), nil
	})

	router.Stat("/users/{id}.json", func(c *fsrouter.Context) (*fsrouter.FileStat, error) {
		mu.RLock()
		defer mu.RUnlock()
		u, ok := users[c.Param("id")]
		if !ok {
			return nil, fmt.Errorf("not found")
		}
		data, _ := json.MarshalIndent(u, "", "  ")
		return &fsrouter.FileStat{Size: int64(len(data))}, nil
	})

	router.Create("/users/{id}.json", func(c *fsrouter.Context, data []byte) error {
		var u User
		if err := json.Unmarshal(data, &u); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		u.ID = c.Param("id")
		mu.Lock()
		defer mu.Unlock()
		if _, exists := users[u.ID]; exists {
			return fmt.Errorf("user %s already exists", u.ID)
		}
		users[u.ID] = &u
		log.Printf("CREATE /users/%s.json (%d bytes)", u.ID, len(data))
		return nil
	})

	router.Write("/users/{id}.json", func(c *fsrouter.Context, data []byte, offset int64) error {
		var u User
		if err := json.Unmarshal(data, &u); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		u.ID = c.Param("id")
		mu.Lock()
		defer mu.Unlock()
		users[u.ID] = &u
		log.Printf("WRITE /users/%s.json at offset %d (%d bytes)", u.ID, offset, len(data))
		return nil
	})

	router.Remove("/users/{id}.json", func(c *fsrouter.Context) error {
		mu.Lock()
		defer mu.Unlock()
		id := c.Param("id")
		if _, ok := users[id]; !ok {
			return fmt.Errorf("not found")
		}
		delete(users, id)
		log.Printf("REMOVE /users/%s.json", id)
		return nil
	})

	// --- Catch-all echo ---

	router.Read("/echo/{path...}", func(c *fsrouter.Context, offset int64, length int) ([]byte, error) {
		data := []byte("echo: " + c.Param("path") + "\n")
		return sliceRange(data, offset, length), nil
	})

	// --- Serve ---

	listener, err := net.Listen("tcp", "127.0.0.1:2049")
	if err != nil {
		log.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	fmt.Printf("Serving NFS on 127.0.0.1:%d\n", port)
	fmt.Printf("Mount with: sudo mount -t nfs -o port=%d,mountport=%d,nfsvers=3,tcp,nolock 127.0.0.1:/ /mnt/point\n", port, port)
	log.Fatal(router.ServeListener(listener))
}

// sliceRange is a helper for implementing ReadHandler on in-memory data.
func sliceRange(data []byte, offset int64, length int) []byte {
	if offset >= int64(len(data)) {
		return nil
	}
	end := offset + int64(length)
	if end > int64(len(data)) {
		end = int64(len(data))
	}
	return data[offset:end]
}

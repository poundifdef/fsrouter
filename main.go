package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sync"
	"syscall"

	"github.com/rs/zerolog"

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
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <mountpoint>\n", os.Args[0])
		os.Exit(1)
	}
	mountpoint := os.Args[1]

	logger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).
		With().Timestamp().Logger()

	router := fsrouter.New()
	router.Logger = logger

	// Add logging middleware for all handler invocations.
	router.Use(fsrouter.LoggingMiddleware(logger.With().Str("layer", "handler").Logger()))

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
		logger.Info().Str("id", u.ID).Int("bytes", len(data)).Msg("created user")
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
		logger.Info().Str("id", u.ID).Int64("offset", offset).Int("bytes", len(data)).Msg("wrote user")
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
		logger.Info().Str("id", id).Msg("removed user")
		return nil
	})

	// --- Catch-all echo ---

	router.Read("/echo/{path...}", func(c *fsrouter.Context, offset int64, length int) ([]byte, error) {
		data := []byte("echo: " + c.Param("path") + "\n")
		return sliceRange(data, offset, length), nil
	})

	// --- Serve and mount ---

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to listen")
	}
	port := listener.Addr().(*net.TCPAddr).Port

	// Start NFS server in background.
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- router.ServeListener(listener)
	}()

	// Mount the NFS filesystem.
	if err := nfsMount(mountpoint, port); err != nil {
		listener.Close()
		logger.Fatal().Err(err).Msg("failed to mount")
	}
	logger.Info().Str("mountpoint", mountpoint).Int("port", port).Msg("mounted")

	// Wait for SIGINT/SIGTERM.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	select {
	case s := <-sig:
		logger.Info().Str("signal", s.String()).Msg("shutting down")
	case err := <-serverErr:
		logger.Error().Err(err).Msg("server exited unexpectedly")
	}

	// Unmount and stop.
	if err := nfsUnmount(mountpoint); err != nil {
		logger.Error().Err(err).Msg("failed to unmount")
	} else {
		logger.Info().Str("mountpoint", mountpoint).Msg("unmounted")
	}
	listener.Close()
}

func nfsMount(mountpoint string, port int) error {
	if err := os.MkdirAll(mountpoint, 0755); err != nil {
		return err
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("mount", "-t", "nfs",
			"-o", fmt.Sprintf("port=%d,mountport=%d,nfsvers=3,tcp,nolock", port, port),
			"127.0.0.1:/", mountpoint)
	case "linux":
		cmd = exec.Command("mount", "-t", "nfs",
			"-o", fmt.Sprintf("port=%d,mountport=%d,nfsvers=3,tcp,nolock,addr=127.0.0.1", port, port),
			"127.0.0.1:/", mountpoint)
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func nfsUnmount(mountpoint string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("umount", mountpoint)
	case "linux":
		cmd = exec.Command("umount", "-l", mountpoint)
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
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

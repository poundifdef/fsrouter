package fsrouter

import (
	"testing"
)

func TestPatternMatch(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		match   bool
		params  map[string]string
	}{
		// Exact matches.
		{"/readme.txt", "/readme.txt", true, map[string]string{}},
		{"/readme.txt", "/other.txt", false, nil},
		{"/", "/", true, map[string]string{}},
		{"/", "/anything", false, nil},

		// Simple parameter.
		{"/users/{id}", "/users/alice", true, map[string]string{"id": "alice"}},
		{"/users/{id}", "/users/", false, nil},
		{"/users/{id}", "/users/alice/extra", false, nil},

		// Parameter with suffix.
		{"/users/{id}.json", "/users/alice.json", true, map[string]string{"id": "alice"}},
		{"/users/{id}.json", "/users/alice.txt", false, nil},
		{"/users/{id}.json", "/users/.json", false, nil},

		// Parameter with prefix.
		{"/files/file_{name}", "/files/file_report", true, map[string]string{"name": "report"}},

		// Multiple parameters.
		{"/orgs/{org}/users/{id}.json", "/orgs/acme/users/alice.json", true, map[string]string{"org": "acme", "id": "alice"}},

		// Glob.
		{"/echo/{path...}", "/echo/a/b/c", true, map[string]string{"path": "a/b/c"}},
		{"/echo/{path...}", "/echo/single", true, map[string]string{"path": "single"}},

		// Nested literal paths.
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
				got := params[k]
				if got != want {
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

	r.Read("/users/{id}.json", func(c *Context) ([]byte, error) {
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

	data, err := handler.(ReadHandler)(ctx)
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

	r.Read("/a/b/c.json", func(c *Context) ([]byte, error) {
		return nil, nil
	})

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
	if r.isImplicitDir("/x") {
		t.Error("/x should NOT be implicit dir")
	}
}

func TestImplicitChildren(t *testing.T) {
	r := New()

	r.Read("/a/b/c.json", func(c *Context) ([]byte, error) { return nil, nil })
	r.Read("/a/b/d.json", func(c *Context) ([]byte, error) { return nil, nil })
	r.Read("/a/x.txt", func(c *Context) ([]byte, error) { return nil, nil })

	rootChildren := r.implicitChildren("/")
	if len(rootChildren) != 1 || rootChildren[0] != "a" {
		t.Errorf("root children = %v, want [a]", rootChildren)
	}

	aChildren := r.implicitChildren("/a")
	// Should have "b" and "x.txt"
	if len(aChildren) != 2 {
		t.Errorf("a children = %v, want [b, x.txt]", aChildren)
	}
}

func TestGroupRoutes(t *testing.T) {
	r := New()
	api := r.Group("/api/v1")

	api.Read("/status.json", func(c *Context) ([]byte, error) {
		return []byte("ok"), nil
	})

	handler, _ := r.resolve(VerbRead, "/api/v1/status.json")
	if handler == nil {
		t.Fatal("expected handler for /api/v1/status.json")
	}
}

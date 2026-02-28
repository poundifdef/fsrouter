package fsrouter

// Context carries information about the filesystem operation being handled.
// It is the filesystem equivalent of http.Request — providing the resolved path,
// extracted parameters, and any metadata about the operation.
type Context struct {
	// Path is the full resolved path that matched the route pattern.
	Path string

	// params holds the extracted path parameters (e.g., {id} → "42").
	params map[string]string
}

// Param returns the value of a named path parameter.
//
//	router.Read("/users/{id}/profile.json", func(c *Context) ([]byte, error) {
//	    userID := c.Param("id") // "42" for path "/users/42/profile.json"
//	    ...
//	})
func (c *Context) Param(name string) string {
	if c.params == nil {
		return ""
	}
	return c.params[name]
}

// Params returns a copy of all path parameters.
func (c *Context) Params() map[string]string {
	out := make(map[string]string, len(c.params))
	for k, v := range c.params {
		out[k] = v
	}
	return out
}

func newContext(path string, params map[string]string) *Context {
	return &Context{
		Path:   path,
		params: params,
	}
}

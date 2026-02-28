package fsrouter

import (
	"path"
	"strings"
)

// pattern represents a parsed route pattern like "/users/{id}/config.json".
type pattern struct {
	raw      string           // original pattern string
	segments []patternSegment // parsed segments
	isDir    bool             // true if pattern ends with "/"
}

// patternSegment represents one segment of a path pattern.
// A segment is either a literal ("users"), a parameter ("{id}"),
// or a parameter with prefix/suffix ("file_{name}.json").
type patternSegment struct {
	// For literal segments: the exact string to match.
	literal string
	// For param segments: prefix before the param, the param name, and suffix after.
	prefix string
	param  string
	suffix string
	// isParam is true if this segment captures a parameter.
	isParam bool
	// isGlob captures the rest of the path (e.g., {path...}).
	isGlob bool
}

// parsePattern parses a route pattern string into a structured pattern.
//
// Supported syntax:
//   - Literal segments: /users/config.json
//   - Parameter segments: /users/{id} (captures the whole segment)
//   - Affixed parameters: /users/{id}.json (captures "foo" from "foo.json")
//   - Glob parameters: /files/{path...} (captures remaining path segments)
//   - Directory patterns: /users/ (trailing slash marks a directory)
func parsePattern(raw string) *pattern {
	raw = path.Clean("/" + raw)
	isDir := strings.HasSuffix(raw, "/") || raw == "/"

	// Re-add trailing slash for directory patterns after Clean removes it.
	p := &pattern{
		raw:   raw,
		isDir: isDir,
	}

	// Split into segments, skipping empty strings from leading slash.
	parts := strings.Split(strings.Trim(raw, "/"), "/")
	if len(parts) == 1 && parts[0] == "" {
		// Root pattern "/".
		return p
	}

	for _, part := range parts {
		seg := parseSegment(part)
		p.segments = append(p.segments, seg)
	}

	return p
}

// parseSegment parses a single path segment.
func parseSegment(s string) patternSegment {
	// Look for {param} or {param...} within the segment.
	openIdx := strings.Index(s, "{")
	closeIdx := strings.Index(s, "}")

	if openIdx < 0 || closeIdx < 0 || closeIdx <= openIdx {
		// No parameter — literal segment.
		return patternSegment{literal: s}
	}

	paramName := s[openIdx+1 : closeIdx]
	isGlob := strings.HasSuffix(paramName, "...")
	if isGlob {
		paramName = strings.TrimSuffix(paramName, "...")
	}

	prefix := s[:openIdx]
	suffix := s[closeIdx+1:]

	return patternSegment{
		prefix:  prefix,
		param:   paramName,
		suffix:  suffix,
		isParam: true,
		isGlob:  isGlob,
	}
}

// match attempts to match a concrete path against this pattern.
// Returns extracted parameters and whether the match succeeded.
func (p *pattern) match(filePath string) (params map[string]string, ok bool) {
	filePath = path.Clean("/" + filePath)
	params = make(map[string]string)

	// Root matches root.
	if len(p.segments) == 0 {
		if filePath == "/" {
			return params, true
		}
		return nil, false
	}

	parts := strings.Split(strings.Trim(filePath, "/"), "/")
	if len(parts) == 1 && parts[0] == "" {
		parts = nil
	}

	return p.matchSegments(parts, params)
}

func (p *pattern) matchSegments(parts []string, params map[string]string) (map[string]string, bool) {
	si := 0 // segment index

	for i, seg := range p.segments {
		if si >= len(parts) {
			return nil, false
		}

		if seg.isGlob {
			// Glob captures everything remaining.
			remaining := strings.Join(parts[si:], "/")
			params[seg.param] = remaining
			return params, true
		}

		if seg.isParam {
			part := parts[si]
			// Check prefix and suffix.
			if seg.prefix != "" && !strings.HasPrefix(part, seg.prefix) {
				return nil, false
			}
			if seg.suffix != "" && !strings.HasSuffix(part, seg.suffix) {
				return nil, false
			}
			// Extract the parameter value.
			value := part
			if seg.prefix != "" {
				value = strings.TrimPrefix(value, seg.prefix)
			}
			if seg.suffix != "" {
				value = strings.TrimSuffix(value, seg.suffix)
			}
			if value == "" {
				return nil, false
			}
			params[seg.param] = value
			si++
		} else {
			// Literal match.
			if parts[si] != seg.literal {
				return nil, false
			}
			si++
		}

		_ = i
	}

	// All segments consumed — remaining parts must also be consumed.
	if si != len(parts) {
		return nil, false
	}

	return params, true
}

// parentDirs returns all implicit parent directory paths for this pattern.
// For pattern "/a/b/{id}.json", it returns ["/", "/a", "/a/b"].
func (p *pattern) parentDirs() []string {
	var dirs []string
	current := "/"
	dirs = append(dirs, current)

	for _, seg := range p.segments {
		if seg.isParam || seg.isGlob {
			// Stop before dynamic segments — we can't enumerate further.
			break
		}
		current = path.Join(current, seg.literal)
		dirs = append(dirs, current)
	}

	// Remove the last entry if it matches the pattern itself (it's a file, not a parent).
	if len(dirs) > 1 && !p.isDir {
		dirs = dirs[:len(dirs)-1]
	}

	return dirs
}

// staticPrefix returns the longest static prefix of the pattern.
// For "/users/{id}/config.json" it returns "/users".
func (p *pattern) staticPrefix() string {
	parts := []string{"/"}
	for _, seg := range p.segments {
		if seg.isParam || seg.isGlob {
			break
		}
		parts = append(parts, seg.literal)
	}
	return path.Join(parts...)
}

// depth returns the number of segments.
func (p *pattern) depth() int {
	return len(p.segments)
}

// hasGlob returns true if this pattern contains a glob segment ({param...}).
func (p *pattern) hasGlob() bool {
	for _, seg := range p.segments {
		if seg.isGlob {
			return true
		}
	}
	return false
}

// Package fsrouter provides an HTTP-router-style API for building virtual filesystems
// served over NFS. Just as net/http lets you register handlers for URL paths with
// HTTP verbs (GET, POST, PUT, DELETE), fsrouter lets you register handlers for
// filesystem paths with filesystem verbs (Read, Write, Stat, List, Remove, Create).
//
// The resulting filesystem is served over NFSv3 using github.com/willscott/go-nfs
// and can be mounted by any standard NFS client.
package fsrouter

import (
	"os"
	"time"
)

// Verb represents a filesystem operation, analogous to HTTP methods.
type Verb int

const (
	// VerbRead is invoked when a file's contents are read (analogous to GET).
	VerbRead Verb = iota
	// VerbWrite is invoked when data is written to a file (analogous to PUT/POST).
	VerbWrite
	// VerbStat is invoked to retrieve file metadata (analogous to HEAD).
	VerbStat
	// VerbRemove is invoked when a file or directory is deleted (analogous to DELETE).
	VerbRemove
	// VerbList is invoked to enumerate a directory's contents (analogous to GET on a collection).
	VerbList
	// VerbCreate is invoked when a new file is created, before any data is written.
	VerbCreate
	// VerbMkdir is invoked when a new directory is created.
	VerbMkdir
	// VerbRename is invoked when a file or directory is moved/renamed (analogous to MOVE).
	VerbRename
)

// DirEntry describes a single entry returned by a List handler.
// It is the filesystem equivalent of an item in an HTTP collection response.
type DirEntry struct {
	// Name is the filename (base name only, no slashes).
	Name string
	// Size is the file size in bytes. For directories, use 0.
	Size int64
	// IsDir indicates whether this entry is a directory.
	IsDir bool
	// Mode is the Unix file permission bits. Defaults to 0644 for files, 0755 for dirs.
	Mode os.FileMode
	// ModTime is the last modification time. Defaults to time.Now() if zero.
	ModTime time.Time
}

// FileStat holds metadata about a file, returned by Stat handlers.
// This is the filesystem equivalent of HTTP response headers (Content-Length, Last-Modified, etc).
type FileStat struct {
	// Size of the file in bytes.
	Size int64
	// Mode is the Unix file permission bits. Defaults to 0644.
	Mode os.FileMode
	// ModTime is the last modification time. Defaults to time.Now() if zero.
	ModTime time.Time
	// IsDir overrides whether this path appears as a directory.
	IsDir bool
}

// fileInfo adapts FileStat to the os.FileInfo interface required by billy.
type fileInfo struct {
	name    string
	stat    FileStat
}

func (fi *fileInfo) Name() string        { return fi.name }
func (fi *fileInfo) Size() int64         { return fi.stat.Size }
func (fi *fileInfo) Mode() os.FileMode   { return fi.stat.Mode }
func (fi *fileInfo) ModTime() time.Time  { return fi.stat.ModTime }
func (fi *fileInfo) IsDir() bool         { return fi.stat.IsDir }
func (fi *fileInfo) Sys() interface{}    { return nil }

// dirEntryInfo adapts DirEntry to os.FileInfo.
type dirEntryInfo struct {
	entry DirEntry
}

func (di *dirEntryInfo) Name() string       { return di.entry.Name }
func (di *dirEntryInfo) Size() int64        { return di.entry.Size }
func (di *dirEntryInfo) IsDir() bool        { return di.entry.IsDir }
func (di *dirEntryInfo) Sys() interface{}   { return nil }

func (di *dirEntryInfo) Mode() os.FileMode {
	if di.entry.Mode != 0 {
		return di.entry.Mode
	}
	if di.entry.IsDir {
		return os.ModeDir | 0755
	}
	return 0644
}

func (di *dirEntryInfo) ModTime() time.Time {
	if di.entry.ModTime.IsZero() {
		return time.Now()
	}
	return di.entry.ModTime
}

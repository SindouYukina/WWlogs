package capture

import (
	_ "embed"
	"io/fs"
	"strings"
	"sync"
	"time"

	"modernc.org/sqlite/vfs"
)

// embeddedMasterDatabase is the tracked Master SQLite file. A string keeps it in read-only executable memory: a []byte
// would sit in a writable section, which Windows charges to the process as private memory even if it is never touched.
//
//go:embed master_data.db
var embeddedMasterDatabase string

const embeddedMasterDatabaseName = "master_data.db"

// embeddedMasterVFS registers the read-only SQLite file system over the embedded database once. SQLite reads pages from
// the executable into its bounded page cache, so the database is neither written to disk nor held a second time in
// memory. The registration is process-wide and lives as long as the process, as the executable's data does.
var embeddedMasterVFS = sync.OnceValues(func() (string, error) {
	name, _, err := vfs.New(embeddedMasterFS{})
	return name, err
})

type embeddedMasterFS struct{}

func (embeddedMasterFS) Open(name string) (fs.File, error) {
	if name != embeddedMasterDatabaseName {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return embeddedMasterFile{strings.NewReader(embeddedMasterDatabase)}, nil
}

// embeddedMasterFile is one read position over the shared bytes. The VFS seeks and reads it for every page.
type embeddedMasterFile struct{ *strings.Reader }

func (embeddedMasterFile) Close() error { return nil }

func (f embeddedMasterFile) Stat() (fs.FileInfo, error) { return embeddedMasterInfo{f.Size()}, nil }

type embeddedMasterInfo struct{ size int64 }

func (embeddedMasterInfo) Name() string       { return embeddedMasterDatabaseName }
func (i embeddedMasterInfo) Size() int64      { return i.size }
func (embeddedMasterInfo) Mode() fs.FileMode  { return 0o444 }
func (embeddedMasterInfo) ModTime() time.Time { return time.Time{} }
func (embeddedMasterInfo) IsDir() bool        { return false }
func (embeddedMasterInfo) Sys() any           { return nil }

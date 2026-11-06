package assets

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"
	"sync"
	"time"
)

// A pack is a ZIP of stored (uncompressed, since WebP is already compressed) images, one per distinct content, plus an
// index from manifest path to content. Thousands of skill and effect IDs share a few hundred icons, so this keeps the
// executable small while every ID still resolves through the same Resolver paths as a directory.
const (
	packIndexName  = "index"
	packBlobPrefix = "blobs/"
)

// PackStats summarizes a written pack for the build log.
type PackStats struct {
	Files        int
	FileBytes    int64
	Distinct     int
	DistinctSize int64
}

// WritePack reads every manifest entry from source and writes the pack. A missing file, a changed file or a file that is
// not WebP fails, so an executable can never carry images that differ from the reviewed manifest. Identical input gives
// identical output.
func WritePack(output io.Writer, source fs.FS, manifest Manifest) (PackStats, error) {
	contents := make([][]byte, len(manifest.Entries))
	if err := forEachBounded(len(manifest.Entries), func(index int) error {
		entry := manifest.Entries[index]
		data, err := fs.ReadFile(source, entry.Path)
		if err != nil {
			return fmt.Errorf("read manifest image: %w", err)
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != entry.SHA256 || int64(len(data)) != entry.Size {
			return fmt.Errorf("%s does not match assets/manifest.json", entry.Path)
		}
		if !isWebP(data) {
			return fmt.Errorf("%s is not a WebP image", entry.Path)
		}
		contents[index] = data
		return nil
	}); err != nil {
		return PackStats{}, err
	}

	stats := PackStats{Files: len(manifest.Entries)}
	distinct := make(map[string][]byte)
	var index strings.Builder
	for position, entry := range manifest.Entries {
		stats.FileBytes += entry.Size
		distinct[entry.SHA256] = contents[position]
		fmt.Fprintf(&index, "%s\t%s\n", entry.Path, entry.SHA256)
	}
	hashes := make([]string, 0, len(distinct))
	for hash := range distinct {
		hashes = append(hashes, hash)
	}
	slices.Sort(hashes)

	writer := zip.NewWriter(output)
	modified := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, hash := range hashes {
		entry, err := writer.CreateHeader(&zip.FileHeader{Name: packBlobPrefix + hash, Method: zip.Store, Modified: modified})
		if err != nil {
			return PackStats{}, err
		}
		if _, err := entry.Write(distinct[hash]); err != nil {
			return PackStats{}, err
		}
		stats.Distinct++
		stats.DistinctSize += int64(len(distinct[hash]))
	}
	entry, err := writer.CreateHeader(&zip.FileHeader{Name: packIndexName, Method: zip.Deflate, Modified: modified})
	if err != nil {
		return PackStats{}, err
	}
	if _, err := io.WriteString(entry, index.String()); err != nil {
		return PackStats{}, err
	}
	if err := writer.Close(); err != nil {
		return PackStats{}, fmt.Errorf("finish image pack: %w", err)
	}
	return stats, nil
}

// PackFS is a read-only filesystem over a pack held in the executable. Nothing is parsed until the first image is
// requested, so an application that shows no image pays nothing, and the image bytes are never copied to the heap
// beyond what a request reads.
type PackFS struct {
	pack   string
	once   sync.Once
	reader *zip.Reader
	index  map[string]string
	err    error
}

// NewPackFS wraps a pack. A damaged pack is reported by Open as an error that does not match fs.ErrNotExist, which keeps
// "this ID has no image" and "the image could not be loaded" apart.
func NewPackFS(pack string) *PackFS {
	return &PackFS{pack: pack}
}

func readPack(pack string) (*zip.Reader, map[string]string, error) {
	reader, err := zip.NewReader(strings.NewReader(pack), int64(len(pack)))
	if err != nil {
		return nil, nil, fmt.Errorf("open image pack: %w", err)
	}
	indexFile, err := reader.Open(packIndexName)
	if err != nil {
		return nil, nil, fmt.Errorf("open image pack index: %w", err)
	}
	defer indexFile.Close()
	index := make(map[string]string)
	scanner := bufio.NewScanner(indexFile)
	for scanner.Scan() {
		path, hash, found := bytes.Cut(scanner.Bytes(), []byte{'\t'})
		if !found {
			return nil, nil, errors.New("image pack index is malformed")
		}
		index[string(path)] = string(hash)
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("read image pack index: %w", err)
	}
	return reader, index, nil
}

// Open implements fs.FS.
func (p *PackFS) Open(name string) (fs.File, error) {
	p.once.Do(func() { p.reader, p.index, p.err = readPack(p.pack) })
	if p.err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: p.err}
	}
	hash, found := p.index[name]
	if !found {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	file, err := p.reader.Open(packBlobPrefix + hash)
	if err != nil {
		// The index promised this content, so its absence is damage, not a missing image.
		return nil, &fs.PathError{Op: "open", Path: name, Err: fmt.Errorf("image pack content is unavailable: %v", err)}
	}
	return file, nil
}

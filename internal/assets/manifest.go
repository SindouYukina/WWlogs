package assets

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"sync"
)

// ManifestSchema is the only assets/manifest.json layout this package reads.
const ManifestSchema = 1

// Manifest lists every game image that belongs to the product. It is the only input of the Public image pack, so a
// file that is not listed cannot reach an executable, and a listed file that changed fails the build.
type Manifest struct {
	Schema int `json:"schema"`
	// Client and Resource identify the game version the images were extracted from.
	Client   string          `json:"client"`
	Resource string          `json:"resource"`
	Entries  []ManifestEntry `json:"entries"`
}

// ManifestEntry describes one image by its path under assets/.
type ManifestEntry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// A manifest path must be reachable through a Key; anything else is not a product image.
var manifestPath = regexp.MustCompile(`^game/(characters/[1-9][0-9]*/(avatar|portrait)|skills/[1-9][0-9]*/icon|skills/by-config/[1-9][0-9]*/icon|buffs/[1-9][0-9]*/icon)\.webp$`)

// ParseManifest decodes and validates a manifest. Line endings do not matter, so a CRLF checkout reads the same.
func ParseManifest(data []byte) (Manifest, error) {
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode asset manifest: %w", err)
	}
	if manifest.Schema != ManifestSchema {
		return Manifest{}, fmt.Errorf("asset manifest schema %d is not supported", manifest.Schema)
	}
	if len(manifest.Entries) == 0 {
		return Manifest{}, errors.New("asset manifest has no entries")
	}
	previous := ""
	for _, entry := range manifest.Entries {
		if !manifestPath.MatchString(entry.Path) {
			return Manifest{}, fmt.Errorf("asset manifest path %q is not a product image", entry.Path)
		}
		// Sorted order makes the file reviewable and rules out duplicate paths.
		if entry.Path <= previous {
			return Manifest{}, fmt.Errorf("asset manifest is not sorted at %q", entry.Path)
		}
		previous = entry.Path
		if digest, err := hex.DecodeString(entry.SHA256); err != nil || len(digest) != sha256.Size {
			return Manifest{}, fmt.Errorf("asset manifest entry %q has an invalid SHA-256", entry.Path)
		}
		if entry.Size <= 0 {
			return Manifest{}, fmt.Errorf("asset manifest entry %q has no size", entry.Path)
		}
	}
	return manifest, nil
}

// Encode writes the manifest with one entry per line, so a game update shows up as a readable diff.
func (m Manifest) Encode() []byte {
	var out bytes.Buffer
	header, _ := json.Marshal(m.Client)
	resource, _ := json.Marshal(m.Resource)
	fmt.Fprintf(&out, "{\n  \"schema\": %d,\n  \"client\": %s,\n  \"resource\": %s,\n  \"entries\": [\n", m.Schema, header, resource)
	for index, entry := range m.Entries {
		line, _ := json.Marshal(entry)
		out.WriteString("    ")
		out.Write(line)
		if index < len(m.Entries)-1 {
			out.WriteByte(',')
		}
		out.WriteByte('\n')
	}
	out.WriteString("  ]\n}\n")
	return out.Bytes()
}

// ScanManifest builds the entries for the images present in source. Files that no Key can reach are returned
// separately, so the maintainer can see what stays out of the product.
func ScanManifest(source fs.FS, client, resource string) (Manifest, []string, error) {
	manifest := Manifest{Schema: ManifestSchema, Client: client, Resource: resource}
	var paths, skipped []string
	err := fs.WalkDir(source, "game", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if manifestPath.MatchString(path) {
			paths = append(paths, path)
		} else {
			skipped = append(skipped, path)
		}
		return nil
	})
	if err != nil {
		return Manifest{}, nil, fmt.Errorf("scan game assets: %w", err)
	}
	slices.Sort(paths)
	manifest.Entries = make([]ManifestEntry, len(paths))
	if err := forEachBounded(len(paths), func(index int) error {
		data, err := fs.ReadFile(source, paths[index])
		if err != nil {
			return err
		}
		if !isWebP(data) {
			return fmt.Errorf("%s is not a WebP image", paths[index])
		}
		digest := sha256.Sum256(data)
		manifest.Entries[index] = ManifestEntry{Path: paths[index], SHA256: hex.EncodeToString(digest[:]), Size: int64(len(data))}
		return nil
	}); err != nil {
		return Manifest{}, nil, err
	}
	return manifest, skipped, nil
}

func isWebP(data []byte) bool {
	return len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP"
}

// assetReadWorkers bounds concurrent file reads. The asset tree may be on a network share, where sequential opens of
// several thousand small files take minutes.
const assetReadWorkers = 16

func forEachBounded(count int, work func(index int) error) error {
	var (
		group sync.WaitGroup
		mutex sync.Mutex
		first error
	)
	next := make(chan int)
	for range assetReadWorkers {
		group.Add(1)
		go func() {
			defer group.Done()
			for index := range next {
				if err := work(index); err != nil {
					mutex.Lock()
					if first == nil {
						first = err
					}
					mutex.Unlock()
				}
			}
		}()
	}
	for index := range count {
		next <- index
	}
	close(next)
	group.Wait()
	return first
}

package readiness

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"sync"
	"time"
)

// Results that depend only on file bytes are remembered within one process, so repeated gate runs (the tests run the gate
// on many isolated inputs) do not reload the same Master or rehash the same images. A changed file has a different key.

var (
	// loaderResults maps Master SHA-256 + "/" + locale to the loader's error text ("" for success).
	loaderResults sync.Map
	// integrityResults maps Master SHA-256 to its integrity_check result and foreign_key_check count.
	integrityResults sync.Map
	// imageHashes maps an image path, size and modification time to its SHA-256.
	imageHashes sync.Map
)

type integrityResult struct {
	check       string
	foreignKeys int
	err         error
}

type imageKey struct {
	path    string
	size    int64
	modTime time.Time
}

// imageSHA256 hashes one image file, remembering the answer for an unchanged file.
func imageSHA256(path string) (string, int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, err
	}
	key := imageKey{path, info.Size(), info.ModTime()}
	if known, ok := imageHashes.Load(key); ok {
		return known.(string), info.Size(), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", 0, err
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	imageHashes.Store(key, digest)
	return digest, int64(len(data)), nil
}

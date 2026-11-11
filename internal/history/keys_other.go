//go:build !windows

package history

import (
	"errors"
	"math"
	"os"
)

// WWlogs runs on Windows. Elsewhere no key provider exists, so a history file can be neither created nor read; tests that
// need one pass their own provider.
type unavailableProvider struct{}

func defaultKeyProvider() KeyProvider { return unavailableProvider{} }

func (unavailableProvider) ID() string { return "unavailable" }

func (unavailableProvider) Seal([]byte) ([]byte, error) {
	return nil, errors.New("no history key provider on this platform")
}

func (unavailableProvider) Unseal([]byte) ([]byte, error) {
	return nil, errors.New("no history key provider on this platform")
}

func replaceFile(from, to string) error { return os.Rename(from, to) }

func freeSpace(string) (uint64, error) { return math.MaxUint64, nil }

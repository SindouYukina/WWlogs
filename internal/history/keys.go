package history

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The history key.
//
// The database key is 64 random bytes from crypto/rand: the two AES-256 keys of AES-256-XTS. It is never written in the
// clear and never derived from anything the build or the repository contains. A KeyProvider seals it; only the sealed form
// is stored, in a key file next to the history file. The key file is written once, atomically, and never replaced: a key
// that cannot be unsealed leaves the history unreadable rather than overwritten.

// KeyProvider seals and unseals the history key. The Windows provider uses DPAPI for the current user (keys_windows.go).
// A provider backed by other hardware, such as a TPM, implements the same interface; the database format does not change.
type KeyProvider interface {
	// ID names the provider in the key file; a key file sealed by another provider is refused.
	ID() string
	Seal(key []byte) ([]byte, error)
	Unseal(sealed []byte) ([]byte, error)
}

// historyKeySize is the AES-256-XTS key length.
const historyKeySize = 64

// keyFileMagic starts a history key file, version 1: magic, provider ID length (1 byte), provider ID, sealed key length
// (4 bytes, little endian), sealed key.
const keyFileMagic = "WWLHKEY1"

// maxSealedKey bounds the sealed key a key file may declare.
const maxSealedKey = 1 << 16

var (
	errKeyMissing     = errors.New("the history key file is missing for an existing history file")
	errKeyUnavailable = errors.New("the history key cannot be unsealed")
)

// keyPath is the key file of a history file: combat-history.db keeps its key in combat-history.key.
func keyPath(historyPath string) string {
	return strings.TrimSuffix(historyPath, filepath.Ext(historyPath)) + ".key"
}

// loadKey unseals the key of path. create allows a new key when no key file exists; it is false whenever an encrypted file
// already exists, so a lost key file never gets a new key that would hide the old one.
func loadKey(path string, provider KeyProvider, create bool) ([]byte, error) {
	file := keyPath(path)
	data, err := os.ReadFile(file)
	switch {
	case errors.Is(err, os.ErrNotExist) && create:
		return createKey(file, provider)
	case errors.Is(err, os.ErrNotExist):
		return nil, errKeyMissing
	case err != nil:
		return nil, fmt.Errorf("%w: %v", errKeyUnavailable, err)
	}
	id, sealed, err := parseKeyFile(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errKeyUnavailable, err)
	}
	if id != provider.ID() {
		return nil, fmt.Errorf("%w: sealed by %q, not %q", errKeyUnavailable, id, provider.ID())
	}
	key, err := provider.Unseal(sealed)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errKeyUnavailable, err)
	}
	if len(key) != historyKeySize {
		clear(key)
		return nil, fmt.Errorf("%w: the unsealed key has %d bytes", errKeyUnavailable, len(key))
	}
	return key, nil
}

func createKey(file string, provider KeyProvider) ([]byte, error) {
	key := make([]byte, historyKeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("create history key: %w", err)
	}
	sealed, err := provider.Seal(key)
	if err != nil {
		clear(key)
		return nil, fmt.Errorf("%w: %v", errKeyUnavailable, err)
	}
	if err := writeKeyFile(file, provider.ID(), sealed); err != nil {
		clear(key)
		return nil, err
	}
	return key, nil
}

func parseKeyFile(data []byte) (string, []byte, error) {
	if !bytes.HasPrefix(data, []byte(keyFileMagic)) || len(data) < len(keyFileMagic)+1 {
		return "", nil, errors.New("not a history key file")
	}
	rest := data[len(keyFileMagic):]
	idLength := int(rest[0])
	rest = rest[1:]
	if len(rest) < idLength+4 {
		return "", nil, errors.New("truncated history key file")
	}
	id := string(rest[:idLength])
	rest = rest[idLength:]
	sealedLength := binary.LittleEndian.Uint32(rest[:4])
	rest = rest[4:]
	if sealedLength == 0 || sealedLength > maxSealedKey || uint32(len(rest)) != sealedLength {
		return "", nil, errors.New("malformed history key file")
	}
	return id, rest, nil
}

// writeKeyFile writes the key file atomically and refuses to replace an existing one.
func writeKeyFile(file, id string, sealed []byte) error {
	if len(id) > 255 || len(sealed) == 0 || len(sealed) > maxSealedKey {
		return errors.New("invalid sealed history key")
	}
	var data bytes.Buffer
	data.WriteString(keyFileMagic)
	data.WriteByte(byte(len(id)))
	data.WriteString(id)
	var length [4]byte
	binary.LittleEndian.PutUint32(length[:], uint32(len(sealed)))
	data.Write(length[:])
	data.Write(sealed)
	temp := file + ".tmp"
	_ = os.Remove(temp)
	out, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("write history key: %w", err)
	}
	if _, err := out.Write(data.Bytes()); err != nil {
		out.Close()
		os.Remove(temp)
		return fmt.Errorf("write history key: %w", err)
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(temp)
		return fmt.Errorf("write history key: %w", err)
	}
	if err := out.Close(); err != nil {
		os.Remove(temp)
		return fmt.Errorf("write history key: %w", err)
	}
	if _, err := os.Stat(file); err == nil {
		os.Remove(temp)
		return errors.New("a history key file already exists")
	}
	if err := os.Rename(temp, file); err != nil {
		os.Remove(temp)
		return fmt.Errorf("write history key: %w", err)
	}
	return nil
}

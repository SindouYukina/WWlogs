package history

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Synthetic key inputs and v0.7.0 fixtures for every test of this package.
//
// No test here derives a key from this computer's motherboard or from the game protocol key a build embeds: init replaces
// the platform inputs with a synthetic motherboard (testMachine) and a synthetic RSA key (syntheticProtocolKeyA). Both are
// the inputs of the fixed derivation vectors (keyderive_test.go), so a history a test creates has exactly the vector key.
// The synthetic RSA keys were generated for the derivation research (work/hwid-kdf-poc, cmd/genrsa) and have no value. A
// test that needs another machine or key passes its own KeyInputs.

func init() {
	platformKeyInputs = testKeyInputs(testMachine(), syntheticProtocolKeyA)
}

func mustHex(text string) []byte {
	value, err := hex.DecodeString(text)
	if err != nil {
		panic(err)
	}
	return value
}

// testMachine is the synthetic motherboard of the vectors: UUID bytes 47 4C 57 57 45 54 54 53 80 00 ... 01 and the serial
// WWLOGSTEST0001.
func testMachine() Machine {
	return Machine{SystemUUID: mustHex("474c5757455454538000000000000001"), BaseboardSerial: []byte("WWLOGSTEST0001")}
}

// otherMachine is a second synthetic motherboard: another UUID and another serial.
func otherMachine() Machine {
	return Machine{SystemUUID: mustHex("474c57574f544845528000000000000a"), BaseboardSerial: []byte("WWLOGSOTHER002")}
}

// testKeyInputs supplies machine and protocolKey. The store wipes what the inputs return, so every call returns copies.
func testKeyInputs(machine Machine, protocolKey []byte) KeyInputs {
	return KeyInputs{
		Machine: func() (Machine, error) {
			return Machine{SystemUUID: bytes.Clone(machine.SystemUUID), BaseboardSerial: bytes.Clone(machine.BaseboardSerial)}, nil
		},
		ProtocolKey: func() ([]byte, error) { return bytes.Clone(protocolKey), nil },
	}
}

// openWith opens the history at path with inputs.
func openWith(path string, inputs KeyInputs) (*Store, error) {
	return Open(Options{Path: path, Location: time.UTC, Build: &testBuild, MasterSHA256: "test-master", KeyInputs: inputs})
}

// writeLegacyKeyFile writes a key file as WWlogs v0.7.0 did: magic, provider ID length (1 byte), provider ID, sealed key
// length (4 bytes, little endian), sealed key.
func writeLegacyKeyFile(t testing.TB, file, id string, sealed []byte) {
	t.Helper()
	var data bytes.Buffer
	data.WriteString(keyFileMagic)
	data.WriteByte(byte(len(id)))
	data.WriteString(id)
	var length [4]byte
	binary.LittleEndian.PutUint32(length[:], uint32(len(sealed)))
	data.Write(length[:])
	data.Write(sealed)
	if err := os.WriteFile(file, data.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// legacyStore creates a history at path as WWlogs v0.7.0 did: 64 random bytes as its key, sealed by DPAPI for the current
// user in combat-history.key, and the encrypted database of the current layout in write-ahead log mode. It returns a store
// open on it for recording, as v0.7.0 opened it: no record of a derived key exists beside it.
func legacyStore(t testing.TB, path string) *Store {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, historyKeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	provider := defaultKeyProvider()
	sealed, err := provider.Seal(key)
	if err != nil {
		t.Fatal(err)
	}
	writeLegacyKeyFile(t, keyPath(path), provider.ID(), sealed)
	lock, err := acquireWriterLock(path + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	db, err := openEncrypted(path, key, false)
	if err != nil {
		t.Fatal(err)
	}
	store := &Store{path: path, db: db, lock: lock, location: time.UTC, now: time.Now, build: testBuild, master: "test-master",
		inputs: platformKeyInputs, provider: provider, key: key, protection: provider.ID()}
	ctx := context.Background()
	if err := store.create(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}
	store.status = Status{State: StateReady, SchemaVersion: schemaVersion, LatestSchemaVersion: schemaVersion, Encrypted: true,
		Encryption: encryptionName, KeyProtection: provider.ID()}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// recordedLegacyStore is a v0.7.0 history with one finished encounter and one effect change, as recordedStore is for the
// current format.
func recordedLegacyStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := testPath(t)
	store := legacyStore(t, path)
	recordOneEncounter(t, store)
	return store, path
}

package history

import (
	"encoding/pem"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// What opening a history pays for its key, step by step (go test -bench '^BenchmarkHistoryKey' -run '^$'): reading this
// computer's SMBIOS table, the canonical form of the protocol key, the derivation itself, the whole key of a new history from
// its inputs, and the preflight of an existing file. The protocol key and the motherboard of the derivation steps are the
// synthetic ones; the firmware step reads the real table and keeps nothing.

func BenchmarkHistoryKeyFirmwareTable(b *testing.B) {
	if runtime.GOOS != "windows" {
		b.Skip("the firmware table is read on Windows")
	}
	for i := 0; i < b.N; i++ {
		raw, err := readFirmwareTable()
		if err != nil {
			b.Fatal(err)
		}
		machine, err := machineFromSMBIOS(raw)
		if err != nil {
			b.Fatal(err)
		}
		machine.wipe()
		clear(raw)
	}
}

func BenchmarkHistoryKeyCanonicalProtocolKey(b *testing.B) {
	pem := pemOf(syntheticProtocolKeyA)
	for i := 0; i < b.N; i++ {
		canonical, err := canonicalProtocolKey(pem)
		if err != nil {
			b.Fatal(err)
		}
		clear(canonical)
	}
}

func BenchmarkHistoryKeyDerivation(b *testing.B) {
	identifier := mustHex(kdfVectors[0].identifier)
	for i := 0; i < b.N; i++ {
		key, _, err := deriveHistoryKey(sourceSystemUUID, identifier, syntheticProtocolKeyA)
		if err != nil {
			b.Fatal(err)
		}
		clear(key)
	}
}

func BenchmarkHistoryKeyOfANewHistory(b *testing.B) {
	inputs := testKeyInputs(testMachine(), pemOf(syntheticProtocolKeyA))
	for i := 0; i < b.N; i++ {
		key, _, err := newHistoryKey(inputs)
		if err != nil {
			b.Fatal(err)
		}
		clear(key.key)
	}
}

func BenchmarkHistoryKeyPreflight(b *testing.B) {
	path := filepath.Join(b.TempDir(), "history", "combat-history.db")
	store, err := Open(Options{Path: path, Location: time.UTC, Build: &testBuild, MasterSHA256: "test-master"})
	if err != nil {
		b.Fatal(err)
	}
	key := append([]byte(nil), store.key...)
	// The store stays open, so the write-ahead log is there and the preflight checks it too.
	defer store.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if outcome, err := preflight(path, key, true); err != nil || outcome != preflightReady {
			b.Fatalf("preflight = %s (%v)", outcome, err)
		}
	}
}

// pemOf is a canonical key as the PKCS#1 PEM block a build embeds.
func pemOf(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})
}

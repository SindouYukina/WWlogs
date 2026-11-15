package history

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wwlogs/internal/buildinfo"
	"wwlogs/internal/capture"
)

// Histories whose key derives from the machine and the protocol key (keyderive.go, keysource.go, unlock.go). Every machine
// and protocol key here is synthetic (key_inputs_test.go); the default inputs of this package are those of the first vector.

func readRecord(t *testing.T, path string) keyRecord {
	t.Helper()
	record, state, err := readKeyRecord(path)
	if err != nil || state != recordValid {
		t.Fatalf("history record: state %d (%v)", state, err)
	}
	return record
}

// TestNewHistoryIsKeyedByTheMachineAndTheProtocolKey creates a history with the inputs of the first vector: its key is the
// vector key, its record names the source, the protocol key id and the check value, and no DPAPI key file exists.
func TestNewHistoryIsKeyedByTheMachineAndTheProtocolKey(t *testing.T) {
	path := testPath(t)
	store := openTestStore(t, path, nil)
	vector := kdfVectors[0]
	if hex.EncodeToString(store.key) != vector.key {
		t.Fatal("the history key is not the derived key of its inputs")
	}
	if status := store.Status(); status.State != StateReady || status.KeyProtection != keyProtectionDerived {
		t.Fatalf("status = %+v", status)
	}
	if record := readRecord(t, path); record != (keyRecord{Format: keyRecordFormat, KDFVersion: 2, Source: sourceSystemUUID, RSAKeyID: vector.keyID, KeyCheck: vector.check}) {
		t.Fatalf("record = %+v", record)
	}
	if _, err := os.Stat(keyPath(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a new history got a DPAPI key file (%v)", err)
	}
	recordOneEncounter(t, store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	// The protocol key in another textual form is the same key.
	parsed, err := x509.ParsePKCS1PrivateKey(syntheticProtocolKeyA)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(parsed)
	if err != nil {
		t.Fatal(err)
	}
	asPKCS8 := []byte(strings.ReplaceAll(string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})), "\n", "\r\n"))
	reopened, err := openWith(path, testKeyInputs(testMachine(), asPKCS8))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if year, err := reopened.Year(context.Background(), 2026, time.UTC); err != nil || year.Total != 1 {
		t.Fatalf("reopened year = %+v (%v)", year, err)
	}
}

// TestAnotherMachineOrProtocolKeyNeverOpensTheHistory: a history with 40 encounters only in its write-ahead log is opened on
// another motherboard and with another protocol key. Both are refused before SQLite sees a key and change no file; its own
// machine and key then read all 140.
func TestAnotherMachineOrProtocolKeyNeverOpensTheHistory(t *testing.T) {
	const checkpointed, inLog = 100, 40
	path := logFixtureOf(t, openTestStore(t, testPath(t), nil), checkpointed, inLog)
	for name, c := range map[string]struct {
		inputs KeyInputs
		detail string
	}{
		"another motherboard":  {testKeyInputs(otherMachine(), syntheticProtocolKeyA), ""},
		"another protocol key": {testKeyInputs(testMachine(), syntheticProtocolKeyB), "another protocol key"},
	} {
		before := historyDigests(t, filepath.Dir(path))
		store, err := openWith(path, c.inputs)
		if err == nil || store.Status().State != StateUnavailable || store.Status().Reason != ReasonKeyVerification {
			t.Fatalf("%s: %+v (%v)", name, store.Status(), err)
		}
		if !strings.Contains(err.Error(), c.detail) {
			t.Fatalf("%s: %v", name, err)
		}
		store.Close()
		requireSameFiles(t, before, historyDigests(t, filepath.Dir(path)))
	}
	if got := encountersIn(t, path); got != checkpointed+inLog {
		t.Fatalf("%d of %d encounters after the refusals", got, checkpointed+inLog)
	}
}

// TestSerialHistoryKeepsItsSource: a history created on a motherboard without a usable UUID takes the serial number and is
// opened with it for good, also once the firmware reports a usable UUID; another serial does not open it.
func TestSerialHistoryKeepsItsSource(t *testing.T) {
	path := testPath(t)
	noUUID := Machine{SystemUUID: make([]byte, 16), BaseboardSerial: []byte("WWLOGSTEST0001")}
	store, err := openWith(path, testKeyInputs(noUUID, syntheticProtocolKeyA))
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(store.key) != kdfVectors[2].key || readRecord(t, path).Source != sourceBaseboardSerial {
		t.Fatalf("serial history: record %+v", readRecord(t, path))
	}
	recordOneEncounter(t, store)
	store.Close()
	before := readFile(t, keyRecordPath(path))
	withUUID, err := openWith(path, testKeyInputs(testMachine(), syntheticProtocolKeyA))
	if err != nil {
		t.Fatalf("the serial history did not open once the UUID became usable: %v", err)
	}
	withUUID.Close()
	if !bytes.Equal(readFile(t, keyRecordPath(path)), before) {
		t.Fatal("the record changed its source")
	}
	otherSerial := Machine{SystemUUID: testUUID, BaseboardSerial: []byte("WWLOGSOTHER002")}
	refused, err := openWith(path, testKeyInputs(otherSerial, syntheticProtocolKeyA))
	if err == nil || refused.Status().Reason != ReasonKeyVerification {
		t.Fatalf("another serial: %+v (%v)", refused.Status(), err)
	}
	refused.Close()
}

// TestNoKeyInputsCreatesNothing: without a usable motherboard identifier or without the protocol key, no history is created,
// never with a default key, and an existing history is left as it was.
func TestNoKeyInputsCreatesNothing(t *testing.T) {
	failing := errors.New("synthetic failure")
	for name, c := range map[string]struct {
		inputs KeyInputs
		reason string
	}{
		"placeholder identifiers": {testKeyInputs(Machine{SystemUUID: make([]byte, 16), BaseboardSerial: []byte("To Be Filled By O.E.M.")}, syntheticProtocolKeyA), ReasonHardwareIdentity},
		"unreadable firmware": {KeyInputs{Machine: func() (Machine, error) { return Machine{}, failing },
			ProtocolKey: func() ([]byte, error) { return bytes.Clone(syntheticProtocolKeyA), nil }}, ReasonHardwareIdentity},
		"no protocol key": {KeyInputs{Machine: testKeyInputs(testMachine(), nil).Machine,
			ProtocolKey: func() ([]byte, error) { return nil, failing }}, ReasonKeyMaterial},
		"a protocol key that is no key": {testKeyInputs(testMachine(), []byte("not a key")), ReasonKeyMaterial},
	} {
		t.Run(name, func(t *testing.T) {
			path := testPath(t)
			store, err := openWith(path, c.inputs)
			if err == nil || store.Status().State != StateUnavailable || store.Status().Reason != c.reason {
				t.Fatalf("new history: %+v (%v)", store.Status(), err)
			}
			store.Close()
			if names := historyDigests(t, filepath.Dir(path)); len(names) != 0 {
				t.Fatalf("files were created: %v", fileNames(names))
			}
			existing, existingPath := recordedStore(t)
			existing.Close()
			before := historyDigests(t, filepath.Dir(existingPath))
			store, err = openWith(existingPath, c.inputs)
			if c.reason == ReasonHardwareIdentity && name == "placeholder identifiers" {
				// The recorded source is the UUID; a placeholder now in its place is a different identifier.
				c.reason = ReasonKeyVerification
			}
			if err == nil || store.Status().Reason != c.reason {
				t.Fatalf("existing history: %+v (%v)", store.Status(), err)
			}
			store.Close()
			requireSameFiles(t, before, historyDigests(t, filepath.Dir(existingPath)))
		})
	}
}

// TestLostOrDamagedRecordIsRecovered: a derived history whose record is lost or damaged still opens, with the key that
// decrypts it, and gets the same record back.
func TestLostOrDamagedRecordIsRecovered(t *testing.T) {
	recorded, path := recordedStore(t)
	if err := recorded.Close(); err != nil {
		t.Fatal(err)
	}
	original := readFile(t, keyRecordPath(path))
	for name, damage := range map[string]func(){
		"lost":    func() { os.Remove(keyRecordPath(path)) },
		"damaged": func() { writeFile(t, keyRecordPath(path), []byte("{\"format\": \"wwlogs-hist")) },
		"empty":   func() { writeFile(t, keyRecordPath(path), nil) },
	} {
		damage()
		store, err := openWith(path, KeyInputs{})
		if err != nil {
			t.Fatalf("%s record: %v", name, err)
		}
		year, err := store.Year(context.Background(), 2026, time.UTC)
		store.Close()
		if err != nil || year.Total != 1 {
			t.Fatalf("%s record: %+v (%v)", name, year, err)
		}
		if !bytes.Equal(readFile(t, keyRecordPath(path)), original) {
			t.Fatalf("%s record was not written back as it was", name)
		}
	}
	// A record whose check value is not the key's is authoritative: the history is refused, not reinterpreted.
	var record keyRecord
	if err := json.Unmarshal(original, &record); err != nil {
		t.Fatal(err)
	}
	record.KeyCheck = strings.Repeat("00", historyKeyCheck)
	data, _ := json.Marshal(record)
	writeFile(t, keyRecordPath(path), data)
	before := historyDigests(t, filepath.Dir(path))
	store, err := openWith(path, KeyInputs{})
	if err == nil || store.Status().Reason != ReasonKeyVerification {
		t.Fatalf("a record of another key: %+v (%v)", store.Status(), err)
	}
	store.Close()
	requireSameFiles(t, before, historyDigests(t, filepath.Dir(path)))
}

// TestUnknownRecordIsNeverRewritten: a record of a later derivation version or of another format means a history this build
// does not know: it is neither opened nor rewritten, and no history is created beside it.
func TestUnknownRecordIsNeverRewritten(t *testing.T) {
	for name, record := range map[string]string{
		"a later derivation version": `{"format":"wwlogs-history-hwid-kdf","kdfVersion":3,"source":"smbios-type1-uuid","rsaKeyId":"69a1a3e72059b8b4","keyCheck":"2e85806d08c4fb62396f087fc5101306"}`,
		"another format":             `{"format":"wwlogs-history-tpm","kdfVersion":2}`,
	} {
		for _, existing := range []bool{true, false} {
			path := testPath(t)
			if existing {
				var recorded *Store
				recorded, path = recordedStore(t)
				if err := recorded.Close(); err != nil {
					t.Fatal(err)
				}
			} else if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			writeFile(t, keyRecordPath(path), []byte(record))
			before := historyDigests(t, filepath.Dir(path))
			store, err := openWith(path, KeyInputs{})
			if err == nil || store.Status().Reason != ReasonFormatUnsupported {
				t.Fatalf("%s (history exists %t): %+v (%v)", name, existing, store.Status(), err)
			}
			store.Close()
			requireSameFiles(t, before, historyDigests(t, filepath.Dir(path)))
		}
	}
}

// TestUUIDByteOrderChangeIsTolerated: firmware that reports the same UUID with its first three fields in the other byte order
// still opens the history; any other UUID does not.
func TestUUIDByteOrderChangeIsTolerated(t *testing.T) {
	recorded, path := recordedStore(t)
	if err := recorded.Close(); err != nil {
		t.Fatal(err)
	}
	swapped := Machine{SystemUUID: uuidByteOrderSwapped(testUUID), BaseboardSerial: []byte("WWLOGSTEST0001")}
	store, err := openWith(path, testKeyInputs(swapped, syntheticProtocolKeyA))
	if err != nil {
		t.Fatalf("the swapped UUID: %v", err)
	}
	store.Close()
	changed := bytes.Clone(testUUID)
	changed[12] ^= 0x10
	store, err = openWith(path, testKeyInputs(Machine{SystemUUID: changed}, syntheticProtocolKeyA))
	if err == nil || store.Status().Reason != ReasonKeyVerification {
		t.Fatalf("another UUID: %+v (%v)", store.Status(), err)
	}
	store.Close()
}

// TestLegacyHistoryBesideAStaleRecordOpensWithItsOwnKey: a v0.7.0 history next to the record of another history (for example
// restored from a backup over a newer folder) is opened with its own DPAPI key.
func TestLegacyHistoryBesideAStaleRecordOpensWithItsOwnKey(t *testing.T) {
	legacy, path := recordedLegacyStore(t)
	legacy.Close()
	derived, derivedPath := recordedStore(t)
	if err := derived.Close(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, keyRecordPath(path), readFile(t, keyRecordPath(derivedPath)))
	store := openTestStore(t, path, nil)
	if year, err := store.Year(context.Background(), 2026, time.UTC); err != nil || year.Total != 1 {
		t.Fatalf("year = %+v (%v)", year, err)
	}
}

// TestDerivationIgnoresBuildEnvironmentAndProcess derives the key of the same inputs under other build identities, zones and
// clocks, and in another process with another environment: the key is the same every time.
func TestDerivationIgnoresBuildEnvironmentAndProcess(t *testing.T) {
	path := testPath(t)
	first := openTestStore(t, path, nil)
	want := bytes.Clone(first.key)
	first.Close()
	zones := []string{"UTC", "Asia/Tokyo", "Pacific/Chatham", "America/St_Johns"}
	for i, build := range []buildinfo.Identity{testBuild, {Version: "v0.8.0", BuildNumber: 45, Profile: buildinfo.ProfileDevelopment}, {Version: "v9.9.9", Profile: buildinfo.ProfilePublic}} {
		zone, err := time.LoadLocation(zones[i%len(zones)])
		if err != nil {
			t.Fatal(err)
		}
		store, err := Open(Options{Path: path, Location: zone, Build: &build, Now: func() time.Time { return time.Unix(int64(i)*1e9, 0) }})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(store.key, want) {
			t.Fatalf("build %+v derived another key", build)
		}
		store.Close()
	}
	challenge := make([]byte, 32)
	if _, err := rand.Read(challenge); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	other := exec.Command(executable, "-test.run=^TestKeyDerivationHelperProcess$", "-test.count=1")
	other.Dir = t.TempDir()
	other.Env = append(os.Environ(), "WWLOGS_KDF_HELPER=1", "TZ=Pacific/Chatham", "LANG=ja_JP.UTF-8", "LC_ALL=ko_KR.UTF-8", "GOMAXPROCS=1", "GOGC=off",
		"TEMP="+t.TempDir(), "TMP="+t.TempDir())
	other.Stdin = strings.NewReader(hex.EncodeToString(challenge) + "\n")
	output, err := other.Output()
	if err != nil {
		t.Fatalf("helper: %v %s", err, output)
	}
	mac := hmac.New(sha256.New, challenge)
	mac.Write(want)
	if !strings.Contains(string(output), "commitment "+hex.EncodeToString(mac.Sum(nil))) {
		t.Fatal("another process with another environment derived another key")
	}
}

// TestKeyDerivationHelperProcess is not a test of its own: TestDerivationIgnoresBuildEnvironmentAndProcess starts the test
// binary again with WWLOGS_KDF_HELPER set; it then derives the key of the default synthetic inputs and prints only an HMAC of
// it under the challenge read from stdin, never the key.
func TestKeyDerivationHelperProcess(t *testing.T) {
	if os.Getenv("WWLOGS_KDF_HELPER") != "1" {
		t.Skip("runs only as the helper process of another test")
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	challenge, err := hex.DecodeString(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := newHistoryKey(platformKeyInputs)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, challenge)
	mac.Write(key.key)
	fmt.Printf("commitment %x\n", mac.Sum(nil))
}

// TestPlatformKeyInputsOpenAHistory opens a history with the real inputs of a build: this computer's SMBIOS table and the
// protocol key the build embeds. It runs only in a test binary that carries the embedded key (scripts/embed-key.go
// -test-binary ./internal/history); elsewhere the key is absent and it skips. The history is closed and opened again here
// and in another process, which must derive the same key on its own. Nothing of the inputs or the key is printed.
func TestPlatformKeyInputsOpenAHistory(t *testing.T) {
	material, err := capture.ProtocolKeyMaterial()
	if err != nil {
		t.Skip("this test binary carries no embedded protocol key")
	}
	clear(material)
	path := os.Getenv("WWLOGS_PLATFORM_HISTORY")
	if path == "" {
		path = testPath(t)
	}
	inputs := KeyInputs{ProtocolKey: capture.ProtocolKeyMaterial}
	if os.Getenv("WWLOGS_PLATFORM_HELPER") == "1" {
		store, err := openWith(path, inputs)
		if err != nil {
			t.Fatalf("another process could not open the history: %v", err)
		}
		recordOneEncounter(t, store)
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		return
	}
	store, err := openWith(path, inputs)
	if err != nil {
		t.Fatal(err)
	}
	recordOneEncounter(t, store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	other := exec.Command(executable, "-test.run=^TestPlatformKeyInputsOpenAHistory$", "-test.count=1")
	other.Dir = t.TempDir()
	other.Env = append(os.Environ(), "WWLOGS_PLATFORM_HELPER=1", "WWLOGS_PLATFORM_HISTORY="+path, "TZ=Asia/Tokyo", "LANG=en_US.UTF-8")
	if output, err := other.CombinedOutput(); err != nil {
		t.Fatalf("helper process: %v\n%s", err, output)
	}
	reopened, err := openWith(path, inputs)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if status := reopened.Status(); status.KeyProtection != keyProtectionDerived {
		t.Fatalf("status = %+v", status)
	}
	if count := countRows(t, reopened, "encounter"); count != 2 {
		t.Fatalf("%d encounters, want one from each process", count)
	}
	if _, err := os.Stat(keyPath(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a key file was written")
	}
}

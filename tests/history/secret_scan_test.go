package history

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf16"

	"wwlogs/internal/capture"
)

// TestSecretScanCollector looks for this computer's real History key material in files: the motherboard identifiers, the
// game protocol key the build embeds, the history key derived from them and its check value, each in the forms a leak would
// take (raw bytes, hex in either case, UUID text in either byte order, Base64, UTF-16). It needs the embedded key, so it runs
// in a test binary built by scripts/embed-key.go (-test-binary ./internal/history). WWLOGS_SECRET_SCAN_LIST names a file
// listing one path per line; WWLOGS_SECRET_SCAN_OUT receives the hit count of each needle kind per file. Nothing derived
// from the material is printed or written: a positive control proves every needle is found before the files are read.
func TestSecretScanCollector(t *testing.T) {
	list, out := os.Getenv("WWLOGS_SECRET_SCAN_LIST"), os.Getenv("WWLOGS_SECRET_SCAN_OUT")
	if list == "" || out == "" {
		t.Skip("WWLOGS_SECRET_SCAN_LIST and WWLOGS_SECRET_SCAN_OUT are not set")
	}
	needles := realKeyNeedles(t)
	defer func() {
		for _, needle := range needles {
			clear(needle.value)
		}
	}()
	// Positive control, in memory only: a buffer holding every needle between other bytes must report every kind, through the
	// same matching the files go through.
	var control []byte
	for _, needle := range needles {
		control = append(append(append(control, "<<"...), needle.value...), ">>"...)
	}
	found := needleHits(control, needles)
	clear(control)
	for _, needle := range needles {
		if found[needle.kind] == 0 {
			t.Fatalf("the positive control did not find %s", needle.kind)
		}
	}
	file, err := os.Open(list)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	report := struct {
		Needles int                       `json:"needles"`
		Kinds   map[string]int            `json:"kinds"`
		Files   int                       `json:"files"`
		Bytes   int64                     `json:"bytes"`
		Hits    map[string]map[string]int `json:"hits"`
		Missing []string                  `json:"missing,omitempty"`
	}{Needles: len(needles), Kinds: map[string]int{}, Hits: map[string]map[string]int{}}
	for _, needle := range needles {
		report.Kinds[needle.kind]++
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		path := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\xef\xbb\xbf"))
		if path == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			report.Missing = append(report.Missing, path)
			continue
		}
		report.Files++
		report.Bytes += int64(len(data))
		if hits := needleHits(data, needles); len(hits) > 0 {
			report.Hits[path] = hits
		}
		clear(data)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	data, _ := json.MarshalIndent(report, "", "  ")
	if err := os.WriteFile(out, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d needles of %d kinds over %d files (%d bytes): %d files with hits, %d unreadable", report.Needles, len(report.Kinds), report.Files, report.Bytes, len(report.Hits), len(report.Missing))
}

// needleHits counts every needle kind found in data.
func needleHits(data []byte, needles []keyNeedle) map[string]int {
	hits := map[string]int{}
	for _, needle := range needles {
		if count := bytes.Count(data, needle.value); count > 0 {
			hits[needle.kind] += count
		}
	}
	return hits
}

type keyNeedle struct {
	kind  string
	value []byte
}

// realKeyNeedles builds every form of this computer's real key material to look for. Short forms (under 12 bytes) are left
// out: they would match unrelated data.
func realKeyNeedles(t *testing.T) []keyNeedle {
	t.Helper()
	material, err := capture.ProtocolKeyMaterial()
	if err != nil {
		t.Skip("this test binary carries no embedded protocol key")
	}
	defer clear(material)
	protocolKey, err := canonicalProtocolKey(material)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(protocolKey)
	raw, err := readFirmwareTable()
	if err != nil {
		t.Fatal(err)
	}
	defer clear(raw)
	machine, err := machineFromSMBIOS(raw)
	if err != nil {
		t.Fatal(err)
	}
	defer machine.wipe()
	var needles []keyNeedle
	add := func(kind string, value []byte) {
		if len(value) >= 12 {
			needles = append(needles, keyNeedle{kind: kind, value: bytes.Clone(value)})
		}
	}
	forms := func(kind string, value []byte) {
		add(kind+":raw", value)
		add(kind+":hex", []byte(hex.EncodeToString(value)))
		add(kind+":HEX", []byte(strings.ToUpper(hex.EncodeToString(value))))
		add(kind+":base64", []byte(base64.StdEncoding.EncodeToString(value)))
		add(kind+":base64url", []byte(base64.RawURLEncoding.EncodeToString(value)))
	}
	if uuid := machine.SystemUUID; len(uuid) == systemUUIDSize {
		for name, order := range map[string][]byte{"stored": uuid, "swapped": uuidByteOrderSwapped(uuid)} {
			forms("uuid-"+name, order)
			text := uuidDisplay(order)
			add("uuid-"+name+":text", []byte(text))
			add("uuid-"+name+":text-lower", []byte(strings.ToLower(text)))
			add("uuid-"+name+":utf16", utf16Bytes(text))
		}
	}
	if serial, ok := canonicalSerial(machine.BaseboardSerial); ok && len(serial) > 0 {
		forms("serial", serial)
		add("serial:utf16", utf16Bytes(string(serial)))
	}
	forms("protocol-key", protocolKey)
	// The body of a PEM block starts with the Base64 of the DER; its first line is enough to find a copy.
	if encoded := base64.StdEncoding.EncodeToString(protocolKey); len(encoded) >= 64 {
		add("protocol-key:pem-line", []byte(encoded[:64]))
	}
	for _, source := range []string{sourceSystemUUID, sourceBaseboardSerial} {
		identifier, err := identifierFor(source, machine, false)
		if err != nil {
			continue
		}
		key, check, err := deriveHistoryKey(source, identifier, protocolKey)
		clear(identifier)
		if err != nil {
			t.Fatal(err)
		}
		forms("history-key-"+source, key)
		forms("history-key-"+source+":half1", key[:32])
		forms("history-key-"+source+":half2", key[32:])
		forms("key-check-"+source, check)
		clear(key)
	}
	return needles
}

func utf16Bytes(text string) []byte {
	var out []byte
	for _, unit := range utf16.Encode([]rune(text)) {
		out = append(out, byte(unit), byte(unit>>8))
	}
	return out
}

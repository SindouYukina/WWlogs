package desktop

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestLicenseLinksOpenOnlyTheListedAddresses(t *testing.T) {
	var opened []string
	bridge := &Bridge{openBrowser: func(address string) error {
		opened = append(opened, address)
		return nil
	}}
	for address := range licenseLinks {
		if err := bridge.OpenLicenseLink(address); err != nil {
			t.Fatalf("%s: %v", address, err)
		}
	}
	if len(opened) != len(licenseLinks) {
		t.Fatalf("opened %v", opened)
	}
	opened = nil
	for _, address := range []string{"", "https://example.com/", "file:///C:/Windows/System32/calc.exe", "https://github.com/rsms/inter/../../evil", "http://github.com/rsms/inter", "https://github.com/SindouYukina/WWlogs/../evil", "http://wutheringwaves.kurogames.com/"} {
		if err := bridge.OpenLicenseLink(address); err == nil {
			t.Errorf("%q was accepted", address)
		}
	}
	if len(opened) != 0 {
		t.Fatalf("a refused address reached the browser: %v", opened)
	}
}

func TestLicenseLinksMatchTheFrontendList(t *testing.T) {
	found := map[string]struct{}{}
	for _, name := range []string{"font-licenses.ts", "legal-notices.ts"} {
		source, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "lib", name))
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range regexp.MustCompile(`url: "([^"]+)"`).FindAllSubmatch(source, -1) {
			found[string(match[1])] = struct{}{}
		}
	}
	want := slices.Sorted(maps.Keys(licenseLinks))
	got := slices.Sorted(maps.Keys(found))
	if !slices.Equal(want, got) {
		t.Fatalf("frontend links %v, host links %v", got, want)
	}
}

// THIRD_PARTY_NOTICES.md names each Go module and npm package at a version; a dependency change that leaves the notice
// behind fails here. Rows whose first cell is a code span are components: a path with a dot is a Go module (go.mod), any
// other name an npm package (frontend/package-lock.json).
func TestThirdPartyNoticesNameThePinnedVersions(t *testing.T) {
	read := func(parts ...string) []byte {
		data, err := os.ReadFile(filepath.Join(append([]string{"..", ".."}, parts...)...))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	notices := strings.ReplaceAll(string(read("THIRD_PARTY_NOTICES.md")), "\r\n", "\n")
	goMod := strings.ReplaceAll(string(read("go.mod")), "\r\n", "\n")
	var lock struct {
		Packages map[string]struct {
			Version string `json:"version"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(read("frontend", "package-lock.json"), &lock); err != nil {
		t.Fatal(err)
	}
	rows := regexp.MustCompile("(?m)^\\| `([^`]+)` \\| ([^|]+?) \\|").FindAllStringSubmatch(notices, -1)
	modules, packages := 0, 0
	for _, row := range rows {
		name, version := row[1], row[2]
		if strings.Contains(name, ".") {
			modules++
			pinned := regexp.MustCompile(`(?m)^\s*(?:require\s+)?` + regexp.QuoteMeta(name) + ` (v\S+)`).FindStringSubmatch(goMod)
			if pinned == nil || pinned[1] != version {
				t.Errorf("%s: the notice names %s, go.mod pins %v", name, version, pinned)
			}
			continue
		}
		packages++
		if pinned, ok := lock.Packages["node_modules/"+name]; !ok || pinned.Version != version {
			t.Errorf("%s: the notice names %s, the lockfile pins %q", name, version, pinned.Version)
		}
	}
	if modules < 16 || packages < 8 {
		t.Fatalf("the notice lists %d Go modules and %d npm packages; the component tables were not read", modules, packages)
	}
	// The ownership statements the notice exists for.
	for _, statement := range []string{"`AGPL-3.0-only`", "belong to KURO GAMES or their respective rights holders", "is not covered by its AGPL-3.0-only license", "does not claim any official partnership, sponsorship or endorsement", "<https://wutheringwaves.kurogames.com/>", "[LICENSE](LICENSE)"} {
		if !strings.Contains(notices, statement) {
			t.Errorf("THIRD_PARTY_NOTICES.md no longer states %q", statement)
		}
	}
	if strings.Contains(notices, "AGPL-3.0-or-later") || strings.Contains(notices, "any later version") {
		t.Error("WWlogs is licensed under AGPL-3.0-only, not a later version")
	}
}

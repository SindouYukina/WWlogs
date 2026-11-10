package desktop

import (
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
	for _, address := range []string{"", "https://example.com/", "file:///C:/Windows/System32/calc.exe", "https://github.com/rsms/inter/../../evil", "http://github.com/rsms/inter"} {
		if err := bridge.OpenLicenseLink(address); err == nil {
			t.Errorf("%q was accepted", address)
		}
	}
	if len(opened) != 0 {
		t.Fatalf("a refused address reached the browser: %v", opened)
	}
}

func TestLicenseLinksMatchTheFrontendList(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "lib", "font-licenses.ts"))
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]struct{}{}
	for _, match := range regexp.MustCompile(`url: "([^"]+)"`).FindAllSubmatch(source, -1) {
		found[string(match[1])] = struct{}{}
	}
	want := slices.Sorted(maps.Keys(licenseLinks))
	got := slices.Sorted(maps.Keys(found))
	if !slices.Equal(want, got) {
		t.Fatalf("frontend links %v, host links %v", got, want)
	}
}

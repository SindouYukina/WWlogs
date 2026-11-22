package desktop

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode"
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

// libraryComponent is one entry of frontend/src/lib/library-licenses/manifest.json, the third-party software the
// executables contain with the license files Settings > Licenses shows for it.
type libraryComponent struct {
	ID          string   `json:"id"`
	Group       string   `json:"group"`
	Source      string   `json:"source"`
	LicenseFrom string   `json:"licenseFrom"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Files       []string `json:"files"`
	Generated   bool     `json:"generated"`
}

func readRepository(t *testing.T, parts ...string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(append([]string{"..", ".."}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func libraryManifest(t *testing.T) []libraryComponent {
	t.Helper()
	var manifest struct {
		Components []libraryComponent `json:"components"`
	}
	if err := json.Unmarshal(readRepository(t, "frontend", "src", "lib", "library-licenses", "manifest.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Components) == 0 {
		t.Fatal("the library license manifest lists no component")
	}
	return manifest.Components
}

// goEnv reads one setting of the Go toolchain that runs the tests, the same one that builds the executables.
func goEnv(t *testing.T, name string) string {
	t.Helper()
	out, err := exec.Command("go", "env", name).Output()
	if err != nil {
		t.Fatalf("go env %s: %v", name, err)
	}
	return strings.TrimSpace(string(out))
}

// moduleCachePath escapes a module path the way the module cache stores it: every upper-case letter as "!" + lower case.
func moduleCachePath(module string) string {
	var escaped strings.Builder
	for _, r := range module {
		if unicode.IsUpper(r) {
			escaped.WriteByte('!')
			r = unicode.ToLower(r)
		}
		escaped.WriteRune(r)
	}
	return escaped.String()
}

func normalizedLines(data []byte) []byte {
	return bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
}

// Every Go module either profile's desktop executable links is listed with its license, at the version it links, and
// nothing else is: a dependency added, removed or upgraded without its license fails here.
func TestLibraryLicensesListTheLinkedModules(t *testing.T) {
	listed := map[string]string{}
	for _, component := range libraryManifest(t) {
		if component.Source == "go-module" {
			listed[component.Name] = component.Version
		}
	}
	// The tags of the two profiles' desktop builds: Public builds with production, Development with wwdev.
	for _, tags := range []string{"production", "wwdev"} {
		command := exec.Command("go", "list", "-e", "-deps", "-tags", tags, "-f", "{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}}{{end}}{{end}}", "./cmd/desktop")
		command.Dir = filepath.Join("..", "..")
		out, err := command.Output()
		if err != nil {
			t.Fatalf("go list (%s): %v", tags, err)
		}
		linked := map[string]string{}
		for _, line := range strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n") {
			if path, version, ok := strings.Cut(strings.TrimSpace(line), " "); ok {
				linked[path] = version
			}
		}
		if !maps.Equal(linked, listed) {
			t.Errorf("%s profile links %v; the library license manifest lists %v", tags, linked, listed)
		}
	}
}

// The license files the Licenses page shows are the unmodified files of the toolchain and of each module at its pinned
// version (line endings aside, which the checkout may change).
func TestLibraryLicenseFilesAreTheUpstreamFiles(t *testing.T) {
	goroot, modcache := goEnv(t, "GOROOT"), goEnv(t, "GOMODCACHE")
	checked := 0
	for _, component := range libraryManifest(t) {
		var upstream string
		switch {
		case component.LicenseFrom != "":
			module, version, _ := strings.Cut(component.LicenseFrom, "@")
			upstream = filepath.Join(modcache, moduleCachePath(module)+"@"+version)
		case component.Source == "go":
			upstream = goroot
		case component.Source == "go-module":
			upstream = filepath.Join(modcache, moduleCachePath(component.Name)+"@"+component.Version)
		default:
			continue // npm packages: tests/frontend/licenses.test.mjs compares them with the installed packages
		}
		for _, file := range component.Files {
			want, err := os.ReadFile(filepath.Join(upstream, file))
			if err != nil {
				t.Errorf("%s: upstream %s: %v", component.ID, file, err)
				continue
			}
			got := readRepository(t, "frontend", "src", "lib", "library-licenses", component.ID, file)
			if !bytes.Equal(normalizedLines(got), normalizedLines(want)) {
				t.Errorf("%s/%s differs from %s", component.ID, file, filepath.Join(upstream, file))
			}
			checked++
		}
	}
	if checked < 27 {
		t.Fatalf("compared only %d license files", checked)
	}
}

// THIRD_PARTY_NOTICES.md names each Go module and npm package at a version; a dependency change that leaves the notice
// behind fails here. Rows whose first cell is a code span are components: a path with a dot is a Go module (go.mod), any
// other name an npm package (frontend/package-lock.json). The same components are in the library license manifest.
func TestThirdPartyNoticesNameThePinnedVersions(t *testing.T) {
	notices := strings.ReplaceAll(string(readRepository(t, "THIRD_PARTY_NOTICES.md")), "\r\n", "\n")
	goMod := strings.ReplaceAll(string(readRepository(t, "go.mod")), "\r\n", "\n")
	var lock struct {
		Packages map[string]struct {
			Version string `json:"version"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(readRepository(t, "frontend", "package-lock.json"), &lock); err != nil {
		t.Fatal(err)
	}
	rows := regexp.MustCompile("(?m)^\\| `([^`]+)` \\| ([^|]+?) \\|").FindAllStringSubmatch(notices, -1)
	named := map[string]string{}
	modules, packages := 0, 0
	for _, row := range rows {
		name, version := row[1], row[2]
		named[name] = version
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
	manifest := map[string]string{}
	for _, component := range libraryManifest(t) {
		if component.Source != "go" {
			manifest[component.Name] = component.Version
		}
	}
	if !maps.Equal(named, manifest) {
		t.Errorf("THIRD_PARTY_NOTICES.md names %v; the library license manifest lists %v", named, manifest)
	}
	// The ownership statements the notice exists for.
	for _, statement := range []string{"`AGPL-3.0-only`", "Copyright (C) 2026 SindouYukina", "This copyright notice covers only the source code written for WWlogs", "belong to KURO GAMES or their respective rights holders", "is not covered by its AGPL-3.0-only license", "does not claim any official partnership, sponsorship or endorsement", "<https://wutheringwaves.kurogames.com/>", "[LICENSE](LICENSE)", "frontend/src/lib/library-licenses/"} {
		if !strings.Contains(notices, statement) {
			t.Errorf("THIRD_PARTY_NOTICES.md no longer states %q", statement)
		}
	}
	if strings.Contains(notices, "AGPL-3.0-or-later") || strings.Contains(notices, "any later version") {
		t.Error("WWlogs is licensed under AGPL-3.0-only, not a later version")
	}
}

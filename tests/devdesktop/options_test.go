package devdesktop

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"wwlogs/internal/capture"
)

func TestDevelopmentDiagnosticDefaultsWithoutAutostart(t *testing.T) {
	options, err := ParseOptions(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if options.MasterDatabase != "" {
		t.Fatalf("default Master source = %q, want embedded", options.MasterDatabase)
	}
	if options.AutoStart || !options.Session.Diagnostics {
		t.Fatalf("defaults: autostart=%v diagnostics=%v, want live diagnostics without autostart", options.AutoStart, options.Session.Diagnostics)
	}
	override, err := ParseOptions([]string{"-master-db", "test-master.db"}, io.Discard)
	if err != nil || override.MasterDatabase != "test-master.db" {
		t.Fatalf("Development Master override = %+v, %v", override, err)
	}
	replay, err := ParseOptions([]string{"-replay", "sample.pcap"}, io.Discard)
	if err != nil || replay.Session.Diagnostics || !replay.AutoStart {
		t.Fatalf("replay defaults: %+v %v", replay, err)
	}
	if _, err := ParseOptions([]string{"-replay", "sample.pcap", "-capture-diagnostics=true"}, io.Discard); err == nil {
		t.Fatal("explicit replay recording accepted")
	}
}

func TestEnvironmentSimulationFlags(t *testing.T) {
	options, err := ParseOptions([]string{"-simulate-capture-environment", "npcap_missing,no_ipv4", "-simulate-npcap-offline", "-record-browser-url", "out.txt"}, io.Discard)
	if err != nil || options.EnvironmentProbe == nil || options.NpcapTransport == nil || options.OpenBrowser == nil {
		t.Fatalf("simulation options: %+v %v", options, err)
	}
	if first := options.EnvironmentProbe(); first.State != capture.EnvironmentNpcapMissing {
		t.Fatalf("first = %+v", first)
	}
	for range 2 {
		if next := options.EnvironmentProbe(); next.State != capture.EnvironmentNoIPv4 {
			t.Fatalf("the last state must repeat: %+v", next)
		}
	}
	if _, err := ParseOptions([]string{"-simulate-capture-environment", "guess"}, io.Discard); err == nil {
		t.Fatal("an unknown state was accepted")
	}
	defaults, err := ParseOptions(nil, io.Discard)
	if err != nil || defaults.EnvironmentProbe != nil || defaults.NpcapTransport != nil || defaults.OpenBrowser != nil {
		t.Fatalf("defaults must use the real system: %+v %v", defaults, err)
	}
}

func TestShowcaseFlags(t *testing.T) {
	options, err := ParseOptions([]string{"-showcase", "-showcase-overlays", "-asset-root", "example", "-font-root", "local-fonts"}, io.Discard)
	if err != nil || options.Review == nil || options.GameAssetRoot != "example" {
		t.Fatalf("Development Showcase options: %+v %v", options, err)
	}
	if options.Review.Route != "/?view=showcase" || options.Review.Open == nil || options.Review.Assets == nil {
		t.Fatalf("the Showcase surface must open its route, its Overlay windows and its font candidates: %+v", *options.Review)
	}
	plain, err := ParseOptions([]string{"-showcase"}, io.Discard)
	if err != nil || plain.Review == nil || plain.Review.Route != "/?view=showcase" || plain.Review.Open != nil || plain.Review.Assets != nil {
		t.Fatalf("-showcase alone opens only the Showcase window: %+v %v", plain.Review, err)
	}
	if _, err := ParseOptions([]string{"-showcase-overlays"}, io.Discard); err == nil {
		t.Fatal("Showcase overlay windows were accepted without -showcase")
	}
	defaults, err := ParseOptions(nil, io.Discard)
	if err != nil || defaults.Review != nil || defaults.GameAssetRoot != "" {
		t.Fatalf("defaults must not open the Showcase or find assets on their own: %+v %v", defaults, err)
	}
	if _, err := ParseOptions([]string{"-font-root", "local-fonts"}, io.Discard); err == nil {
		t.Fatal("font root was accepted without -showcase")
	}
}

// The Showcase serves font candidates only from the explicitly supplied directory, only font files and the manifest, and
// passes every other route to the host's asset handler. A font root that is not a directory is reported, not fatal.
func TestShowcaseFontRouteUsesSeparateRootAndAllowsOnlyFonts(t *testing.T) {
	fonts := fstest.MapFS{
		"manifest.json":  &fstest.MapFile{Data: []byte("{}")},
		"sarasa/a.woff2": &fstest.MapFile{Data: []byte("font")},
		"secret.txt":     &fstest.MapFile{Data: []byte("secret")},
	}
	host := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(response, "host") })
	handler := withPreviewFonts(host, fonts)
	for _, test := range []struct {
		path   string
		status int
		body   string
	}{
		{"/preview-fonts/manifest.json", http.StatusOK, "{}"},
		{"/preview-fonts/sarasa/a.woff2", http.StatusOK, "font"},
		{"/preview-fonts/../secret.txt", http.StatusNotFound, ""},
		{"/preview-fonts/secret.txt", http.StatusNotFound, ""},
		{"/preview-fonts/sarasa/a.exe", http.StatusNotFound, ""},
		{"/preview-fonts/missing.woff2", http.StatusNotFound, ""},
		{"/game-assets/character-avatar/1205", http.StatusOK, "host"},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
		if recorder.Code != test.status {
			t.Fatalf("%s: status %d, want %d", test.path, recorder.Code, test.status)
		}
		if test.status == http.StatusOK && recorder.Body.String() != test.body {
			t.Fatalf("%s: body %q, want %q", test.path, recorder.Body.String(), test.body)
		}
	}
	var warnings strings.Builder
	missing := showcaseSurface(false, filepath.Join(t.TempDir(), "missing"), &warnings)
	recorder := httptest.NewRecorder()
	missing.Assets(host).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/preview-fonts/a.woff2", nil))
	if recorder.Code != http.StatusNotFound || !strings.Contains(warnings.String(), "-font-root directory was not found") {
		t.Fatalf("a missing font root: status %d, warning %q", recorder.Code, warnings.String())
	}
}

// The remote debugging port joins the requested browser arguments, after them; without a request there is none.
func TestWebviewArgumentsCarryTheDebuggingPort(t *testing.T) {
	options, err := ParseOptions([]string{"-webview-args", "--a --b", "-webview-debug-port", "9333"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--a", "--b", "--remote-debugging-port=9333"}
	if len(options.WebviewArguments) != len(want) {
		t.Fatalf("arguments = %q, want %q", options.WebviewArguments, want)
	}
	for i := range want {
		if options.WebviewArguments[i] != want[i] {
			t.Fatalf("arguments = %q, want %q", options.WebviewArguments, want)
		}
	}
	defaults, err := ParseOptions(nil, io.Discard)
	if err != nil || len(defaults.WebviewArguments) != 0 {
		t.Fatalf("defaults must add no browser argument: %q %v", defaults.WebviewArguments, err)
	}
}

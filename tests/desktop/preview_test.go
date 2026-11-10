package desktop

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"wwlogs/internal/buildinfo"
)

func TestGameAssetRouteResolvesConfigurationIDsOnly(t *testing.T) {
	source := fstest.MapFS{
		"game/characters/1205/avatar.webp": &fstest.MapFile{Data: []byte("avatar")},
		"game/buffs/10010000/icon.webp":    &fstest.MapFile{Data: []byte("buff")},
		"game/secret.txt":                  &fstest.MapFile{Data: []byte("secret")},
	}
	frontend := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(response, "frontend") })
	handler := withGameAssets(frontend, source)
	for _, test := range []struct {
		path   string
		status int
		body   string
	}{
		{"/game-assets/character-avatar/1205", http.StatusOK, "avatar"},
		{"/game-assets/buff/10010000", http.StatusOK, "buff"},
		{"/game-assets/character-portrait/1205", http.StatusNotFound, ""},
		{"/game-assets/character-avatar/0", http.StatusNotFound, ""},
		{"/game-assets/character-avatar/..%2F..%2Fsecret", http.StatusNotFound, ""},
		{"/game-assets/character-avatar/1205/extra", http.StatusNotFound, ""},
		{"/game-assets/unknown-kind/1205", http.StatusNotFound, ""},
		{"/game-assets/character-avatar", http.StatusNotFound, ""},
		{"/index.html", http.StatusOK, "frontend"},
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
}

func TestPreviewFontRouteUsesSeparateRootAndAllowsOnlyFonts(t *testing.T) {
	gameAssets := fstest.MapFS{
		"game/characters/1205/avatar.webp": &fstest.MapFile{Data: []byte("avatar")},
	}
	fonts := fstest.MapFS{
		"manifest.json":  &fstest.MapFile{Data: []byte("{}")},
		"sarasa/a.woff2": &fstest.MapFile{Data: []byte("font")},
		"secret.txt":     &fstest.MapFile{Data: []byte("secret")},
	}
	frontend := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(response, "frontend") })
	handler := withPreviewFonts(withGameAssets(frontend, gameAssets), fonts)
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
		{"/game-assets/character-avatar/1205", http.StatusOK, "avatar"},
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
}

func TestShowcaseFlagsExistOnlyInDevelopment(t *testing.T) {
	options, err := parseOptions([]string{"-showcase", "-showcase-overlays", "-asset-root", "example", "-font-root", "local-fonts"}, io.Discard)
	if buildinfo.Current().Profile != buildinfo.ProfileDevelopment {
		if err == nil {
			t.Fatal("Public accepted Showcase flags")
		}
		return
	}
	if err != nil || !options.Showcase || !options.ShowcaseOverlays || options.GameAssetRoot != "example" || options.PreviewFontRoot != "local-fonts" {
		t.Fatalf("Development Showcase options: %+v %v", options, err)
	}
	if _, err := parseOptions([]string{"-showcase-overlays"}, io.Discard); err == nil {
		t.Fatal("Showcase overlay windows were accepted without -showcase")
	}
	defaults, err := parseOptions(nil, io.Discard)
	if err != nil || defaults.Showcase || defaults.GameAssetRoot != "" || defaults.PreviewFontRoot != "" {
		t.Fatalf("defaults must not enable the Showcase or find assets on their own: %+v %v", defaults, err)
	}
	if _, err := parseOptions([]string{"-font-root", "local-fonts"}, io.Discard); err == nil {
		t.Fatal("font root was accepted without -showcase")
	}
}

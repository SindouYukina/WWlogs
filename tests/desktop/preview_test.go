package desktop

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
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

// The Showcase flags are Development flags (tests/devdesktop); the Public command line refuses them, and the Public
// options open no review surface.
func TestPublicOptionsRefuseShowcase(t *testing.T) {
	if _, err := ParseOptions([]string{"-showcase", "-showcase-overlays", "-asset-root", "example", "-font-root", "local-fonts"}, io.Discard); err == nil {
		t.Fatal("Public accepted Showcase flags")
	}
	defaults, err := ParseOptions(nil, io.Discard)
	if err != nil || defaults.Review != nil || defaults.GameAssetRoot != "" {
		t.Fatalf("defaults must not open a review surface or find assets on their own: %+v %v", defaults, err)
	}
}

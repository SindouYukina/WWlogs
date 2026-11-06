package desktop

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wwlogs/internal/assets"
	"wwlogs/internal/buildinfo"
)

func TestFrontendFallbackWithoutArchive(t *testing.T) {
	saved := frontendArchive
	defer func() { frontendArchive = saved }()
	frontendArchive = nil
	assets, embedded, err := frontendFS()
	if err != nil || embedded {
		t.Fatalf("fallback: embedded=%t err=%v", embedded, err)
	}
	page, err := fs.ReadFile(assets, "index.html")
	if err != nil || !strings.Contains(string(page), "built without the frontend") {
		t.Fatalf("fallback page = %q, %v", page, err)
	}
}

func TestFrontendArchiveIsServed(t *testing.T) {
	saved := frontendArchive
	defer func() { frontendArchive = saved }()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for name, body := range map[string]string{"index.html": "<html>ok</html>", "assets/app.js": "console.log(1)"} {
		entry, _ := writer.Create(name)
		_, _ = entry.Write([]byte(body))
	}
	_ = writer.Close()
	frontendArchive = archive.Bytes()
	assets, embedded, err := frontendFS()
	if err != nil || !embedded {
		t.Fatalf("archive: embedded=%t err=%v", embedded, err)
	}
	if script, err := fs.ReadFile(assets, "assets/app.js"); err != nil || string(script) != "console.log(1)" {
		t.Fatalf("assets/app.js = %q, %v", script, err)
	}

	frontendArchive = []byte("not a zip")
	if _, _, err := frontendFS(); err == nil {
		t.Fatal("a corrupt archive was accepted")
	}
}

func TestDiagnosticDefaultsFollowProfileWithoutAutostart(t *testing.T) {
	options, err := parseOptions(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	want := buildinfo.Current().Profile == buildinfo.ProfileDevelopment
	if options.MasterDatabase != "" {
		t.Fatalf("default Master source = %q, want embedded", options.MasterDatabase)
	}
	if options.AutoStart || options.Session.Diagnostics != want {
		t.Fatalf("defaults: autostart=%v diagnostics=%v want=%v", options.AutoStart, options.Session.Diagnostics, want)
	}
	if want {
		override, err := parseOptions([]string{"-master-db", "test-master.db"}, io.Discard)
		if err != nil || override.MasterDatabase != "test-master.db" {
			t.Fatalf("Development Master override = %+v, %v", override, err)
		}
		replay, err := parseOptions([]string{"-replay", "sample.pcap"}, io.Discard)
		if err != nil || replay.Session.Diagnostics || !replay.AutoStart {
			t.Fatalf("replay defaults: %+v %v", replay, err)
		}
		if _, err := parseOptions([]string{"-replay", "sample.pcap", "-capture-diagnostics=true"}, io.Discard); err == nil {
			t.Fatal("explicit replay recording accepted")
		}
	} else {
		if _, err := parseOptions([]string{"-master-db", "test-master.db"}, io.Discard); err == nil {
			t.Fatal("Public accepted an external Master database")
		}
		if _, err := parseOptions([]string{"-capture-diagnostics=true"}, io.Discard); err == nil {
			t.Fatal("Public accepted recording switch")
		}
	}
}

func TestGameAssetSourceUsesEmbeddedPackByDefaultAndExplicitRootOverride(t *testing.T) {
	makeRoot := func(name string, image []byte) string {
		t.Helper()
		root := filepath.Join(t.TempDir(), name)
		imagePath := filepath.Join(root, "game", "characters", "1409", "avatar.webp")
		if err := os.MkdirAll(filepath.Dir(imagePath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(imagePath, image, 0o600); err != nil {
			t.Fatal(err)
		}
		return root
	}
	readImage := func(source fs.FS) []byte {
		t.Helper()
		file, err := assets.NewResolver(source).Open(assets.Key{Kind: assets.CharacterAvatar, ID: 1409})
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}

	embeddedImage := []byte("RIFF\x04\x00\x00\x00WEBP")
	embeddedHash := sha256.Sum256(embeddedImage)
	manifest := assets.Manifest{Schema: assets.ManifestSchema, Client: "test", Resource: "test", Entries: []assets.ManifestEntry{{
		Path: "game/characters/1409/avatar.webp", SHA256: hex.EncodeToString(embeddedHash[:]), Size: int64(len(embeddedImage)),
	}}}
	embeddedRoot := makeRoot("embedded", embeddedImage)
	var pack bytes.Buffer
	if _, err := assets.WritePack(&pack, os.DirFS(embeddedRoot), manifest); err != nil {
		t.Fatal(err)
	}
	defaultImage := readImage(gameAssetSource("", pack.String()))
	if !bytes.Equal(defaultImage, embeddedImage) {
		t.Fatal("default image source did not resolve the embedded pack")
	}

	overrideImage := []byte("RIFF\x04\x00\x00\x00WEBP")
	overrideImage[len(overrideImage)-1] = 'X'
	overrideRoot := makeRoot("override", overrideImage)
	gotOverride := readImage(gameAssetSource(overrideRoot, pack.String()))
	if !bytes.Equal(gotOverride, overrideImage) || bytes.Equal(gotOverride, defaultImage) {
		t.Fatal("explicit Development asset root did not override the embedded image")
	}
}

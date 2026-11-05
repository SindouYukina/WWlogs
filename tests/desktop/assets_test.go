package desktop

import (
	"archive/zip"
	"bytes"
	"io"
	"io/fs"
	"strings"
	"testing"
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
	if options.AutoStart || options.Session.Diagnostics != want {
		t.Fatalf("defaults: autostart=%v diagnostics=%v want=%v", options.AutoStart, options.Session.Diagnostics, want)
	}
	if want {
		replay, err := parseOptions([]string{"-replay", "sample.pcap"}, io.Discard)
		if err != nil || replay.Session.Diagnostics || !replay.AutoStart {
			t.Fatalf("replay defaults: %+v %v", replay, err)
		}
		if _, err := parseOptions([]string{"-replay", "sample.pcap", "-capture-diagnostics=true"}, io.Discard); err == nil {
			t.Fatal("explicit replay recording accepted")
		}
	} else {
		if _, err := parseOptions([]string{"-capture-diagnostics=true"}, io.Discard); err == nil {
			t.Fatal("Public accepted recording switch")
		}
	}
}

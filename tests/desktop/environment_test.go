package desktop

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"wwlogs/internal/assets"
	"wwlogs/internal/capture"
)

func TestEnvironmentCheckerSharesOneCheckAcrossRepeatedPresses(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	checker := newEnvironmentChecker(func() capture.CaptureEnvironment {
		calls.Add(1)
		<-release
		return capture.CaptureEnvironment{State: capture.EnvironmentReady}
	})
	var group sync.WaitGroup
	results := make([]capture.CaptureEnvironment, 8)
	for index := range results {
		group.Add(1)
		go func() {
			defer group.Done()
			results[index] = checker.check(context.Background())
		}()
	}
	// Every press must have joined before the check ends, or a later one would legitimately start a second check.
	for {
		checker.mu.Lock()
		running := checker.current != nil
		checker.mu.Unlock()
		if running {
			break
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	group.Wait()
	if calls.Load() != 1 {
		t.Fatalf("%d checks ran for 8 presses", calls.Load())
	}
	for _, result := range results {
		if result.State != capture.EnvironmentReady {
			t.Fatalf("result = %+v", result)
		}
	}
	// A press after the check finished is a new check.
	release = make(chan struct{})
	close(release)
	if checker.check(context.Background()).State != capture.EnvironmentReady || calls.Load() != 2 {
		t.Fatalf("second check: %d calls", calls.Load())
	}
}

func TestEnvironmentCheckerReportsAStuckCheckAndLaterRequestsJoinIt(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	var calls atomic.Int32
	checker := newEnvironmentChecker(func() capture.CaptureEnvironment {
		calls.Add(1)
		<-release
		return capture.CaptureEnvironment{State: capture.EnvironmentReady}
	})
	checker.limit = 20 * time.Millisecond
	for range 3 {
		if got := checker.check(context.Background()); got.State != capture.EnvironmentCheckFailed || got.Adapters == nil {
			t.Fatalf("stuck check: %+v", got)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("%d goroutines entered the library", calls.Load())
	}
}

// npcapFixture answers the official URLs only. Any other host fails the test, so these tests also pin the sources.
type npcapFixture struct {
	t         *testing.T
	home      func() (int, string)
	release   func() (int, string)
	installer func(path string) (int, http.Header)
	requests  atomic.Int32
}

func (f *npcapFixture) RoundTrip(request *http.Request) (*http.Response, error) {
	f.requests.Add(1)
	reply := func(status int, body string, header http.Header) (*http.Response, error) {
		if header == nil {
			header = http.Header{}
		}
		return &http.Response{StatusCode: status, Status: http.StatusText(status), Body: io.NopCloser(strings.NewReader(body)), Header: header, Request: request, ContentLength: contentLength(header)}, nil
	}
	switch {
	case request.Method == http.MethodGet && request.URL.String() == npcapHomeURL:
		status, body := f.home()
		return reply(status, body, nil)
	case request.Method == http.MethodGet && request.URL.String() == npcapReleaseAPI:
		status, body := f.release()
		return reply(status, body, nil)
	case request.Method == http.MethodHead && request.URL.Host == "npcap.com":
		status, header := f.installer(request.URL.Path)
		return reply(status, "", header)
	}
	f.t.Errorf("request outside the official sources: %s %s", request.Method, request.URL)
	return reply(http.StatusTeapot, "", nil)
}

func contentLength(header http.Header) int64 {
	if header.Get("Content-Length") == "1325256" {
		return 1325256
	}
	if header.Get("Content-Length") == "900" {
		return 900
	}
	return -1
}

const officialPage = `<a href="guide/npcap-devguide.html#npcap-devguide-updating">guide</a>
<a href="dist/npcap-1.90.exe">Npcap 1.90 installer</a> <a href="dist/npcap-sdk-1.17.zip">SDK</a>
<a href="dist/npcap-1.90-DebugSymbols.zip">symbols</a> <a href="dist/npcap-1.90.zip">source</a>`

func installerOK(string) (int, http.Header) {
	return http.StatusOK, http.Header{"Content-Type": {"application/octet-stream"}, "Content-Length": {"1325256"}}
}

func releaseJSON(tag string, extra string) func() (int, string) {
	return func() (int, string) {
		return http.StatusOK, `{"tag_name":"` + tag + `","draft":false,"prerelease":false,"assets":[],"zipball_url":"https://api.github.com/repos/nmap/npcap/zipball/` + tag + `","tarball_url":"https://api.github.com/repos/nmap/npcap/tarball/` + tag + `"` + extra + `}`
	}
}

func notFoundText() (int, string) { return http.StatusNotFound, "" }

func TestNpcapDownloadResolution(t *testing.T) {
	official := NpcapDownload{Version: "1.90", URL: "https://npcap.com/dist/npcap-1.90.exe", Direct: true}
	for _, test := range []struct {
		name    string
		fixture *npcapFixture
		want    NpcapDownload
	}{
		{"the download page names the installer", &npcapFixture{home: func() (int, string) { return http.StatusOK, officialPage }, release: notFoundText, installer: installerOK}, official},
		{"an absolute link on the official host", &npcapFixture{home: func() (int, string) { return http.StatusOK, `<a href="https://npcap.com/dist/npcap-1.90.exe">x</a>` }, release: notFoundText, installer: installerOK}, official},
		{"the highest version wins numerically", &npcapFixture{home: func() (int, string) {
			return http.StatusOK, `<a href="dist/npcap-1.90.exe">a</a><a href="dist/npcap-1.100.exe">b</a>`
		}, release: notFoundText, installer: installerOK}, NpcapDownload{Version: "1.100", URL: "https://npcap.com/dist/npcap-1.100.exe", Direct: true}},
		{"a changed page falls back to the release tag", &npcapFixture{home: func() (int, string) { return http.StatusOK, "<html>redesigned</html>" }, release: releaseJSON("v1.90", ""), installer: installerOK}, official},
		{"a download page outage falls back to the release tag", &npcapFixture{home: func() (int, string) { return http.StatusBadGateway, "" }, release: releaseJSON("v1.90", ""), installer: installerOK}, official},
		{"a link to another host is not an installer", &npcapFixture{home: func() (int, string) {
			return http.StatusOK, `<a href="https://mirror.example/dist/npcap-9.99.exe">x</a>`
		}, release: notFoundText, installer: installerOK}, npcapFallback()},
		{"a GitHub rate limit on a changed page leaves the official page", &npcapFixture{home: func() (int, string) { return http.StatusOK, "changed" }, release: func() (int, string) { return http.StatusForbidden, `{"message":"API rate limit exceeded"}` }, installer: installerOK}, npcapFallback()},
		{"unexpected release JSON", &npcapFixture{home: notFoundText, release: func() (int, string) { return http.StatusOK, "<html>" }, installer: installerOK}, npcapFallback()},
		{"a malformed tag is not a version", &npcapFixture{home: notFoundText, release: releaseJSON("nightly", ""), installer: installerOK}, npcapFallback()},
		{"a prerelease is not the latest official version", &npcapFixture{home: notFoundText, release: func() (int, string) { return http.StatusOK, `{"tag_name":"v2.00","prerelease":true}` }, installer: installerOK}, npcapFallback()},
		{"a tag without a published installer is not guessed", &npcapFixture{home: notFoundText, release: releaseJSON("v1.91", ""), installer: func(string) (int, http.Header) { return http.StatusNotFound, nil }}, npcapFallback()},
		{"an HTML error page is not an installer", &npcapFixture{home: func() (int, string) { return http.StatusOK, officialPage }, release: notFoundText, installer: func(string) (int, http.Header) {
			return http.StatusOK, http.Header{"Content-Type": {"text/html"}, "Content-Length": {"1325256"}}
		}}, npcapFallback()},
		{"a stub is not an installer", &npcapFixture{home: func() (int, string) { return http.StatusOK, officialPage }, release: notFoundText, installer: func(string) (int, http.Header) {
			return http.StatusOK, http.Header{"Content-Type": {"application/octet-stream"}, "Content-Length": {"900"}}
		}}, npcapFallback()},
	} {
		test.fixture.t = t
		resolver := newNpcapDownloadResolver(test.fixture)
		if got := resolver.resolve(context.Background()); got != test.want {
			t.Errorf("%s: %+v, want %+v", test.name, got, test.want)
		}
	}
}

func TestNpcapDownloadNeverUsesAReleaseSourceArchive(t *testing.T) {
	fixture := npcapFixture{t: t, home: notFoundText, release: releaseJSON("v1.90", ""), installer: installerOK}
	got := newNpcapDownloadResolver(&fixture).resolve(context.Background())
	if !strings.HasPrefix(got.URL, npcapInstallerBase) || strings.Contains(got.URL, "github") || strings.Contains(got.URL, "ball") {
		t.Fatalf("url = %s", got.URL)
	}
}

type offlineFixture struct{ requests atomic.Int32 }

func (f *offlineFixture) RoundTrip(*http.Request) (*http.Response, error) {
	f.requests.Add(1)
	return nil, context.DeadlineExceeded
}

func TestNpcapDownloadOfflineFallsBackAndRetriesLater(t *testing.T) {
	transport := &offlineFixture{}
	resolver := newNpcapDownloadResolver(transport)
	now := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	resolver.now = func() time.Time { return now }
	if got := resolver.resolve(context.Background()); got != npcapFallback() || got.Version != "" {
		t.Fatalf("offline: %+v", got)
	}
	first := transport.requests.Load()
	if resolver.resolve(context.Background()); transport.requests.Load() != first {
		t.Fatal("a failure was not cached for the repeat press")
	}
	now = now.Add(npcapFailedLifetime + time.Second)
	if resolver.resolve(context.Background()); transport.requests.Load() == first {
		t.Fatal("a failed lookup was never retried")
	}
}

func TestNpcapDownloadCachesASuccess(t *testing.T) {
	fixture := npcapFixture{t: t, home: func() (int, string) { return http.StatusOK, officialPage }, release: notFoundText, installer: installerOK}
	resolver := newNpcapDownloadResolver(&fixture)
	resolver.resolve(context.Background())
	first := fixture.requests.Load()
	resolver.resolve(context.Background())
	if fixture.requests.Load() != first {
		t.Fatal("a confirmed answer was fetched again")
	}
}

func TestNpcapRedirectsStayOnOfficialHosts(t *testing.T) {
	via := []*http.Request{httptest.NewRequest(http.MethodGet, npcapHomeURL, nil)}
	for address, allowed := range map[string]bool{
		"https://npcap.com/dist/npcap-1.90.exe": true,
		"https://api.github.com/x":              true,
		"http://npcap.com/dist/npcap-1.90.exe":  false,
		"https://npcap.com.evil.example/x":      false,
		"https://github.com/nmap/npcap":         false,
	} {
		err := checkNpcapRedirect(httptest.NewRequest(http.MethodGet, address, nil), via)
		if (err == nil) != allowed {
			t.Errorf("%s: %v", address, err)
		}
	}
}

func TestOnlyOfficialURLsReachTheBrowser(t *testing.T) {
	for address, valid := range map[string]bool{
		"https://npcap.com/#download":                        true,
		"https://npcap.com/dist/npcap-1.90.exe":              true,
		"https://npcap.com/dist/npcap-1.90.1.exe":            true,
		"http://npcap.com/dist/npcap-1.90.exe":               false,
		"https://npcap.com.evil.example/dist/npcap-1.90.exe": false,
		"https://npcap.com/dist/npcap-sdk-1.17.zip":          false,
		"https://npcap.com/dist/npcap-1.90.zip":              false,
		"https://npcap.com/dist/../evil.exe":                 false,
		"https://user@npcap.com/dist/npcap-1.90.exe":         false,
		"https://npcap.com/dist/npcap-1.90.exe?x=1":          false,
		"file:///C:/Windows/System32/calc.exe":               false,
		"":                                                   false,
	} {
		if validNpcapURL(address) != valid {
			t.Errorf("%q: valid should be %t", address, valid)
		}
	}
}

func TestOpenNpcapDownloadOpensOnlyTheResolvedAddress(t *testing.T) {
	fixture := npcapFixture{t: t, home: func() (int, string) { return http.StatusOK, officialPage }, release: notFoundText, installer: installerOK}
	var opened []string
	bridge := &Bridge{npcap: newNpcapDownloadResolver(&fixture), openBrowser: func(address string) error {
		opened = append(opened, address)
		return nil
	}}
	got, err := bridge.OpenNpcapDownload(context.Background())
	if err != nil || !got.Direct || len(opened) != 1 || opened[0] != "https://npcap.com/dist/npcap-1.90.exe" {
		t.Fatalf("got %+v err %v opened %v", got, err, opened)
	}

	offline := &Bridge{npcap: newNpcapDownloadResolver(&offlineFixture{}), openBrowser: func(address string) error {
		opened = append(opened, address)
		return nil
	}}
	got, err = offline.OpenNpcapDownload(context.Background())
	if err != nil || got.Direct || got.Version != "" || opened[1] != npcapDownloadPage {
		t.Fatalf("fallback: %+v err %v opened %v", got, err, opened)
	}
}

func TestGameAssetRouteSeparatesMissingImagesFromLoadFailures(t *testing.T) {
	frontend := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(response, "frontend") })
	status := func(handler http.Handler, path string) int {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		return recorder.Code
	}
	damaged := withGameAssets(frontend, assets.NewPackFS("damaged"))
	if got := status(damaged, "/game-assets/buff/1"); got != http.StatusInternalServerError {
		t.Fatalf("damaged pack: %d", got)
	}
	if got := status(damaged, "/game-assets/buff/0"); got != http.StatusNotFound {
		t.Fatalf("invalid ID on a damaged pack: %d", got)
	}
	if got := status(withGameAssets(frontend, nil), "/game-assets/buff/1"); got != http.StatusNotFound {
		t.Fatalf("no source: %d", got)
	}
}

func TestGameAssetSourceSelection(t *testing.T) {
	if gameAssetSource("", "") != nil {
		t.Fatal("no root and no pack must mean no source")
	}
	if _, isPack := gameAssetSource("", "pack").(*assets.PackFS); !isPack {
		t.Fatal("an embedded pack was not used")
	}
	if _, isPack := gameAssetSource("root", "pack").(*assets.PackFS); isPack {
		t.Fatal("an explicit root must win over the pack")
	}
	// The route gives one answer whichever source holds the image.
	tree := fstest.MapFS{"game/skills/by-config/7/icon.webp": &fstest.MapFile{Data: []byte("config")}}
	recorder := httptest.NewRecorder()
	withGameAssets(http.NotFoundHandler(), tree).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/game-assets/skill-config/7", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "config" {
		t.Fatalf("skill-config: %d %q", recorder.Code, recorder.Body.String())
	}
}

// The simulation flags are Development flags (tests/devdesktop); the Public command line refuses them.
func TestPublicOptionsRefuseEnvironmentSimulation(t *testing.T) {
	if _, err := ParseOptions([]string{"-simulate-capture-environment", "npcap_missing,no_ipv4", "-simulate-npcap-offline", "-record-browser-url", "out.txt"}, io.Discard); err == nil {
		t.Fatal("Public accepted simulation flags")
	}
	defaults, err := ParseOptions(nil, io.Discard)
	if err != nil || defaults.EnvironmentProbe != nil || defaults.NpcapTransport != nil || defaults.OpenBrowser != nil {
		t.Fatalf("defaults must use the real system: %+v %v", defaults, err)
	}
}

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Official Npcap sources. npcap.com publishes the Windows installer; GitHub Releases carries version information and
// source archives only, so an installer URL is never taken from a release.
const (
	npcapHomeURL       = "https://npcap.com/"
	npcapDownloadPage  = "https://npcap.com/#download"
	npcapInstallerBase = "https://npcap.com/dist/"
	npcapReleaseAPI    = "https://api.github.com/repos/nmap/npcap/releases/latest"
)

const (
	npcapRequestLimit  = 8 * time.Second
	npcapResponseLimit = 2 << 20
	// A smaller answer is an error page or a stub, not the installer. The installer has been over 1 MB for years.
	npcapInstallerMinBytes = 256 << 10
	npcapResolvedLifetime  = time.Hour
	npcapFailedLifetime    = 30 * time.Second
)

var (
	npcapInstallerLink = regexp.MustCompile(`href=["'](?:https://npcap\.com/)?dist/npcap-([0-9]+\.[0-9]+(?:\.[0-9]+)?)\.exe["']`)
	npcapReleaseTag    = regexp.MustCompile(`^v?([0-9]+\.[0-9]+(?:\.[0-9]+)?)$`)
	npcapInstallerPath = regexp.MustCompile(`^/dist/npcap-[0-9]+\.[0-9]+(?:\.[0-9]+)?\.exe$`)
)

// NpcapDownload is what the UI offers. Direct is true only when the installer URL was confirmed against npcap.com;
// otherwise URL is the official download page and Version is empty, never a guess.
type NpcapDownload struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	Direct  bool   `json:"direct"`
}

// npcapDownloadResolver finds the latest official installer and caches the answer. It contacts only the two official
// hosts, and only after the UI found that Npcap needs installing.
type npcapDownloadResolver struct {
	client *http.Client
	now    func() time.Time

	mu      sync.Mutex
	cached  NpcapDownload
	expires time.Time
}

func newNpcapDownloadResolver(transport http.RoundTripper) *npcapDownloadResolver {
	return &npcapDownloadResolver{
		client: &http.Client{Transport: transport, Timeout: npcapRequestLimit, CheckRedirect: checkNpcapRedirect},
		now:    time.Now,
	}
}

// checkNpcapRedirect stops a redirect from leaving the official hosts or dropping HTTPS.
func checkNpcapRedirect(request *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("too many redirects")
	}
	if request.URL.Scheme != "https" || (request.URL.Hostname() != "npcap.com" && request.URL.Hostname() != "api.github.com") {
		return errors.New("redirect left the official Npcap hosts")
	}
	return nil
}

func npcapFallback() NpcapDownload {
	return NpcapDownload{URL: npcapDownloadPage}
}

// resolve returns the cached answer or asks the official sources. Callers wait for one another, so concurrent requests
// make one set of network calls.
func (r *npcapDownloadResolver) resolve(ctx context.Context) NpcapDownload {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.expires.IsZero() && r.now().Before(r.expires) {
		return r.cached
	}
	result := r.lookup(ctx)
	lifetime := npcapFailedLifetime
	if result.Direct {
		lifetime = npcapResolvedLifetime
	}
	r.cached, r.expires = result, r.now().Add(lifetime)
	return result
}

func (r *npcapDownloadResolver) lookup(ctx context.Context) NpcapDownload {
	version, err := r.versionFromDownloadPage(ctx)
	if err != nil {
		// The page format may change without the installer location changing. A release tag only names a candidate,
		// which installerConfirmed must still find on npcap.com.
		if version, err = r.versionFromRelease(ctx); err != nil {
			return npcapFallback()
		}
	}
	installer := npcapInstallerBase + "npcap-" + version + ".exe"
	if !r.installerConfirmed(ctx, installer) {
		return npcapFallback()
	}
	return NpcapDownload{Version: version, URL: installer, Direct: true}
}

func (r *npcapDownloadResolver) get(ctx context.Context, address string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "WWlogs")
	response, err := r.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", response.StatusCode)
	}
	return io.ReadAll(io.LimitReader(response.Body, npcapResponseLimit))
}

// versionFromDownloadPage reads the installer link npcap.com itself offers. The page also links the SDK, debug symbols
// and a zip, which the pattern excludes.
func (r *npcapDownloadResolver) versionFromDownloadPage(ctx context.Context) (string, error) {
	body, err := r.get(ctx, npcapHomeURL)
	if err != nil {
		return "", err
	}
	best := ""
	for _, match := range npcapInstallerLink.FindAllSubmatch(body, -1) {
		if candidate := string(match[1]); best == "" || npcapVersionNewer(candidate, best) {
			best = candidate
		}
	}
	if best == "" {
		return "", errors.New("no installer link on the download page")
	}
	return best, nil
}

// versionFromRelease reads the latest published release. Drafts, prereleases and unexpected tags are refused.
func (r *npcapDownloadResolver) versionFromRelease(ctx context.Context) (string, error) {
	body, err := r.get(ctx, npcapReleaseAPI)
	if err != nil {
		return "", err
	}
	var release struct {
		TagName    string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return "", err
	}
	match := npcapReleaseTag.FindStringSubmatch(release.TagName)
	if match == nil || release.Draft || release.Prerelease {
		return "", errors.New("release is not a usable version")
	}
	return match[1], nil
}

// installerConfirmed asks npcap.com whether the file exists and looks like an installer, without downloading it.
func (r *npcapDownloadResolver) installerConfirmed(ctx context.Context, address string) bool {
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, address, nil)
	if err != nil {
		return false
	}
	request.Header.Set("User-Agent", "WWlogs")
	response, err := r.client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	return response.StatusCode == http.StatusOK && response.ContentLength >= npcapInstallerMinBytes &&
		!strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/") &&
		validNpcapURL(response.Request.URL.String())
}

// validNpcapURL is the last check before a URL reaches the browser: the official download page, or an installer path
// on npcap.com over HTTPS.
func validNpcapURL(address string) bool {
	if address == npcapDownloadPage {
		return true
	}
	parsed, err := url.Parse(address)
	return err == nil && parsed.Scheme == "https" && parsed.Host == "npcap.com" && parsed.User == nil &&
		parsed.RawQuery == "" && parsed.Fragment == "" && npcapInstallerPath.MatchString(parsed.Path)
}

// npcapVersionNewer compares dotted versions numerically, so 1.100 is newer than 1.90.
func npcapVersionNewer(a, b string) bool {
	left, right := strings.Split(a, "."), strings.Split(b, ".")
	for index := 0; index < len(left) || index < len(right); index++ {
		var x, y int
		if index < len(left) {
			x, _ = strconv.Atoi(left[index])
		}
		if index < len(right) {
			y, _ = strconv.Atoi(right[index])
		}
		if x != y {
			return x > y
		}
	}
	return false
}

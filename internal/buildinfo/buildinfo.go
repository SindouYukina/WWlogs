// Package buildinfo identifies the executable that is actually running: product version, official Build Number or
// unofficial status, source commit, build profile, and the SHA-256 of the executable file itself.
//
// Version, Build Number and commit are injected at link time by the build orchestrator (scripts/embed-key.go), which
// alone allocates official numbers. The profile is not injected: it is a constant chosen by the wwdev build tag, so it
// always matches what was compiled in. This package is the one identity source for the console, the desktop host and
// the frontend.
package buildinfo

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"regexp"
	"runtime/debug"
	"strconv"
	"sync"
)

// Profile names a compile-time build profile.
type Profile string

const (
	// ProfileDevelopment keeps raw diagnostic outputs for internal research.
	ProfileDevelopment Profile = "development"
	// ProfilePublic is the default profile and has no raw diagnostic outputs.
	ProfilePublic Profile = "public"
)

// Unavailable marks an identity field that could not be determined. It is never replaced by a guess.
const Unavailable = "UNAVAILABLE"

// Values set with -ldflags "-X wwlogs/internal/buildinfo.injectedVersion=..." and so on.
var (
	injectedVersion     string
	injectedBuildNumber string
	injectedCommit      string
	injectedDirty       string
)

var (
	versionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$`)
	commitPattern  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	numberPattern  = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)
)

// Identity is the build identity that needs no file access.
type Identity struct {
	// Version is the product version, for example v0.3.0, or Unavailable.
	Version string
	// BuildNumber is the official lifetime Build Number; zero marks an unofficial build.
	BuildNumber uint64
	// Commit is the full 40-character Git commit SHA, or Unavailable.
	Commit string
	// Dirty is "true" or "false" when the working tree state is known, otherwise "unknown".
	Dirty string
	// Profile is the compiled build profile.
	Profile Profile
}

// Record is the build identity as recorded in diagnostic metadata, with the executable hash.
type Record struct {
	Version          string `json:"version"`
	BuildNumber      uint64 `json:"buildNumber,omitempty"`
	Official         bool   `json:"official"`
	Commit           string `json:"commit"`
	Dirty            string `json:"dirty"`
	Profile          string `json:"profile"`
	ExecutableSHA256 string `json:"executableSha256"`
}

// CurrentRecord returns the build identity together with the hash of the running executable.
func CurrentRecord() Record {
	identity := Current()
	return Record{
		Version: identity.Version, BuildNumber: identity.BuildNumber, Official: identity.Official(),
		Commit: identity.Commit, Dirty: identity.Dirty,
		Profile: string(identity.Profile), ExecutableSHA256: ExecutableSHA256(),
	}
}

// Current returns the build identity. It performs no file or network access.
func Current() Identity {
	return resolve(injectedVersion, injectedBuildNumber, injectedCommit, injectedDirty, readVCS)
}

func resolve(version, buildNumber, commit, dirty string, vcs func() (string, string)) Identity {
	identity := Identity{Version: version, Commit: commit, Dirty: dirty, Profile: profile}
	if numberPattern.MatchString(buildNumber) {
		identity.BuildNumber, _ = strconv.ParseUint(buildNumber, 10, 64)
	}
	if !versionPattern.MatchString(identity.Version) {
		identity.Version = Unavailable
	}
	if !commitPattern.MatchString(identity.Commit) {
		identity.Commit, identity.Dirty = Unavailable, ""
		if revision, modified := vcs(); commitPattern.MatchString(revision) {
			identity.Commit, identity.Dirty = revision, modified
		}
	}
	if identity.Dirty != "true" && identity.Dirty != "false" {
		identity.Dirty = "unknown"
	}
	return identity
}

// readVCS reads the revision stamped by the Go toolchain when the build did not inject one.
func readVCS() (revision, modified string) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", ""
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value
		}
	}
	return revision, modified
}

// Official reports whether the build carries an official Build Number.
func (i Identity) Official() bool { return i.BuildNumber > 0 }

// ProductVersion is the product version as MAJOR.MINOR.PATCH (with any pre-release suffix), without the v of the version
// tag, or empty when the version is Unavailable.
func (i Identity) ProductVersion() string {
	if i.Version == Unavailable || len(i.Version) < 2 || i.Version[0] != 'v' {
		return ""
	}
	return i.Version[1:]
}

// BuildLabel is the Build Number in decimal, or "unofficial".
func (i Identity) BuildLabel() string {
	if !i.Official() {
		return "unofficial"
	}
	return strconv.FormatUint(i.BuildNumber, 10)
}

// String formats the identity on one line without the executable hash.
func (i Identity) String() string {
	commit := i.Commit
	if commit != Unavailable {
		commit = commit[:12]
		if i.Dirty == "true" {
			commit += "-dirty"
		}
	}
	return i.Version + " build " + i.BuildLabel() + " " + string(i.Profile) + " " + commit
}

var (
	executableOnce sync.Once
	executableHash string
)

// ExecutableSHA256 returns the lowercase SHA-256 of the running executable file, or Unavailable.
// The file is read at most once, on the first call; nothing is hashed unless a caller asks.
func ExecutableSHA256() string {
	executableOnce.Do(func() {
		executableHash = hashFile(executablePath)
	})
	return executableHash
}

func executablePath() (string, error) { return os.Executable() }

func hashFile(locate func() (string, error)) string {
	path, err := locate()
	if err != nil {
		return Unavailable
	}
	file, err := os.Open(path)
	if err != nil {
		return Unavailable
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return Unavailable
	}
	return hex.EncodeToString(digest.Sum(nil))
}

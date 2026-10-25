// Package buildinfo identifies the executable that is actually running: product version, source
// commit, build profile, and the SHA-256 of the executable file itself.
//
// Version and commit are injected at link time by the build script. The profile is not injected:
// it is a constant chosen by the wwdev build tag, so it always matches what was compiled in.
package buildinfo

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"regexp"
	"runtime/debug"
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
	injectedVersion string
	injectedCommit  string
	injectedDirty   string
)

var (
	versionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$`)
	commitPattern  = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// Identity is the build identity that needs no file access.
type Identity struct {
	// Version is the product version, for example v0.3.0, or Unavailable.
	Version string
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
	Commit           string `json:"commit"`
	Dirty            string `json:"dirty"`
	Profile          string `json:"profile"`
	ExecutableSHA256 string `json:"executableSha256"`
}

// CurrentRecord returns the build identity together with the hash of the running executable.
func CurrentRecord() Record {
	identity := Current()
	return Record{
		Version: identity.Version, Commit: identity.Commit, Dirty: identity.Dirty,
		Profile: string(identity.Profile), ExecutableSHA256: ExecutableSHA256(),
	}
}

// Current returns the build identity. It performs no file or network access.
func Current() Identity {
	return resolve(injectedVersion, injectedCommit, injectedDirty, readVCS)
}

func resolve(version, commit, dirty string, vcs func() (string, string)) Identity {
	identity := Identity{Version: version, Commit: commit, Dirty: dirty, Profile: profile}
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

// String formats the identity on one line without the executable hash.
func (i Identity) String() string {
	commit := i.Commit
	if commit != Unavailable {
		commit = commit[:12]
		if i.Dirty == "true" {
			commit += "-dirty"
		}
	}
	return i.Version + " " + string(i.Profile) + " " + commit
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

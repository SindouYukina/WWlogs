package assets_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"wwlogs/internal/assets"
)

// keyPattern turns a manifest path back into the Key that must resolve to it.
var keyPattern = regexp.MustCompile(`^game/(?:characters/(\d+)/(avatar|portrait)|skills/(by-config/)?(\d+)/icon|buffs/(\d+)/icon|echo-skills/(\d+)/icon)\.webp$`)

func keyFor(t *testing.T, path string) assets.Key {
	t.Helper()
	match := keyPattern.FindStringSubmatch(path)
	if match == nil {
		t.Fatalf("no key for %s", path)
	}
	parse := func(text string) uint64 {
		value, err := strconv.ParseUint(text, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	switch {
	case match[2] == "avatar":
		return assets.Key{Kind: assets.CharacterAvatar, ID: parse(match[1])}
	case match[2] == "portrait":
		return assets.Key{Kind: assets.CharacterPortrait, ID: parse(match[1])}
	case match[3] != "":
		return assets.Key{Kind: assets.SkillConfigIcon, ID: parse(match[4])}
	case match[4] != "":
		return assets.Key{Kind: assets.SkillIcon, ID: parse(match[4])}
	case match[6] != "":
		return assets.Key{Kind: assets.EchoSkillIcon, ID: parse(match[6])}
	default:
		return assets.Key{Kind: assets.BuffIcon, ID: parse(match[5])}
	}
}

func readResolved(resolver assets.Resolver, key assets.Key) ([]byte, error) {
	file, err := resolver.Open(key)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var out bytes.Buffer
	if _, err := out.ReadFrom(file); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Every test pass checks the tracked tree, which is what makes "a clone can build the images" a tested statement.
func TestRepositoryAssetsMatchTheirManifest(t *testing.T) {
	root := filepath.Join("..", "..", "assets")
	data, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := assets.ParseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Client == "" || manifest.Resource == "" {
		t.Fatal("the manifest does not record its game version")
	}
	var pack bytes.Buffer
	stats, err := assets.WritePack(&pack, os.DirFS(root), manifest)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Files != len(manifest.Entries) || stats.Distinct == 0 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestPackResolverMatchesTrackedAssetsForEveryID(t *testing.T) {
	root := filepath.Join("..", "..", "assets")
	data, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := assets.ParseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	var pack bytes.Buffer
	if _, err := assets.WritePack(&pack, os.DirFS(root), manifest); err != nil {
		t.Fatal(err)
	}
	fromEmbedded := assets.NewResolver(assets.NewPackFS(pack.String()))
	fromDirectory := assets.NewResolver(os.DirFS(root))
	for _, entry := range manifest.Entries {
		key := keyFor(t, entry.Path)
		packed, err := readResolved(fromEmbedded, key)
		if err != nil {
			t.Fatalf("embedded %s: %v", entry.Path, err)
		}
		listed, err := readResolved(fromDirectory, key)
		if err != nil || !bytes.Equal(packed, listed) {
			t.Fatalf("directory and embedded lookups differ for %s: %v", entry.Path, err)
		}
	}
	t.Logf("resolved all %d manifest image IDs with byte parity", len(manifest.Entries))
}

// The artifact test reads the pack extracted from a built executable (work/ui-perf/extract_pack.py). It proves that every
// image of a built artifact resolves through the shared Resolver, by ID, to the bytes the manifest lists.
func TestBuiltArtifactPackMatchesTheManifest(t *testing.T) {
	packPath := os.Getenv("WWLOGS_ASSET_PACK")
	if packPath == "" {
		t.Skip("WWLOGS_ASSET_PACK is not set")
	}
	root := filepath.Join("..", "..", "assets")
	data, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := assets.ParseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	pack, err := os.ReadFile(packPath)
	if err != nil {
		t.Fatal(err)
	}
	fromArtifact := assets.NewResolver(assets.NewPackFS(string(pack)))
	fromDirectory := assets.NewResolver(os.DirFS(root))
	for _, entry := range manifest.Entries {
		key := keyFor(t, entry.Path)
		got, err := readResolved(fromArtifact, key)
		if err != nil {
			t.Fatalf("%s: %v", entry.Path, err)
		}
		digest := sha256.Sum256(got)
		if hex.EncodeToString(digest[:]) != entry.SHA256 {
			t.Fatalf("%s: the artifact holds different bytes", entry.Path)
		}
		fromDisk, err := readResolved(fromDirectory, key)
		if err != nil || !bytes.Equal(got, fromDisk) {
			t.Fatalf("%s: Development and Public differ: %v", entry.Path, err)
		}
	}
	// IDs the game has no image for stay missing in the artifact, the ten multi-Cue BuffIds among them.
	if _, err := fromArtifact.Open(assets.Key{Kind: assets.BuffIcon, ID: 1}); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("an unlisted ID reported %v", err)
	}
	t.Logf("resolved %d images from the artifact, %d bytes of pack", len(manifest.Entries), len(pack))
}

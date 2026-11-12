package assets_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"wwlogs/internal/assets"
)

func webp(body string) []byte { return []byte("RIFF\x00\x00\x00\x00WEBP" + body) }

func manifestFor(t *testing.T, source fs.FS) assets.Manifest {
	t.Helper()
	manifest, skipped, err := assets.ScanManifest(source, "client", "resource")
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped %v", skipped)
	}
	return manifest
}

func sampleTree() fstest.MapFS {
	return fstest.MapFS{
		"game/characters/1205/avatar.webp":    {Data: webp("avatar")},
		"game/characters/1205/portrait.webp":  {Data: webp("portrait")},
		"game/characters/1506/portrait.webp":  {Data: webp("portrait")},
		"game/skills/700/icon.webp":           {Data: webp("shared icon")},
		"game/skills/701/icon.webp":           {Data: webp("shared icon")},
		"game/skills/by-config/700/icon.webp": {Data: webp("config icon")},
		"game/echo-skills/700/icon.webp":      {Data: webp("echo icon")},
		"game/buffs/10010000/icon.webp":       {Data: webp("buff")},
	}
}

func TestPackResolvesLikeADirectoryAndStoresEachContentOnce(t *testing.T) {
	tree := sampleTree()
	manifest := manifestFor(t, tree)
	var pack bytes.Buffer
	stats, err := assets.WritePack(&pack, tree, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Files != 8 || stats.Distinct != 6 {
		t.Fatalf("stats = %+v", stats)
	}
	fromPack := assets.NewResolver(assets.NewPackFS(pack.String()))
	fromDirectory := assets.NewResolver(tree)
	for _, key := range []assets.Key{
		{Kind: assets.CharacterAvatar, ID: 1205}, {Kind: assets.CharacterPortrait, ID: 1205}, {Kind: assets.CharacterPortrait, ID: 1506},
		{Kind: assets.SkillIcon, ID: 700}, {Kind: assets.SkillIcon, ID: 701}, {Kind: assets.SkillConfigIcon, ID: 700}, {Kind: assets.EchoSkillIcon, ID: 700}, {Kind: assets.BuffIcon, ID: 10010000},
	} {
		want := readKey(t, fromDirectory, key)
		if got := readKey(t, fromPack, key); !bytes.Equal(got, want) {
			t.Fatalf("%+v: pack gave %q, directory gave %q", key, got, want)
		}
	}
	// Equal IDs in different namespaces keep their own images.
	if bytes.Equal(readKey(t, fromPack, assets.Key{Kind: assets.SkillIcon, ID: 700}), readKey(t, fromPack, assets.Key{Kind: assets.SkillConfigIcon, ID: 700})) {
		t.Fatal("skill namespaces collapsed")
	}
	if bytes.Equal(readKey(t, fromPack, assets.Key{Kind: assets.SkillIcon, ID: 700}), readKey(t, fromPack, assets.Key{Kind: assets.EchoSkillIcon, ID: 700})) {
		t.Fatal("echo skill namespace collapsed into skills")
	}
	for _, key := range []assets.Key{{Kind: assets.BuffIcon, ID: 999}, {Kind: assets.CharacterAvatar, ID: 1506}, {Kind: assets.SkillIcon, ID: 702}, {Kind: assets.EchoSkillIcon, ID: 701}} {
		if _, err := fromPack.Open(key); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%+v: an ID without an image reported %v", key, err)
		}
	}
	if _, err := fromPack.Open(assets.Key{Kind: assets.BuffIcon}); !errors.Is(err, assets.ErrInvalidKey) {
		t.Fatalf("invalid key reported %v", err)
	}
}

func readKey(t *testing.T, resolver assets.Resolver, key assets.Key) []byte {
	t.Helper()
	file, err := resolver.Open(key)
	if err != nil {
		t.Fatalf("%+v: %v", key, err)
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("%+v: read: %v", key, err)
	}
	return data
}

func TestPackIsDeterministic(t *testing.T) {
	tree := sampleTree()
	manifest := manifestFor(t, tree)
	var first, second bytes.Buffer
	if _, err := assets.WritePack(&first, tree, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := assets.WritePack(&second, tree, manifest); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("the same input produced different packs")
	}
}

func TestPackRefusesAnythingThatDiffersFromTheManifest(t *testing.T) {
	manifest := manifestFor(t, sampleTree())
	changed := sampleTree()
	changed["game/buffs/10010000/icon.webp"] = &fstest.MapFile{Data: webp("swapped")}
	if _, err := assets.WritePack(io.Discard, changed, manifest); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("changed image: %v", err)
	}
	missing := sampleTree()
	delete(missing, "game/skills/700/icon.webp")
	if _, err := assets.WritePack(io.Discard, missing, manifest); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing image: %v", err)
	}
	notWebP := fstest.MapFS{"game/buffs/1/icon.webp": {Data: []byte("not an image")}}
	digest := sha256.Sum256([]byte("not an image"))
	forged := assets.Manifest{Schema: assets.ManifestSchema, Entries: []assets.ManifestEntry{{Path: "game/buffs/1/icon.webp", SHA256: hex.EncodeToString(digest[:]), Size: 12}}}
	if _, err := assets.WritePack(io.Discard, notWebP, forged); err == nil || !strings.Contains(err.Error(), "not a WebP") {
		t.Fatalf("non-image: %v", err)
	}
}

func TestADamagedPackIsNotReportedAsAMissingImage(t *testing.T) {
	_, err := assets.NewResolver(assets.NewPackFS("damaged")).Open(assets.Key{Kind: assets.BuffIcon, ID: 1})
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("damaged pack reported %v", err)
	}
}

func TestHasAnswersFromTheSamePathAsOpenWithoutReadingContent(t *testing.T) {
	tree := sampleTree()
	var pack bytes.Buffer
	if _, err := assets.WritePack(&pack, tree, manifestFor(t, tree)); err != nil {
		t.Fatal(err)
	}
	packFS := assets.NewPackFS(pack.String())
	if _, ok := any(packFS).(fs.StatFS); !ok {
		t.Fatal("the pack does not answer Stat itself")
	}
	for name, resolver := range map[string]assets.Resolver{"pack": assets.NewResolver(packFS), "directory": assets.NewResolver(tree)} {
		for _, test := range []struct {
			key  assets.Key
			want bool
		}{
			{assets.Key{Kind: assets.BuffIcon, ID: 10010000}, true},
			{assets.Key{Kind: assets.SkillIcon, ID: 701}, true},
			// Another ID's image never stands in, even one a neighbour or another namespace has.
			{assets.Key{Kind: assets.BuffIcon, ID: 10010001}, false},
			{assets.Key{Kind: assets.BuffIcon, ID: 700}, false},
			{assets.Key{Kind: assets.BuffIcon}, false},
			{assets.Key{Kind: assets.SkinAvatar, ID: 1205}, false},
		} {
			found, err := resolver.Has(test.key)
			if err != nil || found != test.want {
				t.Fatalf("%s %+v: got %v (%v), want %v", name, test.key, found, err, test.want)
			}
			file, openErr := resolver.Open(test.key)
			if file != nil {
				file.Close()
			}
			if (openErr == nil) != test.want {
				t.Fatalf("%s %+v: Has and Open disagree (%v)", name, test.key, openErr)
			}
		}
	}
	if found, err := assets.NewResolver(nil).Has(assets.Key{Kind: assets.BuffIcon, ID: 10010000}); found || err != nil {
		t.Fatalf("no source: %v %v", found, err)
	}
	// A damaged pack cannot say an image is missing.
	if found, err := assets.NewResolver(assets.NewPackFS("damaged")).Has(assets.Key{Kind: assets.BuffIcon, ID: 1}); found || err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("damaged pack: %v %v", found, err)
	}
}

func TestManifestRoundTripAndValidation(t *testing.T) {
	manifest := manifestFor(t, sampleTree())
	parsed, err := assets.ParseManifest(manifest.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Entries) != len(manifest.Entries) || parsed.Client != "client" || parsed.Resource != "resource" {
		t.Fatalf("round trip: %+v", parsed)
	}
	crlf := bytes.ReplaceAll(manifest.Encode(), []byte("\n"), []byte("\r\n"))
	if _, err := assets.ParseManifest(crlf); err != nil {
		t.Fatalf("a CRLF checkout must parse: %v", err)
	}
	valid := manifest.Entries[0]
	for name, entries := range map[string][]assets.ManifestEntry{
		"no entries":   nil,
		"path escape":  {{Path: "../secret.webp", SHA256: valid.SHA256, Size: 1}},
		"not a kind":   {{Path: "game/characters/1/variants/2/avatar.webp", SHA256: valid.SHA256, Size: 1}},
		"zero id":      {{Path: "game/buffs/0/icon.webp", SHA256: valid.SHA256, Size: 1}},
		"not webp":     {{Path: "game/buffs/1/icon.png", SHA256: valid.SHA256, Size: 1}},
		"bad hash":     {{Path: "game/buffs/1/icon.webp", SHA256: "abc", Size: 1}},
		"no size":      {{Path: "game/buffs/1/icon.webp", SHA256: valid.SHA256}},
		"out of order": {{Path: "game/buffs/2/icon.webp", SHA256: valid.SHA256, Size: 1}, {Path: "game/buffs/1/icon.webp", SHA256: valid.SHA256, Size: 1}},
		"duplicate":    {{Path: "game/buffs/1/icon.webp", SHA256: valid.SHA256, Size: 1}, {Path: "game/buffs/1/icon.webp", SHA256: valid.SHA256, Size: 1}},
	} {
		broken := assets.Manifest{Schema: assets.ManifestSchema, Entries: entries}
		if _, err := assets.ParseManifest(broken.Encode()); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := assets.ParseManifest([]byte(`{"schema":2,"entries":[]}`)); err == nil {
		t.Error("an unknown schema was accepted")
	}
}

func TestScanLeavesOutFilesNoKeyCanReach(t *testing.T) {
	tree := sampleTree()
	tree["game/characters/1205/variants/9/avatar.webp"] = &fstest.MapFile{Data: webp("variant")}
	tree["game/buffs/10010000/notes.txt"] = &fstest.MapFile{Data: []byte("notes")}
	manifest, skipped, err := assets.ScanManifest(tree, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Entries) != 8 || len(skipped) != 2 {
		t.Fatalf("entries=%d skipped=%v", len(manifest.Entries), skipped)
	}
}

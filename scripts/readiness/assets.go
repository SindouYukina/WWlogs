package readiness

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// The assets domain checks the image pack against itself, against the game version and, through a research check,
// against what the current client references. Which images each domain needs (role art, skill and buff icons) is
// checked with the Master in characters and effects.

type manifestEntry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type manifestFile struct {
	Schema   int             `json:"schema"`
	Client   string          `json:"client"`
	Resource string          `json:"resource"`
	Entries  []manifestEntry `json:"entries"`
	paths    map[string]bool
}

func (m *manifestFile) has(path string) bool { return m != nil && m.paths[path] }

func readManifest(path string) (*manifestFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var manifest manifestFile
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	manifest.paths = make(map[string]bool, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		manifest.paths[entry.Path] = true
	}
	return &manifest, nil
}

func (g *gate) assetChecks() {
	integrity := g.check("assets.manifest-integrity", DomainAssets, "Every manifest image exists with its recorded SHA-256 and size; no unlisted image", KindStatic, true)
	version := g.check("assets.manifest-version", DomainAssets, "The image pack belongs to the decoded game version", KindStatic, true)
	references := g.researchCheck("assets.client-references", DomainAssets, "The image pack matches what the current client references (private research check)")

	manifest, err := readManifest(g.in.ManifestPath)
	if err != nil {
		integrity.fail("the image manifest cannot be read: %v", err)
		version.unverified("the image manifest cannot be read")
		references.unverified("the image manifest cannot be read")
		return
	}
	g.manifest = manifest
	g.report.AssetClient, g.report.AssetResource = manifest.Client, manifest.Resource

	verified := 0
	for _, entry := range manifest.Entries {
		path := filepath.Join(g.in.AssetRoot, filepath.FromSlash(entry.Path))
		digest, size, err := imageSHA256(path)
		if err != nil {
			integrity.fail("%s is listed but cannot be read: %v", entry.Path, err)
			continue
		}
		if digest != entry.SHA256 || size != entry.Size {
			integrity.fail("%s does not match its manifest SHA-256 or size", entry.Path)
			continue
		}
		verified++
	}
	gameDir := filepath.Join(g.in.AssetRoot, "game")
	_ = filepath.WalkDir(gameDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(g.in.AssetRoot, path)
		if err == nil && !manifest.has(filepath.ToSlash(rel)) {
			integrity.fail("%s is in the pack directory but not in the manifest", filepath.ToSlash(rel))
		}
		return nil
	})
	integrity.Coverage = &Coverage{Passed: verified, Total: len(manifest.Entries), Denominator: "manifest images hashed"}

	switch {
	case g.source == nil || g.source.gameVersion == "":
		version.unverified("the decoded game version is unknown")
	case majorMinor(manifest.Resource) != g.source.gameVersion:
		version.fail("the image pack is resource %q (client %q); the decoder reads game version %s: refresh the pack (PROCEDURE.md S7)", manifest.Resource, manifest.Client, g.source.gameVersion)
	default:
		version.Evidence = append(version.Evidence, fmt.Sprintf("pack %s resource %s, game version %s", manifest.Client, manifest.Resource, g.source.gameVersion))
	}

	g.research(references)
}

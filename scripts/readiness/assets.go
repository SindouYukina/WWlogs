package readiness

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The assets domain checks the image pack against itself, against the game version and, with the research input,
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

// clientReferences is the research input of PROCEDURE.md 5.2.
type clientReferences struct {
	Client     string   `json:"client"`
	Resource   string   `json:"resource"`
	Paths      []string `json:"paths"`
	Exclusions []struct {
		Path   string `json:"path"`
		Source string `json:"source"`
		Reason string `json:"reason"`
	} `json:"exclusions"`
}

func (g *gate) assetChecks() {
	integrity := g.check("assets.manifest-integrity", DomainAssets, "Every manifest image exists with its recorded SHA-256 and size; no unlisted image", KindStatic, true)
	version := g.check("assets.manifest-version", DomainAssets, "The image pack belongs to the decoded game version", KindStatic, true)
	references := g.check("assets.client-references", DomainAssets, "The pack holds exactly the images the current client references (explicit exclusions apart)", KindResearch, true)

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

	g.clientReferenceCheck(references, manifest)
}

func (g *gate) clientReferenceCheck(c *Check, manifest *manifestFile) {
	if g.in.ClientReferences == "" {
		c.unverified("no client reference inventory was supplied (research input clientReferences; PROCEDURE.md S7)")
		return
	}
	data, err := os.ReadFile(g.in.ClientReferences)
	if err != nil {
		c.unverified(fmt.Sprintf("the client reference inventory cannot be read: %v", err))
		return
	}
	var refs clientReferences
	if err := json.Unmarshal(data, &refs); err != nil {
		c.fail("the client reference inventory cannot be parsed: %v", err)
		return
	}
	if refs.Resource != manifest.Resource {
		c.fail("the inventory describes resource %q, the pack %q", refs.Resource, manifest.Resource)
	}
	referenced := map[string]bool{}
	present := 0
	for _, path := range refs.Paths {
		referenced[path] = true
		if manifest.has(path) {
			present++
		} else {
			c.fail("%s is referenced by the client but missing from the pack", path)
		}
	}
	var unreferenced []string
	for _, entry := range manifest.Entries {
		if !referenced[entry.Path] {
			unreferenced = append(unreferenced, entry.Path)
		}
	}
	sort.Strings(unreferenced)
	for _, path := range unreferenced {
		c.fail("%s is in the pack but no longer referenced by the client (Remove or Unresolved decision missing)", path)
	}
	reasons := map[string]int{}
	for _, exclusion := range refs.Exclusions {
		reason := exclusion.Reason
		if i := strings.Index(reason, ":"); i > 0 {
			reason = reason[:i]
		}
		reasons[reason]++
	}
	c.Coverage = &Coverage{Passed: present, Total: len(refs.Paths), Denominator: "client-referenced images the product requests"}
	c.Evidence = append(c.Evidence, fmt.Sprintf("inventory %s resource %s: %d paths, %d explicit exclusions %v", refs.Client, refs.Resource, len(refs.Paths), len(refs.Exclusions), reasons))
}

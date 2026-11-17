// Package architecture holds the architecture contract tests of WWlogs. They read the product source of both build
// profiles with go/build, go/ast and go/types and check the boundaries the shared core depends on: which files each
// profile compiles, how shared code reaches Development code, and where Development state may live. They need only the
// tracked source and the Go toolchain, never private files, and run with the ordinary product tests
// (scripts/test.ps1, scripts/verify.ps1).
//
// A static check proves only what the source text shows. Behavior that no static rule can see (a Development
// observer that changes core state through a core method, a diagnostic failure that stops processing) is covered by
// the behavioral tests named in each contract's comment, not by these checks.
package architecture

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
)

// modulePath is the module of the product packages.
const modulePath = "wwlogs"

// developmentTag selects the Development profile; a build without it is the Public profile.
const developmentTag = "wwdev"

type buildProfile struct {
	name string
	tags []string
}

var (
	publicProfile      = buildProfile{name: "Public"}
	developmentProfile = buildProfile{name: "Development", tags: []string{developmentTag}}
	bothProfiles       = []buildProfile{publicProfile, developmentProfile}
)

// analyzedPackages are type-checked from source: the product packages that contain profile-specific files.
// fileKinds checks every product package; a new package with profile-specific files must be added here, and
// TestProfileFilesAreAnalyzed fails until it is.
var analyzedPackages = []string{"internal/buildinfo", "internal/capture", "internal/desktop"}

// fileKind is the profile a product file belongs to, decided by its build constraint, never by its name.
type fileKind int

const (
	sharedFile      fileKind = iota // compiled by both profiles
	developmentFile                 // compiled only with the Development tag
	publicFile                      // compiled only without it
	excludedFile                    // compiled by neither profile on this platform (another GOOS or GOARCH)
)

func (k fileKind) String() string {
	return [...]string{"shared", "Development-only", "Public-only", "excluded"}[k]
}

// listedPackage is the part of the "go list -json" output the contracts read.
type listedPackage struct {
	ImportPath     string
	Dir            string
	Export         string
	GoFiles        []string
	CgoFiles       []string
	IgnoredGoFiles []string
	ImportMap      map[string]string
	Error          *struct{ Err string }
}

// repository is the checkout under test: its root and the committed test overlay, which supplies the embedded Master
// to internal/capture exactly as for every product test.
type repository struct {
	root        string
	overlayFile string
	overlay     map[string]string // slash path relative to root -> replacement file, "" for a removed file

	mu           sync.Mutex
	listings     map[string]map[string]*listedPackage // profile name -> import path -> package (with dependencies)
	packages     map[string][]*listedPackage          // profile name -> product packages without dependencies
	dependencies map[string]map[string]bool           // profile name -> packages the executables link
	fsets        map[string]*token.FileSet
	imports      map[string]types.Importer
}

var (
	repositoryOnce sync.Once
	repositoryRoot *repository
	repositoryErr  error
)

func loadRepository(t testing.TB) *repository {
	t.Helper()
	repositoryOnce.Do(func() {
		repositoryRoot, repositoryErr = openRepository()
	})
	if repositoryErr != nil {
		t.Fatal(repositoryErr)
	}
	return repositoryRoot
}

func openRepository() (*repository, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	for {
		if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil && bytes.HasPrefix(bytes.TrimSpace(data), []byte("module "+modulePath)) {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, errors.New("no go.mod of module " + modulePath + " above the test directory")
		}
		dir = parent
	}
	r := &repository{root: dir, overlayFile: filepath.Join(dir, "tests", "go-overlay.json"),
		listings: map[string]map[string]*listedPackage{}, packages: map[string][]*listedPackage{},
		dependencies: map[string]map[string]bool{}, fsets: map[string]*token.FileSet{}, imports: map[string]types.Importer{}}
	data, err := os.ReadFile(r.overlayFile)
	if err != nil {
		return nil, err
	}
	var overlay struct{ Replace map[string]string }
	if err := json.Unmarshal(data, &overlay); err != nil {
		return nil, fmt.Errorf("tests/go-overlay.json: %w", err)
	}
	r.overlay = map[string]string{}
	for from, to := range overlay.Replace {
		if to != "" {
			to = filepath.Join(dir, filepath.FromSlash(to))
		}
		r.overlay[filepath.ToSlash(from)] = to
	}
	return r, nil
}

// goCommand is the go command of the toolchain running the tests.
func goCommand() string {
	if command, err := exec.LookPath("go"); err == nil {
		return command
	}
	return filepath.Join(runtime.GOROOT(), "bin", "go")
}

// runGoList runs "go list" in the repository root for a profile with an overlay (the committed one unless a test gives
// another) and returns its standard output.
func (r *repository) runGoList(profile buildProfile, overlayFile string, flags []string, patterns ...string) ([]byte, error) {
	if overlayFile == "" {
		overlayFile = r.overlayFile
	}
	arguments := append([]string{"list", "-overlay=" + overlayFile}, flags...)
	if len(profile.tags) > 0 {
		arguments = append(arguments, "-tags", strings.Join(profile.tags, ","))
	}
	arguments = append(arguments, patterns...)
	command := exec.Command(goCommand(), arguments...)
	command.Dir = r.root
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("go %s: %w\n%s", strings.Join(arguments, " "), err, stderr.String())
	}
	return output, nil
}

// goList runs "go list -json" and decodes the listed packages.
func (r *repository) goList(profile buildProfile, overlayFile string, flags []string, patterns ...string) ([]*listedPackage, error) {
	output, err := r.runGoList(profile, overlayFile, append([]string{"-json"}, flags...), patterns...)
	if err != nil {
		return nil, err
	}
	var listed []*listedPackage
	for decoder := json.NewDecoder(bytes.NewReader(output)); ; {
		var pkg listedPackage
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		if pkg.Error != nil {
			return nil, fmt.Errorf("go list %s: %s", pkg.ImportPath, pkg.Error.Err)
		}
		listed = append(listed, &pkg)
	}
	return listed, nil
}

// productPackages lists every product package (cmd and internal) as one profile sees it.
func (r *repository) productPackages(t testing.TB, profile buildProfile) []*listedPackage {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if listed, ok := r.packages[profile.name]; ok {
		return listed
	}
	listed, err := r.goList(profile, "", nil, "./cmd/...", "./internal/...")
	if err != nil {
		t.Fatal(err)
	}
	r.packages[profile.name] = listed
	return listed
}

// executableDependencies is every package the product executables (./cmd/...) of a profile link, read with the given
// overlay file ("" for the committed overlay, whose result is kept for the run).
func (r *repository) executableDependencies(t testing.TB, profile buildProfile, overlayFile string) map[string]bool {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if cached, ok := r.dependencies[profile.name]; ok && overlayFile == "" {
		return cached
	}
	output, err := r.runGoList(profile, overlayFile, []string{"-deps", "-f", "{{.ImportPath}}"}, "./cmd/...")
	if err != nil {
		t.Fatal(err)
	}
	dependencies := map[string]bool{}
	for _, line := range strings.Split(string(output), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			dependencies[line] = true
		}
	}
	if overlayFile == "" {
		r.dependencies[profile.name] = dependencies
	}
	return dependencies
}

// exportListing lists the analyzed packages and all their dependencies with export data for one profile.
func (r *repository) exportListing(t testing.TB, profile buildProfile) map[string]*listedPackage {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if listing, ok := r.listings[profile.name]; ok {
		return listing
	}
	patterns := make([]string, len(analyzedPackages))
	for i, rel := range analyzedPackages {
		patterns[i] = "./" + rel
	}
	listed, err := r.goList(profile, "", []string{"-deps", "-export"}, patterns...)
	if err != nil {
		t.Fatal(err)
	}
	listing := map[string]*listedPackage{}
	for _, pkg := range listed {
		listing[pkg.ImportPath] = pkg
	}
	r.listings[profile.name] = listing
	fset := token.NewFileSet()
	r.fsets[profile.name] = fset
	r.imports[profile.name] = importer.ForCompiler(fset, "gc", func(importPath string) (io.ReadCloser, error) {
		pkg := listing[importPath]
		if pkg == nil || pkg.Export == "" {
			return nil, fmt.Errorf("no export data for %s", importPath)
		}
		return os.Open(pkg.Export)
	})
	return listing
}

// relativeDir is the slash path of a package directory relative to the repository root.
func (r *repository) relativeDir(dir string) string {
	rel, err := filepath.Rel(r.root, dir)
	if err != nil {
		return filepath.ToSlash(dir)
	}
	return filepath.ToSlash(rel)
}

// source reads a product file as the go command sees it: the override of a test, else the overlay, else the file.
func (r *repository) source(rel string, overrides map[string]string) ([]byte, error) {
	if text, ok := overrides[rel]; ok {
		return []byte(text), nil
	}
	if replacement, ok := r.overlay[rel]; ok {
		if replacement == "" {
			return nil, fs.ErrNotExist
		}
		return os.ReadFile(replacement)
	}
	return os.ReadFile(filepath.Join(r.root, filepath.FromSlash(rel)))
}

// packageFiles names every non-test Go file of a product package, whatever its constraint, plus the files an override
// adds. Overrides are keyed by slash path relative to the repository root.
func packageFiles(pkg *listedPackage, dirRel string, overrides map[string]string) []string {
	seen := map[string]bool{}
	var names []string
	add := func(name string) {
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || seen[name] {
			return
		}
		seen[name] = true
		names = append(names, name)
	}
	for _, list := range [][]string{pkg.GoFiles, pkg.CgoFiles, pkg.IgnoredGoFiles} {
		for _, name := range list {
			add(name)
		}
	}
	for rel := range overrides {
		if path.Dir(rel) == dirRel {
			add(path.Base(rel))
		}
	}
	sort.Strings(names)
	return names
}

// compiles reports whether a profile compiles a file, with the go command's own rules (go/build: file name suffixes
// and //go:build lines).
func (r *repository) compiles(profile buildProfile, dirRel, name string, overrides map[string]string) (bool, error) {
	context := build.Default
	context.BuildTags = profile.tags
	context.OpenFile = func(name string) (io.ReadCloser, error) {
		rel, err := filepath.Rel(r.root, name)
		if err != nil {
			return nil, err
		}
		data, err := r.source(filepath.ToSlash(rel), overrides)
		if err != nil {
			return nil, err
		}
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	return context.MatchFile(filepath.Join(r.root, filepath.FromSlash(dirRel)), name)
}

// classify decides the profile of a file from what each profile compiles.
func (r *repository) classify(dirRel, name string, overrides map[string]string) (fileKind, error) {
	public, err := r.compiles(publicProfile, dirRel, name, overrides)
	if err != nil {
		return excludedFile, err
	}
	development, err := r.compiles(developmentProfile, dirRel, name, overrides)
	if err != nil {
		return excludedFile, err
	}
	switch {
	case public && development:
		return sharedFile, nil
	case development:
		return developmentFile, nil
	case public:
		return publicFile, nil
	}
	return excludedFile, nil
}

// checkedPackage is one product package type-checked from source for one profile.
type checkedPackage struct {
	rel        string // slash directory relative to the repository root, for example internal/capture
	profile    buildProfile
	fset       *token.FileSet
	files      map[string]*ast.File // base name -> AST, compiled files only
	kinds      map[string]fileKind  // base name -> kind, every file of the package
	pkg        *types.Package
	info       *types.Info
	typeErrors []string
}

// check type-checks a product package for a profile. Overrides replace or add files (slash path relative to the
// repository root -> source) for the negative tests; they never touch the files on disk.
func (r *repository) check(t testing.TB, profile buildProfile, rel string, overrides map[string]string) *checkedPackage {
	t.Helper()
	listing := r.exportListing(t, profile)
	listed := listing[modulePath+"/"+rel]
	if listed == nil {
		t.Fatalf("%s is not listed for the %s profile", rel, profile.name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	fset, imports := r.fsets[profile.name], r.imports[profile.name]
	checked := &checkedPackage{rel: rel, profile: profile, fset: fset, files: map[string]*ast.File{}, kinds: map[string]fileKind{},
		info: &types.Info{Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{},
			Types: map[ast.Expr]types.TypeAndValue{}}}
	var files []*ast.File
	for _, name := range packageFiles(listed, rel, overrides) {
		kind, err := r.classify(rel, name, overrides)
		if err != nil {
			t.Fatalf("%s/%s: %v", rel, name, err)
		}
		checked.kinds[name] = kind
		included, err := r.compiles(profile, rel, name, overrides)
		if err != nil {
			t.Fatalf("%s/%s: %v", rel, name, err)
		}
		if !included {
			continue
		}
		data, err := r.source(rel+"/"+name, overrides)
		if err != nil {
			t.Fatalf("%s/%s: %v", rel, name, err)
		}
		file, err := parser.ParseFile(fset, filepath.Join(r.root, filepath.FromSlash(rel), name), data, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			checked.typeErrors = append(checked.typeErrors, err.Error())
			continue
		}
		checked.files[name] = file
		files = append(files, file)
	}
	config := types.Config{Importer: imports, Error: func(err error) {
		checked.typeErrors = append(checked.typeErrors, err.Error())
	}}
	checked.pkg, _ = config.Check(modulePath+"/"+rel, fset, files, checked.info)
	return checked
}

// fileOf is the base name of the file that declares a position.
func (c *checkedPackage) fileOf(pos token.Pos) string {
	return filepath.Base(c.fset.Position(pos).Filename)
}

// kindOf is the profile kind of the file that declares a position.
func (c *checkedPackage) kindOf(pos token.Pos) fileKind {
	return c.kinds[c.fileOf(pos)]
}

// where prints a position as package-relative file:line for findings.
func (c *checkedPackage) where(pos token.Pos) string {
	position := c.fset.Position(pos)
	return fmt.Sprintf("%s/%s:%d", c.rel, filepath.Base(position.Filename), position.Line)
}

// sortedFileNames lists the compiled files of a package in a stable order.
func (c *checkedPackage) sortedFileNames() []string {
	names := make([]string, 0, len(c.files))
	for name := range c.files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

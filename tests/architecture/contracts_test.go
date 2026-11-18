package architecture

import (
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// The architecture of the two build profiles (AGENTS.md section 5.4): one shared core (internal/capture) and one
// implementation of every product capability, Development-only capabilities in ordinary Development modules
// (developmentModules) that only the Development composition links, and profile differences only in a few build
// composition boundaries (compositionBoundaries), whose files choose what a profile links and hold no logic.

// ---------------------------------------------------------------------------------------------------------------
// Contract 1: profile files. A file belongs to a profile by its build constraint; its name must say the same, and the
// only profile tag is developmentTag. A third tag would be a third build profile, which the policy does not have.

// platformTags are the build tags the go command itself defines; they select a platform, not a profile.
var platformTags = map[string]bool{
	"aix": true, "android": true, "darwin": true, "dragonfly": true, "freebsd": true, "hurd": true, "illumos": true,
	"ios": true, "js": true, "linux": true, "netbsd": true, "openbsd": true, "plan9": true, "solaris": true,
	"wasip1": true, "windows": true, "zos": true, "unix": true,
	"386": true, "amd64": true, "arm": true, "arm64": true, "loong64": true, "mips": true, "mipsle": true,
	"mips64": true, "mips64le": true, "ppc64": true, "ppc64le": true, "riscv64": true, "s390x": true, "wasm": true,
	"cgo": true, "gc": true, "gccgo": true, "ignore": true,
}

// buildTags returns the tags named by a file's //go:build line.
func buildTags(source []byte) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), "", source, parser.PackageClauseOnly|parser.ParseComments)
	if err != nil {
		return nil, err
	}
	var tags []string
	var walk func(constraint.Expr)
	walk = func(expr constraint.Expr) {
		switch e := expr.(type) {
		case *constraint.AndExpr:
			walk(e.X)
			walk(e.Y)
		case *constraint.OrExpr:
			walk(e.X)
			walk(e.Y)
		case *constraint.NotExpr:
			walk(e.X)
		case *constraint.TagExpr:
			tags = append(tags, e.Tag)
		}
	}
	for _, group := range file.Comments {
		if group.End() > file.Package {
			break
		}
		for _, comment := range group.List {
			if constraint.IsGoBuild(comment.Text) {
				expr, err := constraint.Parse(comment.Text)
				if err != nil {
					return nil, err
				}
				walk(expr)
			}
		}
	}
	return tags, nil
}

// profileFileFindings checks every product file of both profiles; it also returns the packages that contain
// profile-specific files.
func profileFileFindings(t testing.TB, r *repository, overrides map[string]string) (findings []string, profilePackages map[string]bool) {
	t.Helper()
	profilePackages = map[string]bool{}
	seen := map[string]bool{}
	for _, profile := range bothProfiles {
		for _, pkg := range r.productPackages(t, profile) {
			rel := r.relativeDir(pkg.Dir)
			if seen[rel] {
				continue
			}
			seen[rel] = true
			for _, name := range packageFiles(pkg, rel, overrides) {
				source, err := r.source(rel+"/"+name, overrides)
				if err != nil {
					t.Fatalf("%s/%s: %v", rel, name, err)
				}
				tags, err := buildTags(source)
				if err != nil {
					findings = append(findings, fmt.Sprintf("%s/%s: unreadable build constraint: %v", rel, name, err))
					continue
				}
				for _, tag := range tags {
					if tag != developmentTag && !platformTags[tag] && !strings.HasPrefix(tag, "go1.") {
						findings = append(findings, fmt.Sprintf("%s/%s: build tag %q is neither the Development profile tag %q nor a platform tag; WWlogs has two build profiles selected by %q alone", rel, name, tag, developmentTag, developmentTag))
					}
				}
				kind, err := r.classify(rel, name, overrides)
				if err != nil {
					t.Fatalf("%s/%s: %v", rel, name, err)
				}
				namedDevelopment, namedPublic := strings.HasSuffix(name, "_development.go"), strings.HasSuffix(name, "_public.go")
				switch {
				case kind == developmentFile && !namedDevelopment:
					findings = append(findings, fmt.Sprintf("%s/%s: compiled only by the Development profile but not named *_development.go", rel, name))
				case kind == publicFile && !namedPublic:
					findings = append(findings, fmt.Sprintf("%s/%s: compiled only by the Public profile but not named *_public.go", rel, name))
				case namedDevelopment && kind != developmentFile:
					findings = append(findings, fmt.Sprintf("%s/%s: named for the Development profile but its build constraint makes it %s", rel, name, kind))
				case namedPublic && kind != publicFile:
					findings = append(findings, fmt.Sprintf("%s/%s: named for the Public profile but its build constraint makes it %s", rel, name, kind))
				}
				if kind == developmentFile || kind == publicFile {
					profilePackages[rel] = true
				}
			}
		}
	}
	sort.Strings(findings)
	return findings, profilePackages
}

// ---------------------------------------------------------------------------------------------------------------
// Contract 2: profile-specific files exist only at the build composition boundaries. Everywhere else a capability has
// one implementation: the core and every product feature for both profiles, a Development capability in a Development
// module. A profile file (or a Public no-op beside a Development hook) in any other package is a parallel
// implementation inside a feature module.

func boundaryFindings(profilePackages map[string]bool) []string {
	var findings []string
	for rel := range profilePackages {
		if !isCompositionBoundary(rel) {
			findings = append(findings, fmt.Sprintf("%s has profile-specific files but is not a build composition boundary: put a Development capability into a Development module and choose it in a composition boundary instead of splitting the package by profile", rel))
		}
	}
	for _, rel := range compositionBoundaries {
		if !profilePackages[rel] {
			findings = append(findings, fmt.Sprintf("%s is listed as a composition boundary but has no profile-specific file any more; remove it from compositionBoundaries", rel))
		}
	}
	sort.Strings(findings)
	return findings
}

// ---------------------------------------------------------------------------------------------------------------
// Contract 3: a composition boundary holds only wiring. Its profile-specific files declare package-level constants and
// variables whose values only name what the profile links (identifiers, qualified names, literals and composite
// literals of those); no function, method or type, and no call: logic belongs in the shared code or a Development
// module. Both profiles declare the same names with the same types, so the shared code of the boundary reads either
// profile the same way.

// wiringValue reports whether an expression only names things: what a composition file may assign.
func wiringValue(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.Ident, *ast.BasicLit:
		return true
	case *ast.SelectorExpr:
		return wiringValue(e.X)
	case *ast.UnaryExpr:
		return e.Op == token.AND && wiringValue(e.X)
	case *ast.CompositeLit:
		for _, element := range e.Elts {
			if keyValue, ok := element.(*ast.KeyValueExpr); ok {
				element = keyValue.Value
			}
			if !wiringValue(element) {
				return false
			}
		}
		return true
	}
	return false
}

func wiringFindings(public, development *checkedPackage) []string {
	var findings []string
	declared := map[string]map[string]string{} // profile -> name -> type
	for _, c := range []*checkedPackage{public, development} {
		for _, message := range c.typeErrors {
			findings = append(findings, fmt.Sprintf("%s profile does not type-check: %s", c.profile.name, message))
		}
		declared[c.profile.name] = map[string]string{}
		for _, name := range c.sortedFileNames() {
			if c.kinds[name] != developmentFile && c.kinds[name] != publicFile {
				continue
			}
			for _, decl := range c.files[name].Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					findings = append(findings, fmt.Sprintf("%s: the composition file declares the function %s; a composition boundary holds wiring only, logic belongs in the shared code or a Development module", c.where(d.Pos()), functionLabel(d)))
				case *ast.GenDecl:
					if d.Tok == token.IMPORT {
						continue
					}
					if d.Tok == token.TYPE {
						findings = append(findings, fmt.Sprintf("%s: the composition file declares a type; a composition boundary holds wiring only", c.where(d.Pos())))
						continue
					}
					for _, spec := range d.Specs {
						value := spec.(*ast.ValueSpec)
						for _, expr := range value.Values {
							if !wiringValue(expr) {
								findings = append(findings, fmt.Sprintf("%s: the composition file computes a value (%T); it may only name what the profile links", c.where(expr.Pos()), expr))
							}
						}
						for _, ident := range value.Names {
							if obj := c.info.Defs[ident]; obj != nil {
								declared[c.profile.name][ident.Name] = obj.Type().String()
							}
						}
					}
				}
			}
		}
	}
	for name, publicType := range declared[publicProfile.name] {
		if developmentType, ok := declared[developmentProfile.name][name]; !ok {
			findings = append(findings, fmt.Sprintf("%s: only the Public profile declares %s", public.rel, name))
		} else if developmentType != publicType {
			findings = append(findings, fmt.Sprintf("%s: %s is %s in the Public profile and %s in the Development profile", public.rel, name, publicType, developmentType))
		}
	}
	for name := range declared[developmentProfile.name] {
		if _, ok := declared[publicProfile.name][name]; !ok {
			findings = append(findings, fmt.Sprintf("%s: only the Development profile declares %s", development.rel, name))
		}
	}
	sort.Strings(findings)
	return findings
}

// ---------------------------------------------------------------------------------------------------------------
// Contract 4: the Development modules stay out of Public and keep their capabilities. No Public executable links a
// Development module or a Development-only dependency, every Development executable still links them (removing one is
// an owner decision, AGENTS.md section 5.4), every product package only Development executables link is declared, and
// only Development modules and the Development files of the composition boundaries import a Development module: the
// core, the desktop host and every other product package never do.

func developmentLinkFindings(public, development map[string]bool) []string {
	var findings []string
	expected := map[string]bool{}
	for _, rel := range developmentModules {
		expected[modulePath+"/"+rel] = true
	}
	for _, pkg := range developmentDependencies {
		expected[pkg] = true
	}
	for pkg := range expected {
		if public[pkg] {
			findings = append(findings, fmt.Sprintf("the Public executables (./cmd/...) link %s, which only Development capabilities need", pkg))
		}
		if !development[pkg] {
			findings = append(findings, fmt.Sprintf("the Development executables (./cmd/...) no longer link %s: a Development capability is gone", pkg))
		}
	}
	for pkg := range development {
		if public[pkg] || expected[pkg] {
			continue
		}
		if strings.HasPrefix(pkg, modulePath+"/") && !strings.HasPrefix(pkg, modulePath+"/cmd/") {
			findings = append(findings, fmt.Sprintf("only the Development executables link %s; declare it in developmentModules or developmentDependencies, or let Public use it", pkg))
		}
	}
	sort.Strings(findings)
	return findings
}

// developmentImportFindings checks the import declarations of every product file, whatever its build constraint.
func developmentImportFindings(t testing.TB, r *repository, overrides map[string]string) []string {
	t.Helper()
	var findings []string
	modules := map[string]bool{}
	for _, rel := range developmentModules {
		modules[modulePath+"/"+rel] = true
	}
	seen := map[string]bool{}
	for _, profile := range bothProfiles {
		for _, pkg := range r.productPackages(t, profile) {
			rel := r.relativeDir(pkg.Dir)
			if seen[rel] || isDevelopmentModule(rel) {
				continue
			}
			seen[rel] = true
			for _, name := range packageFiles(pkg, rel, overrides) {
				source, err := r.source(rel+"/"+name, overrides)
				if err != nil {
					t.Fatalf("%s/%s: %v", rel, name, err)
				}
				file, err := parser.ParseFile(token.NewFileSet(), name, source, parser.ImportsOnly)
				if err != nil {
					findings = append(findings, fmt.Sprintf("%s/%s: %v", rel, name, err))
					continue
				}
				kind, err := r.classify(rel, name, overrides)
				if err != nil {
					t.Fatalf("%s/%s: %v", rel, name, err)
				}
				for _, spec := range file.Imports {
					path, _ := strconv.Unquote(spec.Path.Value)
					if !modules[path] {
						continue
					}
					if isCompositionBoundary(rel) && kind == developmentFile {
						continue
					}
					findings = append(findings, fmt.Sprintf("%s/%s imports the Development module %s: only Development modules and the Development files of a composition boundary may", rel, name, path))
				}
			}
		}
	}
	sort.Strings(findings)
	return findings
}

// ---------------------------------------------------------------------------------------------------------------
// Contract 5: the core holds no Development implementation. Every function, method and type of the core is used by the
// core itself or by a product package outside the Development modules; one only Development modules use is Development
// code in the core (compiled into Public, merely unused) and belongs in a Development module. The observation entry
// points only the Development composition calls are reviewed below.

// reviewedDevelopmentEntryPoints are core declarations only Development modules use, each with the reason it is part
// of the core.
var reviewedDevelopmentEntryPoints = map[string]string{
	"method Processor.Observe":                            "the core's observation entry point: the Development composition attaches its observer; the core never decides anything from it",
	"method DamageStatistics.ObserveEncounterTransitions": "the observation entry point of the encounter state machine: the Development encounter output receives its transitions",
	"method EffectTracker.Snapshot":                       "a neutral read of the tracker's formal data (the tracked set without remaining-time estimates) for the Development effect outputs",
}

// standardInterfaceMethods are method names the language or the standard library calls through an interface: a use
// of the method itself is not visible to the type checker.
var standardInterfaceMethods = map[string]bool{
	"Error": true, "String": true, "Write": true, "Read": true, "Close": true, "Unwrap": true, "MarshalJSON": true,
	"UnmarshalJSON": true, "MarshalText": true, "UnmarshalText": true, "Len": true, "Less": true, "Swap": true,
	"Format": true, "GoString": true, "Is": true, "As": true, "Flush": true, "ServeHTTP": true, "Open": true,
}

// coreUseKeys collects the keys of the declarations of the core a checked package uses.
func coreUseKeys(c *checkedPackage, corePath string, owners map[*types.Var]string) map[string]bool {
	used := map[string]bool{}
	for _, obj := range c.info.Uses {
		if obj == nil || obj.Pkg() == nil || obj.Pkg().Path() != corePath {
			continue
		}
		if key := objectKey(obj, owners); key != "" {
			used[key] = true
		}
	}
	return used
}

func coreServiceFindings(core *checkedPackage, users []*checkedPackage) (findings []string, reviewedSeen map[string]bool) {
	reviewedSeen = map[string]bool{}
	corePath := core.pkg.Path()
	served := coreUseKeys(core, corePath, fieldOwners(core))
	developmentUse := map[string]bool{}
	for _, user := range users {
		for key := range coreUseKeys(user, corePath, importedFieldOwners(user, corePath)) {
			if isDevelopmentModule(user.rel) {
				developmentUse[key] = true
			} else {
				served[key] = true
			}
		}
	}
	for key, decl := range declarations(core) {
		kind := strings.SplitN(key, " ", 2)[0]
		if kind != "func" && kind != "method" && kind != "type" {
			continue
		}
		if kind == "method" && standardInterfaceMethods[decl.obj.Name()] {
			continue
		}
		if served[key] || !developmentUse[key] {
			continue
		}
		if _, ok := reviewedDevelopmentEntryPoints[key]; ok {
			reviewedSeen[key] = true
			continue
		}
		findings = append(findings, fmt.Sprintf("%s: %s of the core is used only by Development modules; Development implementation belongs in a Development module", decl.where, key))
	}
	sort.Strings(findings)
	return findings, reviewedSeen
}

// importedPackage is the package object a checked package imports for a path (nil when it does not import it).
func importedPackage(c *checkedPackage, path string) *types.Package {
	if c.pkg == nil {
		return nil
	}
	for _, imported := range c.pkg.Imports() {
		if imported.Path() == path {
			return imported
		}
	}
	return nil
}

// importedFieldOwners maps the struct fields of an imported package's named types to their type names.
func importedFieldOwners(c *checkedPackage, path string) map[*types.Var]string {
	owners := map[*types.Var]string{}
	pkg := importedPackage(c, path)
	if pkg == nil {
		return owners
	}
	for _, name := range pkg.Scope().Names() {
		typeName, ok := pkg.Scope().Lookup(name).(*types.TypeName)
		if !ok || typeName.IsAlias() {
			continue
		}
		if named, ok := typeName.Type().(*types.Named); ok && named.Origin() != named {
			continue
		}
		if structType, ok := typeName.Type().Underlying().(*types.Struct); ok {
			for i := 0; i < structType.NumFields(); i++ {
				field := structType.Field(i)
				// A type defined from another struct type shares its fields: the first type in scope order owns them,
				// which for the core is the type that spells the struct unless a defined copy sorts first.
				if _, taken := owners[field]; !taken {
					owners[field] = name
				}
			}
		}
	}
	return owners
}

// ---------------------------------------------------------------------------------------------------------------
// Declarations of a checked package.

type declaration struct {
	key   string // "func f", "method T.m", "type T", "var v", "const c" or "field T.f"
	obj   types.Object
	kind  fileKind
	where string
}

func receiverName(t types.Type) string {
	if pointer, ok := t.(*types.Pointer); ok {
		t = pointer.Elem()
	}
	if named, ok := t.(*types.Named); ok {
		return named.Obj().Name()
	}
	return t.String()
}

// fieldOwners maps every field of the package's named struct types to the type that spells the struct.
func fieldOwners(c *checkedPackage) map[*types.Var]string {
	owners := map[*types.Var]string{}
	if c.pkg == nil {
		return owners
	}
	for _, file := range c.files {
		ast.Inspect(file, func(n ast.Node) bool {
			spec, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			if structType, ok := spec.Type.(*ast.StructType); ok {
				for _, field := range structType.Fields.List {
					for _, name := range field.Names {
						if v, ok := c.info.Defs[name].(*types.Var); ok {
							owners[v] = spec.Name.Name
						}
					}
				}
			}
			return true
		})
	}
	for _, name := range c.pkg.Scope().Names() {
		typeName, ok := c.pkg.Scope().Lookup(name).(*types.TypeName)
		if !ok {
			continue
		}
		if structType, ok := typeName.Type().Underlying().(*types.Struct); ok {
			for i := 0; i < structType.NumFields(); i++ {
				if _, taken := owners[structType.Field(i)]; !taken {
					owners[structType.Field(i)] = name
				}
			}
		}
	}
	return owners
}

// objectKey names a package-level object, method or field; the empty key is any other object.
func objectKey(obj types.Object, owners map[*types.Var]string) string {
	switch o := obj.(type) {
	case *types.Func:
		if signature, ok := o.Type().(*types.Signature); ok && signature.Recv() != nil {
			return "method " + receiverName(signature.Recv().Type()) + "." + o.Name()
		}
		if o.Pkg() != nil && o.Parent() == o.Pkg().Scope() {
			return "func " + o.Name()
		}
	case *types.TypeName:
		if o.Pkg() != nil && o.Parent() == o.Pkg().Scope() {
			return "type " + o.Name()
		}
	case *types.Const:
		if o.Pkg() != nil && o.Parent() == o.Pkg().Scope() {
			return "const " + o.Name()
		}
	case *types.Var:
		if o.IsField() {
			if owner, ok := owners[o]; ok {
				return "field " + owner + "." + o.Name()
			}
			return ""
		}
		if o.Pkg() != nil && o.Parent() == o.Pkg().Scope() {
			return "var " + o.Name()
		}
	}
	return ""
}

// declarations lists the package-level objects, methods and named-struct fields a checked package declares.
func declarations(c *checkedPackage) map[string]declaration {
	declared := map[string]declaration{}
	owners := fieldOwners(c)
	for ident, obj := range c.info.Defs {
		if obj == nil || obj.Pkg() != c.pkg || ident.Name == "_" || ident.Name == "init" {
			continue
		}
		key := objectKey(obj, owners)
		if key == "" {
			continue
		}
		declared[key] = declaration{key: key, obj: obj, kind: c.kindOf(obj.Pos()), where: c.where(obj.Pos())}
	}
	return declared
}

// ---------------------------------------------------------------------------------------------------------------
// Contract 6: the observation points are one-way. The interfaces through which the core hands its observation points
// to the Development modules return nothing the core could decide with, and they receive values, not pointers: a
// pointer would let the observer write the core value, and because the compiler cannot see through the interface call
// it moves the value to the heap in every profile, Public included, where no observer runs. The few results and
// pointers are reviewed below with the reason they cannot change combat results. A new interface the core calls into
// Development code is listed in observationInterfaces.

// observationInterfaces are the core interfaces the Development modules implement.
var observationInterfaces = []string{"Observer", "CommandCensusObserver", "CensusFrameObserver", "SessionDiagnostics"}

// reviewedObservationResults are the observation methods that return a value.
var reviewedObservationResults = map[string]string{
	"CommandCensusObserver.FailedCRC":    "the census's own index of a CRC failure; the frame parser only hands it back with the next valid frame boundary",
	"CommandCensusObserver.ObserveFrame": "the census entry of the frame, which the dispatcher only reports to",
	"SessionDiagnostics.Finish":          "the summary text of the Development spool for the session result shown to the developer; no combat decision reads it",
}

// reviewedObservationPointers are the observation methods that receive a pointer.
var reviewedObservationPointers = map[string]string{
	"Observer.AuditCombatNotify": "the optional CombatCommon identifiers the decoder already holds on the heap; the audit only formats them",
	"SessionDiagnostics.Attach":  "the session's processor the Development diagnostics attach their observer to; its two settings are reviewed in contract 7",
}

func observationFindings(core *checkedPackage) (findings []string, resultsSeen, pointersSeen map[string]bool) {
	resultsSeen, pointersSeen = map[string]bool{}, map[string]bool{}
	for _, name := range observationInterfaces {
		typeName, ok := core.pkg.Scope().Lookup(name).(*types.TypeName)
		if !ok {
			findings = append(findings, fmt.Sprintf("%s: the observation interface %s is missing; update observationInterfaces", core.rel, name))
			continue
		}
		iface, ok := typeName.Type().Underlying().(*types.Interface)
		if !ok {
			findings = append(findings, fmt.Sprintf("%s: %s is not an interface", core.where(typeName.Pos()), name))
			continue
		}
		for i := 0; i < iface.NumMethods(); i++ {
			method := iface.Method(i)
			signature := method.Type().(*types.Signature)
			key := name + "." + method.Name()
			if signature.Results().Len() > 0 {
				if _, ok := reviewedObservationResults[key]; ok {
					resultsSeen[key] = true
				} else {
					findings = append(findings, fmt.Sprintf("%s: the observation method %s returns a value; an observation point returns nothing the core could decide with, or its value is reviewed", core.where(method.Pos()), key))
				}
			}
			for j := 0; j < signature.Params().Len(); j++ {
				param := signature.Params().At(j)
				if _, ok := param.Type().Underlying().(*types.Pointer); !ok {
					continue
				}
				if _, ok := reviewedObservationPointers[key]; ok {
					pointersSeen[key] = true
					continue
				}
				findings = append(findings, fmt.Sprintf("%s: the observation method %s takes the pointer %s; an observation point receives values (a pointer lets the observer write the core value and moves it to the heap in every profile), or its pointer is reviewed", core.where(method.Pos()), key, param.Name()))
			}
		}
	}
	sort.Strings(findings)
	return findings, resultsSeen, pointersSeen
}

// ---------------------------------------------------------------------------------------------------------------
// Contract 7: Development code does not write core state. A Development module changes core memory only as a local
// copy. Through a pointer, a slice or a map it writes only its own data: never a field of a core type, a whole core value
// through its pointer, or a package variable of the core. The configuration a Development entry point gives a processor
// before or around processing is reviewed below, each with the reason it cannot change combat results.
//
// Not visible to this check: a change made by calling a core method, and an element written through a local alias of a
// core map or slice. The failure-injection tests (tests/devcapture/diagnostic_isolation_dev_test.go) and the replay
// statistics equality of both profiles (scripts/verify.ps1) cover those by behavior.

// reviewedDevelopmentWrites are the writes of Development code to core state (function, then field).
var reviewedDevelopmentWrites = map[string]string{
	"Main Processor.Masters":                           "entry point configuration: the Development CLI hands the processor the Master it loaded, as the Public Main does",
	"Main Processor.Census":                            "entry point configuration: the Development CLI attaches its command census to the frame parser's observation points",
	"Main Processor.CaptureLayer":                      "the Development diagnostic pipeline reports its dropped records in the capture-layer health (operational status, not combat results)",
	"sessionDiagnostics.Attach Processor.CaptureLayer": "the same dropped-record counter for a Desktop Development session spool",
	"sessionDiagnostics.Attach Processor.Diagnostic":   "a Desktop Development session routes the operational diagnostic stream into the spool console.log",
}

// writeTargets returns the expressions a node assigns to, increments, or deletes from, with the assignment statement
// for plain assignments.
func writeTargets(info *types.Info, node ast.Node) (targets []ast.Expr, assignments map[ast.Expr]*ast.AssignStmt) {
	assignments = map[ast.Expr]*ast.AssignStmt{}
	ast.Inspect(node, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			if s.Tok != token.DEFINE {
				for _, lhs := range s.Lhs {
					targets = append(targets, lhs)
					assignments[lhs] = s
				}
			}
		case *ast.IncDecStmt:
			targets = append(targets, s.X)
		case *ast.RangeStmt:
			if s.Tok == token.ASSIGN {
				for _, e := range []ast.Expr{s.Key, s.Value} {
					if e != nil {
						targets = append(targets, e)
					}
				}
			}
		case *ast.CallExpr:
			if ident, ok := ast.Unparen(s.Fun).(*ast.Ident); ok && len(s.Args) > 0 {
				if builtin, ok := info.Uses[ident].(*types.Builtin); ok && (builtin.Name() == "delete" || builtin.Name() == "clear") {
					targets = append(targets, s.Args[0])
				}
			}
		}
		return true
	})
	return targets, assignments
}

// writtenChain walks a written expression to its root and returns the root and the field selections on the way,
// root first: p.a.b[i].c gives p and the selections of a, b and c.
func writtenChain(info *types.Info, expr ast.Expr) (ast.Expr, []*ast.SelectorExpr) {
	var chain []*ast.SelectorExpr
	for {
		switch e := expr.(type) {
		case *ast.ParenExpr:
			expr = e.X
		case *ast.StarExpr:
			expr = e.X
		case *ast.IndexExpr:
			expr = e.X
		case *ast.IndexListExpr:
			expr = e.X
		case *ast.SelectorExpr:
			if selection := info.Selections[e]; selection != nil && selection.Kind() == types.FieldVal {
				chain = append([]*ast.SelectorExpr{e}, chain...)
				expr = e.X
				continue
			}
			return e, chain
		default:
			return expr, chain
		}
	}
}

// selectedField is the field a selector expression selects.
func selectedField(info *types.Info, selector *ast.SelectorExpr) *types.Var {
	if selection := info.Selections[selector]; selection != nil {
		if field, ok := selection.Obj().(*types.Var); ok {
			return field
		}
	}
	return nil
}

// functionLabel names a function declaration: Name, or Receiver.Name for a method.
func functionLabel(function *ast.FuncDecl) string {
	if function.Recv == nil || len(function.Recv.List) == 0 {
		return function.Name.Name
	}
	receiver := function.Recv.List[0].Type
	if star, ok := receiver.(*ast.StarExpr); ok {
		receiver = star.X
	}
	if index, ok := receiver.(*ast.IndexExpr); ok {
		receiver = index.X
	}
	if ident, ok := receiver.(*ast.Ident); ok {
		return ident.Name + "." + function.Name.Name
	}
	return function.Name.Name
}

// indirectWrite reports whether a written expression reaches memory outside a local copy: a pointer dereference, a
// field selected through a pointer (also through an embedded pointer), or an element of a slice or map.
func indirectWrite(info *types.Info, expr ast.Expr) bool {
	for {
		switch e := expr.(type) {
		case *ast.ParenExpr:
			expr = e.X
		case *ast.StarExpr:
			return true
		case *ast.IndexExpr:
			if value, ok := info.Types[e.X]; ok {
				switch value.Type.Underlying().(type) {
				case *types.Slice, *types.Map, *types.Pointer:
					return true
				}
			}
			expr = e.X
		case *ast.SelectorExpr:
			if selection := info.Selections[e]; selection != nil && selection.Indirect() {
				return true
			}
			if value, ok := info.Types[e.X]; ok {
				if _, pointer := value.Type.Underlying().(*types.Pointer); pointer {
					return true
				}
			}
			expr = e.X
		default:
			return false
		}
	}
}

func developmentWriteFindings(development *checkedPackage, corePath string) (findings []string, reviewedSeen map[string]bool) {
	reviewedSeen = map[string]bool{}
	owners := importedFieldOwners(development, corePath)
	isCoreVariable := func(obj types.Object) bool {
		variable, ok := obj.(*types.Var)
		return ok && !variable.IsField() && variable.Pkg() != nil && variable.Pkg().Path() == corePath && variable.Parent() == variable.Pkg().Scope()
	}
	for _, name := range development.sortedFileNames() {
		for _, decl := range development.files[name].Decls {
			function, ok := decl.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			targets, _ := writeTargets(development.info, function.Body)
			for _, target := range targets {
				root, chain := writtenChain(development.info, target)
				if len(chain) == 0 {
					var obj types.Object
					switch r := root.(type) {
					case *ast.Ident:
						obj = development.info.Uses[r]
					case *ast.SelectorExpr:
						obj = development.info.Uses[r.Sel]
					}
					if isCoreVariable(obj) {
						findings = append(findings, fmt.Sprintf("%s: Development function %s writes the core package variable %s", development.where(target.Pos()), functionLabel(function), obj.Name()))
						continue
					}
				}
				if !indirectWrite(development.info, target) {
					continue // the write changes a local copy or Development data
				}
				if star, ok := ast.Unparen(target).(*ast.StarExpr); ok && len(chain) == 0 {
					if named, ok := development.info.Types[star].Type.(*types.Named); ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == corePath {
						findings = append(findings, fmt.Sprintf("%s: Development function %s replaces a %s, a core value, through a pointer; a Development observer may change only its own copy", development.where(target.Pos()), functionLabel(function), named.Obj().Name()))
					}
					continue
				}
				for _, selector := range chain {
					field := selectedField(development.info, selector)
					if field == nil || field.Pkg() == nil || field.Pkg().Path() != corePath {
						continue // a field of a Development type
					}
					label := functionLabel(function) + " " + owners[field] + "." + field.Name()
					if _, reviewed := reviewedDevelopmentWrites[label]; reviewed {
						reviewedSeen[label] = true
					} else {
						findings = append(findings, fmt.Sprintf("%s: Development function %s writes %s.%s, core state, through a pointer, slice or map; a Development observer changes only its own data", development.where(target.Pos()), functionLabel(function), owners[field], field.Name()))
					}
					break
				}
			}
		}
	}
	sort.Strings(findings)
	return findings, reviewedSeen
}

// ---------------------------------------------------------------------------------------------------------------
// Contract 8: every core processor field is read by the core. A field the core only initializes, assigns, increments,
// appends to or deletes from, and that only Development code reads, is Development state in the core; it belongs in the
// Development module. New core fields are welcome as long as the core reads them.

// coreStateType is the core state struct of the capture core.
const coreStateType = "Processor"

// reviewedUnreadCoreFields are core fields only Development code reads, each with the reason it stays in the core.
var reviewedUnreadCoreFields = map[string]string{
	"NotifyStats": "Deferred (Phase 3B-4A): the core counts its own DamageExecuteNotify settlement decisions here, and the failure-injection tests compare it as a core result; only the Development summary prints it",
}

func coreFieldFindings(core *checkedPackage) (findings []string, reviewedSeen map[string]bool) {
	reviewedSeen = map[string]bool{}
	coreType, ok := core.pkg.Scope().Lookup(coreStateType).(*types.TypeName)
	if !ok {
		return []string{fmt.Sprintf("%s: the core state type %s is missing; update the architecture contract with the new core state", core.rel, coreStateType)}, reviewedSeen
	}
	structType, ok := coreType.Type().Underlying().(*types.Struct)
	if !ok {
		return []string{fmt.Sprintf("%s: %s is not a struct", core.rel, coreStateType)}, reviewedSeen
	}
	coreFields := map[*types.Var]bool{}
	for i := 0; i < structType.NumFields(); i++ {
		coreFields[structType.Field(i)] = true
	}
	written := map[*ast.Ident]bool{}
	reads := map[*types.Var]int{}
	for _, name := range core.sortedFileNames() {
		file := core.files[name]
		targets, assignments := writeTargets(core.info, file)
		for _, target := range targets {
			_, chain := writtenChain(core.info, target)
			var assignedFields []*types.Var
			for _, selector := range chain {
				if field := selectedField(core.info, selector); coreFields[field] {
					written[selector.Sel] = true
					assignedFields = append(assignedFields, field)
				}
			}
			// p.list = append(p.list, x) and p.n = p.n + 1 update the field from itself: still no read.
			if assignment := assignments[target]; assignment != nil && len(assignedFields) > 0 {
				for _, rhs := range assignment.Rhs {
					ast.Inspect(rhs, func(n ast.Node) bool {
						if selector, ok := n.(*ast.SelectorExpr); ok {
							for _, field := range assignedFields {
								if selectedField(core.info, selector) == field {
									written[selector.Sel] = true
								}
							}
						}
						return true
					})
				}
			}
		}
		// Keys of a processor composite literal initialize fields.
		ast.Inspect(file, func(n ast.Node) bool {
			if keyValue, ok := n.(*ast.KeyValueExpr); ok {
				if key, ok := keyValue.Key.(*ast.Ident); ok {
					if field, ok := core.info.Uses[key].(*types.Var); ok && coreFields[field] {
						written[key] = true
					}
				}
			}
			return true
		})
		ast.Inspect(file, func(n ast.Node) bool {
			if ident, ok := n.(*ast.Ident); ok {
				if field, ok := core.info.Uses[ident].(*types.Var); ok && coreFields[field] && !written[ident] {
					reads[field]++
				}
			}
			return true
		})
	}
	for i := 0; i < structType.NumFields(); i++ {
		field := structType.Field(i)
		if _, reviewed := reviewedUnreadCoreFields[field.Name()]; reviewed && reads[field] == 0 {
			reviewedSeen[field.Name()] = true
		} else if reads[field] == 0 {
			findings = append(findings, fmt.Sprintf("%s: the core field %s.%s is never read by the core; Development state belongs in the Development module, and unread core state should go", core.where(field.Pos()), coreStateType, field.Name()))
		}
	}
	return findings, reviewedSeen
}

// ---------------------------------------------------------------------------------------------------------------
// Contract 9: the research protocol schema lives in the Development module. The research schema (-protocol-schema) and
// the generic decoder's records are research evidence, not runtime authority (AGENTS.md section 3.1): they are declared
// in the Development capture module, and the core declares none of them, so no core decision can read them (the core
// cannot import a Development module, contract 4). The behavioral side (a role decoded with and without the schema
// gives the same core result) is tests/devcapture/client_push_dev_test.go.

// researchSchemaAnchors are types of the research schema.
var researchSchemaAnchors = []string{"protocolSchema", "decodedMessage"}

// researchModule is the Development module that declares the research schema.
const researchModule = "internal/devcapture"

func researchSchemaFindings(core, research *checkedPackage) []string {
	var findings []string
	for _, anchor := range researchSchemaAnchors {
		if obj := research.pkg.Scope().Lookup(anchor); obj == nil {
			findings = append(findings, fmt.Sprintf("%s: the research schema type %s is missing; update researchSchemaAnchors with the type that now marks the research schema", research.rel, anchor))
		}
		for _, name := range []string{anchor, strings.ToUpper(anchor[:1]) + anchor[1:]} {
			if obj, ok := core.pkg.Scope().Lookup(name).(*types.TypeName); ok {
				findings = append(findings, fmt.Sprintf("%s: the core declares the research schema type %s; research schema evidence is not runtime authority and stays in %s", core.where(obj.Pos()), name, researchModule))
			}
		}
	}
	sort.Strings(findings)
	return findings
}

// ---------------------------------------------------------------------------------------------------------------
// Contract 10: the Development diagnostic surface is preserved and stays out of Public. Every flag below is a
// Development diagnostic, research or test input of v0.8.0 (the Development diagnostic surface on 2026-11-17). Each
// must still be defined in its Development module, and no Public command line may define it. New Development flags need
// no change here; removing one is a deliberate owner decision that removes it from this list in the same change.

var developmentFlags = map[string][]string{
	"internal/devcapture": {
		"audit-out", "buff-event-out", "buff-overlap-out", "buff-snapshot-out", "bullet-ref-out", "capture-immediate",
		"client-push-out", "client-version-out", "combat-notify-audit-out", "combat-state-out", "command-census-out",
		"damage-push-out", "effect-state-out", "encounter-out", "encounter-summary-out", "event-jsonl-out",
		"frame-archive-out", "hp-state-out", "live-diagnostic-out", "master-db", "max-pcap-bytes", "part-ref-out",
		"pcap-out", "protocol-schema", "research", "role-config-out", "schema-decode-out", "shield-ref-out",
		"show-all-effect-events", "show-effect-events", "skill-ref-out", "status-file", "summon-owner-out",
	},
	"internal/devdesktop": {
		"asset-root", "autostart", "capture-diagnostics", "font-root", "history-db", "ip", "master-db", "overlay",
		"record-browser-url", "replay", "replay-pacing", "showcase", "showcase-overlays", "simulate-capture-environment",
		"simulate-npcap-offline", "simulate-ui-languages", "webview-args", "webview-debug-port",
	},
}

// publicCommandLines are the product packages that define the command lines of the Public executables, with the
// Public flags a Development command line repeats on purpose.
var publicCommandLines = map[string]map[string]bool{
	"internal/capture": {"ip": true},
	"internal/desktop": {},
}

// flagNameArgument is the position of the flag name in the flag package's definition functions and methods.
var flagNameArgument = map[string]int{
	"Bool": 0, "BoolFunc": 0, "Duration": 0, "Float64": 0, "Func": 0, "Int": 0, "Int64": 0, "String": 0, "Uint": 0, "Uint64": 0,
	"BoolVar": 1, "DurationVar": 1, "Float64Var": 1, "IntVar": 1, "Int64Var": 1, "StringVar": 1, "UintVar": 1, "Uint64Var": 1, "TextVar": 1, "Var": 1,
}

// flagDefinitions lists the flags a checked package defines with the flag package.
func flagDefinitions(c *checkedPackage) map[string]string {
	defined := map[string]string{}
	for _, name := range c.sortedFileNames() {
		ast.Inspect(c.files[name], func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
			if !ok {
				return true
			}
			function, ok := c.info.Uses[selector.Sel].(*types.Func)
			if !ok || function.Pkg() == nil || function.Pkg().Path() != "flag" {
				return true
			}
			position, ok := flagNameArgument[function.Name()]
			if !ok || position >= len(call.Args) {
				return true
			}
			value := c.info.Types[call.Args[position]].Value
			if value == nil || value.Kind() != constant.String {
				return true
			}
			defined[constant.StringVal(value)] = c.where(call.Pos())
			return true
		})
	}
	return defined
}

func developmentFlagFindings(module *checkedPackage, publicLines map[*checkedPackage]map[string]bool) []string {
	var findings []string
	defined := flagDefinitions(module)
	for _, name := range developmentFlags[module.rel] {
		if _, ok := defined[name]; !ok {
			findings = append(findings, fmt.Sprintf("%s: the Development diagnostic flag -%s is no longer defined", module.rel, name))
		}
		for line, shared := range publicLines {
			if where, ok := flagDefinitions(line)[name]; ok && !shared[name] {
				findings = append(findings, fmt.Sprintf("%s: a Public command line defines the Development diagnostic flag -%s", where, name))
			}
		}
	}
	sort.Strings(findings)
	return findings
}

// ---------------------------------------------------------------------------------------------------------------
// The tests on the product source.

var (
	checkedMu    sync.Mutex
	checkedCache = map[string]*checkedPackage{}
)

// productCheck is a package of the actual source type-checked for a profile, cached for the run.
func productCheck(t testing.TB, profile buildProfile, rel string) *checkedPackage {
	t.Helper()
	key := profile.name + " " + rel
	checkedMu.Lock()
	cached := checkedCache[key]
	checkedMu.Unlock()
	if cached != nil {
		return cached
	}
	checked := loadRepository(t).check(t, profile, rel, nil)
	checkedMu.Lock()
	checkedCache[key] = checked
	checkedMu.Unlock()
	return checked
}

func reportFindings(t *testing.T, contract string, findings []string) {
	t.Helper()
	if len(findings) > 0 {
		t.Errorf("%s: %d findings\n%s", contract, len(findings), strings.Join(findings, "\n"))
	}
}

// reportStaleEntries fails for a reviewed exception the source no longer needs, so the lists only name live cases.
func reportStaleEntries(t *testing.T, list string, entries map[string]string, seen map[string]bool) {
	t.Helper()
	for name := range entries {
		if !seen[name] {
			t.Errorf("%s lists %q, which the source no longer needs; remove the entry", list, name)
		}
	}
}

// coreUsers are the product packages (both profiles) that import the core, type-checked from source: the actual
// source through the run's cache, a probe repository directly.
func coreUsers(t testing.TB, r *repository) []*checkedPackage {
	t.Helper()
	check := func(profile buildProfile, rel string) *checkedPackage { return r.check(t, profile, rel, nil) }
	if r == loadRepository(t) {
		check = func(profile buildProfile, rel string) *checkedPackage { return productCheck(t, profile, rel) }
	}
	var users []*checkedPackage
	for _, profile := range bothProfiles {
		listing := r.exportListing(t, profile)
		for _, pkg := range r.productPackages(t, profile) {
			rel := r.relativeDir(pkg.Dir)
			listed := listing[modulePath+"/"+rel]
			if rel == corePackage || listed == nil {
				continue
			}
			for _, imported := range listed.Imports {
				if imported == modulePath+"/"+corePackage {
					users = append(users, check(profile, rel))
					break
				}
			}
		}
	}
	return users
}

func TestProfileFilesFollowTheirConstraints(t *testing.T) {
	findings, _ := profileFileFindings(t, loadRepository(t), nil)
	reportFindings(t, "profile files", findings)
}

func TestProfileFilesOnlyAtCompositionBoundaries(t *testing.T) {
	_, profilePackages := profileFileFindings(t, loadRepository(t), nil)
	reportFindings(t, "composition boundaries", boundaryFindings(profilePackages))
}

func TestCompositionBoundariesHoldOnlyWiring(t *testing.T) {
	for _, rel := range compositionBoundaries {
		reportFindings(t, rel+" wiring", wiringFindings(productCheck(t, publicProfile, rel), productCheck(t, developmentProfile, rel)))
	}
}

func TestDevelopmentModulesStayOutOfPublic(t *testing.T) {
	r := loadRepository(t)
	reportFindings(t, "Development links", developmentLinkFindings(r.executableDependencies(t, publicProfile, ""), r.executableDependencies(t, developmentProfile, "")))
	reportFindings(t, "Development imports", developmentImportFindings(t, r, nil))
}

func TestCoreHoldsNoDevelopmentImplementation(t *testing.T) {
	findings, reviewed := coreServiceFindings(productCheck(t, publicProfile, corePackage), coreUsers(t, loadRepository(t)))
	reportFindings(t, "core declarations", findings)
	reportStaleEntries(t, "reviewedDevelopmentEntryPoints", reviewedDevelopmentEntryPoints, reviewed)
}

func TestObservationPointsAreOneWay(t *testing.T) {
	findings, results, pointers := observationFindings(productCheck(t, publicProfile, corePackage))
	reportFindings(t, "observation points", findings)
	reportStaleEntries(t, "reviewedObservationResults", reviewedObservationResults, results)
	reportStaleEntries(t, "reviewedObservationPointers", reviewedObservationPointers, pointers)
}

func TestDevelopmentCodeDoesNotWriteCoreState(t *testing.T) {
	seen := map[string]bool{}
	for _, rel := range developmentModules {
		findings, reviewed := developmentWriteFindings(productCheck(t, developmentProfile, rel), modulePath+"/"+corePackage)
		reportFindings(t, rel+" Development writes", findings)
		for name := range reviewed {
			seen[name] = true
		}
	}
	reportStaleEntries(t, "reviewedDevelopmentWrites", reviewedDevelopmentWrites, seen)
}

func TestCoreProcessorFieldsAreReadByTheCore(t *testing.T) {
	findings, reviewed := coreFieldFindings(productCheck(t, publicProfile, corePackage))
	reportFindings(t, "core fields", findings)
	reportStaleEntries(t, "reviewedUnreadCoreFields", reviewedUnreadCoreFields, reviewed)
}

func TestResearchSchemaStaysInTheDevelopmentModule(t *testing.T) {
	reportFindings(t, "research schema", researchSchemaFindings(productCheck(t, publicProfile, corePackage), productCheck(t, developmentProfile, researchModule)))
}

func TestDevelopmentDiagnosticFlagsArePreserved(t *testing.T) {
	lines := map[*checkedPackage]map[string]bool{}
	for rel, shared := range publicCommandLines {
		lines[productCheck(t, publicProfile, rel)] = shared
	}
	for rel := range developmentFlags {
		reportFindings(t, rel+" Development flags", developmentFlagFindings(productCheck(t, developmentProfile, rel), lines))
	}
}

// behavioralContracts are the tests that cover what no static check sees: diagnostic failures injected into the
// Development outputs, core results with and without the research schema, and the core processor holding no
// Development state. They keep their names, so a refactoring cannot drop them silently.
var behavioralContracts = map[string][]string{
	"tests/devcapture/diagnostic_isolation_dev_test.go": {"TestFailedConsoleAndAuditWritesKeepTheDamageCounters", "TestFailedBuffLifecycleRowsDoNotSkipEffectUpdates"},
	"tests/devcapture/client_push_dev_test.go":          {"TestResearchRoleReportDoesNotChangeTheLocalRoleEntity"},
	"tests/capture/processor_state_test.go":             {"TestProcessorHasNoDevelopmentState"},
}

func TestBehavioralContractsExist(t *testing.T) {
	r := loadRepository(t)
	for path, tests := range behavioralContracts {
		data, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(path)))
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		for _, name := range tests {
			if !strings.Contains(string(data), "func "+name+"(") {
				t.Errorf("%s no longer defines %s", path, name)
			}
		}
	}
}

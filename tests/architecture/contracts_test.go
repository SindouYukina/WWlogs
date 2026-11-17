package architecture

import (
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"sort"
	"strings"
	"sync"
	"testing"
)

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

// profileFileFindings checks every product file; it also returns the packages that contain profile-specific files.
func profileFileFindings(t testing.TB, r *repository, overrides map[string]string) (findings []string, profilePackages map[string]bool) {
	t.Helper()
	profilePackages = map[string]bool{}
	for _, pkg := range r.productPackages(t, publicProfile) {
		rel := r.relativeDir(pkg.Dir)
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
	sort.Strings(findings)
	return findings, profilePackages
}

// ---------------------------------------------------------------------------------------------------------------
// Declarations of a checked package, keyed so that the two profiles' declarations of one name match.

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

// fieldOwners maps every field of the package's named struct types to the type's name.
func fieldOwners(c *checkedPackage) map[*types.Var]string {
	owners := map[*types.Var]string{}
	if c.pkg == nil {
		return owners
	}
	for _, name := range c.pkg.Scope().Names() {
		typeName, ok := c.pkg.Scope().Lookup(name).(*types.TypeName)
		if !ok {
			continue
		}
		if structType, ok := typeName.Type().Underlying().(*types.Struct); ok {
			for i := 0; i < structType.NumFields(); i++ {
				owners[structType.Field(i)] = name
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
		if o.Parent() == o.Pkg().Scope() {
			return "func " + o.Name()
		}
	case *types.TypeName:
		if o.Parent() == o.Pkg().Scope() {
			return "type " + o.Name()
		}
	case *types.Const:
		if o.Parent() == o.Pkg().Scope() {
			return "const " + o.Name()
		}
	case *types.Var:
		if o.IsField() {
			if owner, ok := owners[o]; ok {
				return "field " + owner + "." + o.Name()
			}
			return ""
		}
		if o.Parent() == o.Pkg().Scope() {
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

// profileOnly lists the declarations a profile has only in its profile-specific files.
func profileOnly(c *checkedPackage) map[string]declaration {
	want := developmentFile
	if c.profile.name == publicProfile.name {
		want = publicFile
	}
	only := map[string]declaration{}
	for key, decl := range declarations(c) {
		if decl.kind == want {
			only[key] = decl
		}
	}
	return only
}

// ---------------------------------------------------------------------------------------------------------------
// Contract 2: shared code reaches profile code only through split declarations. A shared file may use a
// profile-specific declaration only when both profiles declare it (a hook with its Public no-op, the Development
// state type with its empty Public form). Anything else would make one profile fail to compile; both profiles must
// also type-check without error.

func sharedReferenceFindings(public, development *checkedPackage) []string {
	var findings []string
	for _, c := range []*checkedPackage{public, development} {
		for _, message := range c.typeErrors {
			findings = append(findings, fmt.Sprintf("%s profile does not type-check: %s", c.profile.name, message))
		}
	}
	publicOnly, developmentOnly := profileOnly(public), profileOnly(development)
	for _, pair := range []struct {
		c           *checkedPackage
		own, others map[string]declaration
	}{{development, developmentOnly, publicOnly}, {public, publicOnly, developmentOnly}} {
		owners := fieldOwners(pair.c)
		seen := map[string]bool{}
		for _, name := range pair.c.sortedFileNames() {
			if pair.c.kinds[name] != sharedFile {
				continue
			}
			ast.Inspect(pair.c.files[name], func(n ast.Node) bool {
				ident, ok := n.(*ast.Ident)
				if !ok {
					return true
				}
				obj := pair.c.info.Uses[ident]
				if obj == nil || obj.Pkg() != pair.c.pkg {
					return true
				}
				key := objectKey(obj, owners)
				if _, own := pair.own[key]; !own || key == "" {
					return true
				}
				if _, split := pair.others[key]; split {
					return true
				}
				message := fmt.Sprintf("%s: shared code uses %s, which only the %s profile declares (%s); reach profile code through a declaration both profiles have", pair.c.where(ident.Pos()), key, pair.c.profile.name, pair.own[key].where)
				if !seen[message] {
					seen[message] = true
					findings = append(findings, message)
				}
				return true
			})
		}
	}
	sort.Strings(findings)
	return findings
}

// ---------------------------------------------------------------------------------------------------------------
// Contract 3: split declarations are one-way. Both profiles declare a split function or method with the same
// signature, and it returns nothing: a Development hook observes the core, and nothing it computes flows back into the
// core's decisions. A split declaration that does return a value, and a split constant or variable, is a reviewed
// profile capability listed below with the reason it cannot change combat results. A split struct type is empty in the
// Public profile, so shared code can read no profile-specific field of it.

// reviewedProfileValues are the split declarations that carry a profile-specific value into shared code.
var reviewedProfileValues = map[string]string{
	"internal/buildinfo const profile":                  "build identity: the profile name the build reports (Settings/About, -version); no combat decision reads it",
	"internal/capture func newProcessorDiagnostics":     "constructs the Development state of a processor; the Public form is the empty struct",
	"internal/capture func openSessionDiagnostics":      "capability gate: Public refuses a Development diagnostics request instead of opening a spool",
	"internal/capture method sessionDiagnostics.finish": "summary text of the Development spool for the session result; Public returns the empty string",
	"internal/desktop func parseOptions":                "the entry point's flag parsing selects build-time capabilities; Public parses only the locale",
	"internal/desktop func webviewArguments":            "WebView2 debugging arguments, a Development tool; Public returns none",
}

// signatureShape prints a signature's receiver, parameter and result types without their names: a Public no-op
// usually leaves its parameters unnamed.
func signatureShape(signature *types.Signature, qualifier types.Qualifier) string {
	tuple := func(list *types.Tuple, variadic bool) string {
		parts := make([]string, list.Len())
		for i := 0; i < list.Len(); i++ {
			if variadic && i == list.Len()-1 {
				parts[i] = "..." + types.TypeString(list.At(i).Type().(*types.Slice).Elem(), qualifier)
				continue
			}
			parts[i] = types.TypeString(list.At(i).Type(), qualifier)
		}
		return "(" + strings.Join(parts, ", ") + ")"
	}
	shape := "func" + tuple(signature.Params(), signature.Variadic()) + " " + tuple(signature.Results(), false)
	if receiver := signature.Recv(); receiver != nil {
		shape = "(" + types.TypeString(receiver.Type(), qualifier) + ") " + shape
	}
	return shape
}

func splitDeclarationFindings(public, development *checkedPackage) (findings []string, reviewedSeen map[string]bool) {
	reviewedSeen = map[string]bool{}
	publicOnly, developmentOnly := profileOnly(public), profileOnly(development)
	qualifier := func(pkg *types.Package) types.Qualifier {
		return func(other *types.Package) string {
			if other == pkg {
				return ""
			}
			return other.Path()
		}
	}
	keys := make([]string, 0, len(developmentOnly))
	for key := range developmentOnly {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		dev := developmentOnly[key]
		pub, split := publicOnly[key]
		if !split {
			continue
		}
		name := development.rel + " " + key
		switch d := dev.obj.(type) {
		case *types.Func:
			devSignature := signatureShape(d.Type().(*types.Signature), qualifier(development.pkg))
			pubSignature := signatureShape(pub.obj.Type().(*types.Signature), qualifier(public.pkg))
			if devSignature != pubSignature {
				findings = append(findings, fmt.Sprintf("%s: Development %s (%s) and Public %s (%s) differ", name, devSignature, dev.where, pubSignature, pub.where))
			}
			if d.Type().(*types.Signature).Results().Len() > 0 {
				if _, reviewed := reviewedProfileValues[name]; reviewed {
					reviewedSeen[name] = true
				} else {
					findings = append(findings, fmt.Sprintf("%s (%s, %s) returns a value in both profiles: a split hook must return nothing, or its value must be reviewed as a profile capability that cannot change combat results", name, dev.where, pub.where))
				}
			}
		case *types.Const, *types.Var:
			if types.TypeString(d.Type(), qualifier(development.pkg)) != types.TypeString(pub.obj.Type(), qualifier(public.pkg)) {
				findings = append(findings, fmt.Sprintf("%s: the two profiles declare it with different types (%s, %s)", name, dev.where, pub.where))
			}
			if _, reviewed := reviewedProfileValues[name]; reviewed {
				reviewedSeen[name] = true
			} else {
				findings = append(findings, fmt.Sprintf("%s (%s, %s) gives shared code a profile-specific value: it must be reviewed as a profile capability that cannot change combat results", name, dev.where, pub.where))
			}
		case *types.TypeName:
			if structType, ok := pub.obj.Type().Underlying().(*types.Struct); ok && structType.NumFields() > 0 {
				findings = append(findings, fmt.Sprintf("%s: the Public form (%s) has %d fields; a split type must be empty in the Public profile so that shared code reads no profile state", name, pub.where, structType.NumFields()))
			}
		}
	}
	sort.Strings(findings)
	return findings, reviewedSeen
}

// ---------------------------------------------------------------------------------------------------------------
// Contract 4: Development code does not write core state. A Development function changes core memory only as a local
// copy. Through a pointer, a slice or a map (memory the core can see again) it writes only Development and research
// data: processor.dev, the research schema types, and types no shared code uses. It never writes a core processor
// field (every field but dev), a field of a type shared code uses (a combatEvent, or the effect row a hook received),
// a whole core value through its pointer, or a package variable of a shared file.
//
// Not visible to this check: a change made by calling a core method, and an element written through a local alias of a
// core map or slice. The failure-injection tests (tests/capture/diagnostic_isolation_dev_test.go) and the replay
// statistics equality of both profiles (scripts/verify.ps1) cover those by behavior.

// reviewedDevelopmentWrites are the writes of Development code to core state that configure a processor before or
// around processing (function, then field), each with the reason it cannot change combat results.
var reviewedDevelopmentWrites = map[string]string{
	"Main processor.masters":                           "entry point configuration: the Development CLI hands the processor the Master it loaded, as the Public Main does",
	"Main processor.census":                            "Deferred (Phase 3B R05): the command census is embedded in the core frame parser; only Development creates it, and the parser only reports to it",
	"Main processor.captureLayer":                      "the Development diagnostic pipeline reports its dropped records in the capture-layer health (operational status, not combat results)",
	"sessionDiagnostics.attach processor.captureLayer": "the same dropped-record counter for a Desktop Development session spool",
	"sessionDiagnostics.attach processor.diagnostic":   "a Desktop Development session routes the operational diagnostic stream into the spool console.log",
}

// coreStateType is the core state struct of the capture package; its dev field holds the Development state.
const (
	coreStateType        = "processor"
	developmentStateName = "dev"
)

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
// field selected through a pointer, or an element of a slice or map.
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

// coreTypes are the named types a shared file declares and shared code uses, by name or through a field or method,
// outside the research schema files: the types whose values the core holds. A type only Development code uses is
// Development data wherever it is declared.
func coreTypes(c *checkedPackage) map[string]bool {
	research, _ := researchSchemaFiles(c)
	owners := fieldOwners(c)
	methodOwners := map[types.Object]string{}
	for _, name := range c.pkg.Scope().Names() {
		if typeName, ok := c.pkg.Scope().Lookup(name).(*types.TypeName); ok {
			if named, ok := typeName.Type().(*types.Named); ok {
				for i := 0; i < named.NumMethods(); i++ {
					methodOwners[named.Method(i)] = name
				}
			}
		}
	}
	core := map[string]bool{}
	for ident, obj := range c.info.Uses {
		if obj == nil || obj.Pkg() != c.pkg {
			continue
		}
		if file := c.fileOf(ident.Pos()); c.kinds[file] != sharedFile || research[file] {
			continue
		}
		switch o := obj.(type) {
		case *types.TypeName:
			if o.Parent() == c.pkg.Scope() && !research[c.fileOf(o.Pos())] {
				core[o.Name()] = true
			}
		case *types.Var:
			if owner, ok := owners[o]; ok {
				core[owner] = true
			}
		case *types.Func:
			if owner, ok := methodOwners[o]; ok {
				core[owner] = true
			}
		}
	}
	// A type declared in a profile file (the Development state, a split type) is never core state.
	for name := range core {
		if typeName, ok := c.pkg.Scope().Lookup(name).(*types.TypeName); !ok || c.kindOf(typeName.Pos()) != sharedFile {
			delete(core, name)
		}
	}
	return core
}

func developmentWriteFindings(development *checkedPackage) (findings []string, reviewedSeen map[string]bool) {
	reviewedSeen = map[string]bool{}
	owners := fieldOwners(development)
	core := coreTypes(development)
	for _, name := range development.sortedFileNames() {
		if development.kinds[name] != developmentFile {
			continue
		}
		for _, decl := range development.files[name].Decls {
			function, ok := decl.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			targets, _ := writeTargets(development.info, function.Body)
			for _, target := range targets {
				root, chain := writtenChain(development.info, target)
				if ident, ok := root.(*ast.Ident); ok {
					if variable, ok := development.info.Uses[ident].(*types.Var); ok && variable.Pkg() == development.pkg && variable.Parent() == development.pkg.Scope() && development.kindOf(variable.Pos()) == sharedFile {
						findings = append(findings, fmt.Sprintf("%s: Development function %s writes the shared package variable %s (%s)", development.where(target.Pos()), functionLabel(function), variable.Name(), development.where(variable.Pos())))
						continue
					}
				}
				if !indirectWrite(development.info, target) {
					continue // the write changes a local copy or a Development variable
				}
				if star, ok := ast.Unparen(target).(*ast.StarExpr); ok && len(chain) == 0 {
					if named, ok := development.info.Types[star].Type.(*types.Named); ok && named.Obj().Pkg() == development.pkg && core[named.Obj().Name()] {
						findings = append(findings, fmt.Sprintf("%s: Development function %s replaces a %s, a core value, through a pointer; a Development observer may change only its own copy", development.where(target.Pos()), functionLabel(function), named.Obj().Name()))
					}
					continue
				}
				for _, selector := range chain {
					field := selectedField(development.info, selector)
					owner, ok := owners[field]
					if !ok {
						continue // a field of another package's type
					}
					if owner == coreStateType {
						if field.Name() == developmentStateName {
							continue
						}
						label := functionLabel(function) + " " + coreStateType + "." + field.Name()
						if _, reviewed := reviewedDevelopmentWrites[label]; reviewed {
							reviewedSeen[label] = true
						} else {
							findings = append(findings, fmt.Sprintf("%s: Development function %s writes the core state %s.%s; Development state belongs in %s.%s", development.where(target.Pos()), functionLabel(function), coreStateType, field.Name(), coreStateType, developmentStateName))
						}
						break
					}
					if core[owner] {
						findings = append(findings, fmt.Sprintf("%s: Development function %s writes %s.%s, a field of a core type, through a pointer, slice or map; a Development observer may change only its own copy", development.where(target.Pos()), functionLabel(function), owner, field.Name()))
						break
					}
				}
			}
		}
	}
	sort.Strings(findings)
	return findings, reviewedSeen
}

// ---------------------------------------------------------------------------------------------------------------
// Contract 5: every core processor field is read by shared code. A field that shared code only initializes, assigns,
// increments, appends to or deletes from, and that only Development code reads, is Development state in the core; it
// belongs in processor.dev. New core fields are welcome as long as the core reads them.

// reviewedUnreadCoreFields are core fields only Development code reads, each with the reason it stays in the core.
var reviewedUnreadCoreFields = map[string]string{
	"notifyStats": "Deferred (Phase 3B-4A): the core counts its own DamageExecuteNotify settlement decisions here, and the failure-injection tests compare it as a core result; only the Development summary prints it",
}

func coreFieldFindings(public *checkedPackage) (findings []string, reviewedSeen map[string]bool) {
	reviewedSeen = map[string]bool{}
	coreType, ok := public.pkg.Scope().Lookup(coreStateType).(*types.TypeName)
	if !ok {
		return []string{fmt.Sprintf("%s: the core state type %s is missing; update the architecture contract with the new core state", public.rel, coreStateType)}, reviewedSeen
	}
	structType, ok := coreType.Type().Underlying().(*types.Struct)
	if !ok {
		return []string{fmt.Sprintf("%s: %s is not a struct", public.rel, coreStateType)}, reviewedSeen
	}
	coreFields := map[*types.Var]bool{}
	for i := 0; i < structType.NumFields(); i++ {
		coreFields[structType.Field(i)] = true
	}
	written := map[*ast.Ident]bool{}
	reads := map[*types.Var]int{}
	for _, name := range public.sortedFileNames() {
		if public.kinds[name] != sharedFile {
			continue
		}
		file := public.files[name]
		targets, assignments := writeTargets(public.info, file)
		for _, target := range targets {
			_, chain := writtenChain(public.info, target)
			var assignedFields []*types.Var
			for _, selector := range chain {
				if field := selectedField(public.info, selector); coreFields[field] {
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
								if selectedField(public.info, selector) == field {
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
					if field, ok := public.info.Uses[key].(*types.Var); ok && coreFields[field] {
						written[key] = true
					}
				}
			}
			return true
		})
		ast.Inspect(file, func(n ast.Node) bool {
			if ident, ok := n.(*ast.Ident); ok {
				if field, ok := public.info.Uses[ident].(*types.Var); ok && coreFields[field] && !written[ident] {
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
			findings = append(findings, fmt.Sprintf("%s: the core field %s.%s is never read by shared code in the Public profile; Development state belongs in %s.%s, and unread core state should go", public.where(field.Pos()), coreStateType, field.Name(), coreStateType, developmentStateName))
		}
	}
	return findings, reviewedSeen
}

// ---------------------------------------------------------------------------------------------------------------
// Contract 6: the research protocol schema stays out of the core. The research schema (-protocol-schema) and the
// generic decoder's records are research evidence, not runtime authority (Phase 3A R03): only Development code and the
// files that declare them may use them. Their decoders are Development-only already; this keeps their types out of
// shared signatures, fields and logic too. The behavioral side (a role decoded with and without the schema gives the
// same core result) is tests/capture/client_push_dev_test.go.

// researchSchemaAnchors are types of the research schema files; every declaration of their files is research schema.
var researchSchemaAnchors = []string{"protocolSchema", "decodedMessage"}

// researchSchemaFiles are the files that declare the research schema anchors.
func researchSchemaFiles(c *checkedPackage) (files map[string]bool, findings []string) {
	files = map[string]bool{}
	for _, anchor := range researchSchemaAnchors {
		obj, ok := c.pkg.Scope().Lookup(anchor).(*types.TypeName)
		if !ok {
			findings = append(findings, fmt.Sprintf("%s: the research schema type %s is missing; update researchSchemaAnchors with the type that now marks the research schema files", c.rel, anchor))
			continue
		}
		files[c.fileOf(obj.Pos())] = true
	}
	return files, findings
}

func researchSchemaFindings(public *checkedPackage) []string {
	researchFiles, findings := researchSchemaFiles(public)
	for _, name := range public.sortedFileNames() {
		if public.kinds[name] != sharedFile || researchFiles[name] {
			continue
		}
		ast.Inspect(public.files[name], func(n ast.Node) bool {
			ident, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			obj := public.info.Uses[ident]
			if obj == nil || obj.Pkg() != public.pkg || !researchFiles[public.fileOf(obj.Pos())] {
				return true
			}
			findings = append(findings, fmt.Sprintf("%s: shared code uses the research schema declaration %s (%s); research schema evidence is not runtime authority", public.where(ident.Pos()), obj.Name(), public.where(obj.Pos())))
			return true
		})
	}
	sort.Strings(findings)
	return findings
}

// ---------------------------------------------------------------------------------------------------------------
// Contract 7: the Development diagnostic surface is preserved and stays out of Public. Every flag below is a
// Development diagnostic, research or test input of v0.8.0 (the Development diagnostic surface on 2026-11-17). Each
// must still be defined in a Development file, and no Public or shared file may define it. The PCAP writer package is
// linked by the Development executables and by no Public one. New Development flags need no change here; removing one
// is a deliberate decision that removes it from this list in the same change.

var developmentFlags = map[string][]string{
	"internal/capture": {
		"audit-out", "buff-event-out", "buff-overlap-out", "buff-snapshot-out", "bullet-ref-out", "capture-immediate",
		"client-push-out", "client-version-out", "combat-notify-audit-out", "combat-state-out", "command-census-out",
		"damage-push-out", "effect-state-out", "encounter-out", "encounter-summary-out", "event-jsonl-out",
		"frame-archive-out", "hp-state-out", "live-diagnostic-out", "master-db", "max-pcap-bytes", "part-ref-out",
		"pcap-out", "protocol-schema", "research", "role-config-out", "schema-decode-out", "shield-ref-out",
		"show-all-effect-events", "show-effect-events", "skill-ref-out", "status-file", "summon-owner-out",
	},
	"internal/desktop": {
		"asset-root", "autostart", "capture-diagnostics", "font-root", "history-db", "ip", "master-db", "overlay",
		"record-browser-url", "replay", "replay-pacing", "showcase", "showcase-overlays", "simulate-capture-environment",
		"simulate-npcap-offline", "simulate-ui-languages", "webview-args", "webview-debug-port",
	},
}

// developmentOnlyDependencies are packages only Development diagnostics need: the writer of the Development PCAP.
var developmentOnlyDependencies = []string{"github.com/google/gopacket/pcapgo"}

func developmentDependencyFindings(public, development map[string]bool) []string {
	var findings []string
	for _, pkg := range developmentOnlyDependencies {
		if public[pkg] {
			findings = append(findings, fmt.Sprintf("the Public executables (./cmd/...) link %s, which only Development diagnostics need", pkg))
		}
		if !development[pkg] {
			findings = append(findings, fmt.Sprintf("the Development executables (./cmd/...) no longer link %s: its Development diagnostic capability is gone", pkg))
		}
	}
	return findings
}

// flagNameArgument is the position of the flag name in the flag package's definition functions and methods.
var flagNameArgument = map[string]int{
	"Bool": 0, "BoolFunc": 0, "Duration": 0, "Float64": 0, "Func": 0, "Int": 0, "Int64": 0, "String": 0, "Uint": 0, "Uint64": 0,
	"BoolVar": 1, "DurationVar": 1, "Float64Var": 1, "IntVar": 1, "Int64Var": 1, "StringVar": 1, "UintVar": 1, "Uint64Var": 1, "TextVar": 1, "Var": 1,
}

// flagDefinitions lists the flags a checked package defines with the flag package, by file kind.
func flagDefinitions(c *checkedPackage) map[fileKind]map[string]string {
	defined := map[fileKind]map[string]string{}
	for _, name := range c.sortedFileNames() {
		kind := c.kinds[name]
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
			if defined[kind] == nil {
				defined[kind] = map[string]string{}
			}
			defined[kind][constant.StringVal(value)] = c.where(call.Pos())
			return true
		})
	}
	return defined
}

func developmentFlagFindings(public, development *checkedPackage) []string {
	var findings []string
	required := developmentFlags[development.rel]
	developmentDefined := flagDefinitions(development)[developmentFile]
	publicDefined := flagDefinitions(public)
	for _, name := range required {
		if _, ok := developmentDefined[name]; !ok {
			findings = append(findings, fmt.Sprintf("%s: the Development diagnostic flag -%s is no longer defined in a Development file", development.rel, name))
		}
		for _, kind := range []fileKind{publicFile, sharedFile} {
			if where, ok := publicDefined[kind][name]; ok {
				findings = append(findings, fmt.Sprintf("%s: a %s file defines the Development diagnostic flag -%s; Public builds must not offer it", where, kind, name))
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

func TestProfileFilesFollowTheirConstraints(t *testing.T) {
	findings, _ := profileFileFindings(t, loadRepository(t), nil)
	reportFindings(t, "profile files", findings)
}

func TestProfileFilesAreAnalyzed(t *testing.T) {
	_, profilePackages := profileFileFindings(t, loadRepository(t), nil)
	analyzed := map[string]bool{}
	for _, rel := range analyzedPackages {
		analyzed[rel] = true
	}
	for rel := range profilePackages {
		if !analyzed[rel] {
			t.Errorf("%s has profile-specific files but is not in analyzedPackages, so no contract checks it", rel)
		}
	}
	for _, rel := range analyzedPackages {
		if !profilePackages[rel] {
			t.Errorf("%s is in analyzedPackages but has no profile-specific file any more; remove it", rel)
		}
	}
}

func TestSharedCodeReachesProfileCodeOnlyThroughSplitDeclarations(t *testing.T) {
	for _, rel := range analyzedPackages {
		reportFindings(t, rel+" shared references", sharedReferenceFindings(productCheck(t, publicProfile, rel), productCheck(t, developmentProfile, rel)))
	}
}

func TestSplitDeclarationsAreOneWay(t *testing.T) {
	seen := map[string]bool{}
	for _, rel := range analyzedPackages {
		findings, reviewed := splitDeclarationFindings(productCheck(t, publicProfile, rel), productCheck(t, developmentProfile, rel))
		reportFindings(t, rel+" split declarations", findings)
		for name := range reviewed {
			seen[name] = true
		}
	}
	reportStaleEntries(t, "reviewedProfileValues", reviewedProfileValues, seen)
}

func TestDevelopmentCodeDoesNotWriteCoreState(t *testing.T) {
	findings, reviewed := developmentWriteFindings(productCheck(t, developmentProfile, "internal/capture"))
	reportFindings(t, "Development writes", findings)
	reportStaleEntries(t, "reviewedDevelopmentWrites", reviewedDevelopmentWrites, reviewed)
}

func TestCoreProcessorFieldsAreReadByTheCore(t *testing.T) {
	findings, reviewed := coreFieldFindings(productCheck(t, publicProfile, "internal/capture"))
	reportFindings(t, "core fields", findings)
	reportStaleEntries(t, "reviewedUnreadCoreFields", reviewedUnreadCoreFields, reviewed)
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

func TestResearchSchemaStaysOutOfTheCore(t *testing.T) {
	reportFindings(t, "research schema", researchSchemaFindings(productCheck(t, publicProfile, "internal/capture")))
}

func TestDevelopmentDiagnosticFlagsArePreserved(t *testing.T) {
	for rel := range developmentFlags {
		reportFindings(t, rel+" Development flags", developmentFlagFindings(productCheck(t, publicProfile, rel), productCheck(t, developmentProfile, rel)))
	}
}

func TestDevelopmentOnlyDependenciesStayOutOfPublic(t *testing.T) {
	r := loadRepository(t)
	reportFindings(t, "Development-only dependencies", developmentDependencyFindings(r.executableDependencies(t, publicProfile, ""), r.executableDependencies(t, developmentProfile, "")))
}

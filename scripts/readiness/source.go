package readiness

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The protocol domain checks the generated wire numbers and the absence of any second protocol: the runtime decodes the
// current game version only (AGENTS.md 4.4 runtime version policy), so an old decoder, a translation table, a fallback or
// a second version constant is a failed adaptation even when every test passes.

const (
	numbersFile     = "internal/capture/protocol_numbers.go"
	testNumbersFile = "tests/capture/protocol_numbers_test.go"
)

// protocolConstant is one generated wire number with the client's minified key from its comment.
type protocolConstant struct {
	file  string
	name  string
	value int
	key   string
	kind  string // "command" or "field"
}

type protocolSource struct {
	gameVersion    string
	protocolClient string
	protocolSHA256 string
	netDefineSHA   string
	constants      []protocolConstant
}

var (
	reGameVersion    = regexp.MustCompile(`(?m)^const currentGameVersion = "([^"]*)"`)
	reProtocolClient = regexp.MustCompile(`(?m)^const currentProtocolClient = "([^"]*)"`)
	reProtocolHash   = regexp.MustCompile(`Protocol\.js \(SHA-256 ([0-9a-f]{64})\)`)
	reNetDefineHash  = regexp.MustCompile(`NetDefine\.js[\s/]*\(SHA-256 ([0-9a-f]{64})\)`)
	reConstant       = regexp.MustCompile(`(?m)^\s*([Cc]md|[Ff]ield)([A-Za-z0-9_]+)\s*=\s*(\d+)\s*//\s*(\S+)\s*$`)
	// A wire number outside the generated files: a command or field constant assigned a literal. The generated constants
	// are cmd*/field*, or Cmd*/Field* when the Development module (internal/devcapture) reads them from the core.
	reStrayNumber = regexp.MustCompile(`(?m)^\s*(?:const\s+)?([Cc]md|[Ff]ield)[A-Z][A-Za-z0-9_]*\s*=\s*\d+\b`)
	// A game version literal held by any identifier but the generated one, in Go or TypeScript.
	reVersionLiteral = regexp.MustCompile(`(?i)\b(\w*gameversion\w*)\s*(?::=|=|:)\s*(?:string\s*=\s*)?["'](\d+\.\d+(?:\.\d+)?)["']`)
	// File names of retired protocol layers.
	reRetiredLayer = regexp.MustCompile(`(?i)(protocol_?translation|legacy_?(protocol|decoder)|protocol_?fallback|translation_\d+\.\d+)`)
)

func readText(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n"), nil
}

func parseConstants(file, text string) []protocolConstant {
	var out []protocolConstant
	for _, match := range reConstant.FindAllStringSubmatch(text, -1) {
		value, err := strconv.Atoi(match[3])
		if err != nil {
			continue
		}
		kind := "command"
		if strings.EqualFold(match[1], "field") {
			kind = "field"
		}
		out = append(out, protocolConstant{file: file, name: match[1] + match[2], value: value, key: match[4], kind: kind})
	}
	return out
}

// productSources lists the product source files under internal/ and cmd/ (and the frontend sources for version
// literals), relative to root with forward slashes. Test files never live there (AGENTS.md 5.1), but are skipped anyway.
func productSources(root string, dirs []string, exts map[string]bool) ([]string, error) {
	var files []string
	for _, dir := range dirs {
		base := filepath.Join(root, filepath.FromSlash(dir))
		if _, err := os.Stat(base); err != nil {
			continue
		}
		err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if name := entry.Name(); name == "node_modules" || name == "dist" {
					return filepath.SkipDir
				}
				return nil
			}
			if !exts[filepath.Ext(path)] || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	return files, nil
}

func (g *gate) protocolChecks() {
	single := g.check("protocol.single-native-decoder", DomainProtocol, "One generated native decoder table; no retired protocol layer", KindStatic, true)
	format := g.check("protocol.version-format", DomainProtocol, "The decoded game version is one major.minor and matches the protocol's client", KindStatic, true)
	keys := g.check("protocol.schema-keys", DomainProtocol, "Every generated wire number resolves to the same number in the current client schema", KindResearch, true)

	text, err := readText(filepath.Join(g.in.Root, filepath.FromSlash(numbersFile)))
	if err != nil {
		single.fail("%s cannot be read: %v", numbersFile, err)
		format.unverified("the generated numbers file is missing")
		keys.unverified("the generated numbers file is missing")
		return
	}
	src := &protocolSource{constants: parseConstants(numbersFile, text)}
	g.source = src
	if !strings.HasPrefix(text, "// Code generated from ") || !strings.Contains(text, "DO NOT EDIT") {
		single.fail("%s does not carry the generator's header", numbersFile)
	}
	if m := reGameVersion.FindStringSubmatch(text); m != nil {
		src.gameVersion = m[1]
	}
	if m := reProtocolClient.FindStringSubmatch(text); m != nil {
		src.protocolClient = m[1]
	}
	if m := reProtocolHash.FindStringSubmatch(text); m != nil {
		src.protocolSHA256 = m[1]
	}
	if m := reNetDefineHash.FindStringSubmatch(text); m != nil {
		src.netDefineSHA = m[1]
	}
	if testText, err := readText(filepath.Join(g.in.Root, filepath.FromSlash(testNumbersFile))); err == nil {
		src.constants = append(src.constants, parseConstants(testNumbersFile, testText)...)
	}
	g.report.GameVersion, g.report.ProtocolClient = src.gameVersion, src.protocolClient
	single.Evidence = append(single.Evidence, fmt.Sprintf("%s: %d generated constants (with %s), game version %q, client %q", numbersFile, len(src.constants), testNumbersFile, src.gameVersion, src.protocolClient))

	goFiles, err := productSources(g.in.Root, []string{"internal", "cmd"}, map[string]bool{".go": true, ".json": true})
	if err != nil {
		single.fail("the product sources cannot be listed: %v", err)
	}
	definitions := 0
	for _, file := range goFiles {
		if reRetiredLayer.MatchString(filepath.Base(file)) {
			single.fail("%s: a retired protocol layer (translation, legacy or fallback) is part of the product", file)
		}
		if !strings.HasSuffix(file, ".go") {
			continue
		}
		body, err := readText(filepath.Join(g.in.Root, filepath.FromSlash(file)))
		if err != nil {
			single.fail("%s cannot be read: %v", file, err)
			continue
		}
		definitions += len(reGameVersion.FindAllString(body, -1))
		if file != numbersFile {
			if reGameVersion.MatchString(body) {
				single.fail("%s defines currentGameVersion outside the generated file", file)
			}
			if loc := reStrayNumber.FindStringIndex(body); loc != nil && strings.HasPrefix(file, "internal/capture/") {
				line := strings.Count(body[:loc[0]], "\n") + 1
				single.fail("%s:%d assigns a wire number outside the generated file", file, line)
			}
		}
	}
	if definitions != 1 {
		single.fail("currentGameVersion is defined %d times in the product (want exactly once, in %s)", definitions, numbersFile)
	}

	switch {
	case !regexp.MustCompile(`^\d+\.\d+$`).MatchString(src.gameVersion):
		format.fail("currentGameVersion %q is not a major.minor version", src.gameVersion)
	case majorMinor(lastVersionIn(src.protocolClient)) != src.gameVersion:
		format.fail("currentProtocolClient %q does not belong to game version %s", src.protocolClient, src.gameVersion)
	default:
		format.Evidence = append(format.Evidence, fmt.Sprintf("game version %s, protocol client %s", src.gameVersion, src.protocolClient))
	}

	g.schemaKeys(keys, src)
}

var reVersion = regexp.MustCompile(`\d+\.\d+(?:\.\d+)*`)

// lastVersionIn returns the last dotted version number in a label ("Global 3.8.9" -> "3.8.9").
func lastVersionIn(label string) string {
	all := reVersion.FindAllString(label, -1)
	if len(all) == 0 {
		return ""
	}
	return all[len(all)-1]
}

// clientSchema is the minified schema protojs_schema.py writes from the client's Protocol.js and NetDefine.js.
type clientSchema struct {
	ProtocolSHA256  string `json:"protocolSha256"`
	NetDefineSHA256 string `json:"netDefineSha256"`
	// Messages maps a minified message name to its fields by number; Commands maps a command number to its message.
	Messages map[string]map[string]schemaField `json:"messages"`
	Commands map[string]string                 `json:"commands"`
}

type schemaField struct {
	Name string `json:"name"`
}

func (g *gate) schemaKeys(c *Check, src *protocolSource) {
	if g.in.ProtocolSchema == "" {
		c.unverified("no client protocol schema was supplied (research input protocolSchema; PROCEDURE.md S2)")
		return
	}
	data, err := os.ReadFile(g.in.ProtocolSchema)
	if err != nil {
		c.unverified(fmt.Sprintf("the client protocol schema cannot be read: %v", err))
		return
	}
	var schema clientSchema
	if err := json.Unmarshal(data, &schema); err != nil {
		c.fail("the client protocol schema is not a protojs_schema.py schema: %v", err)
		return
	}
	commands, messages := schema.Commands, schema.Messages
	c.Evidence = append(c.Evidence, fmt.Sprintf("schema %s: %d messages, %d commands", filepath.Base(g.in.ProtocolSchema), len(messages), len(commands)))
	if len(messages) == 0 || len(commands) == 0 {
		c.fail("the client protocol schema lists no messages or commands")
		return
	}
	if schema.ProtocolSHA256 != src.protocolSHA256 || schema.NetDefineSHA256 != src.netDefineSHA {
		c.fail("the generated numbers come from Protocol.js %s / NetDefine.js %s, the schema from %s / %s: regenerate (PROCEDURE.md S3)",
			short(src.protocolSHA256), short(src.netDefineSHA), short(schema.ProtocolSHA256), short(schema.NetDefineSHA256))
	}
	matched := 0
	for _, constant := range src.constants {
		number := strconv.Itoa(constant.value)
		switch constant.kind {
		case "command":
			if commands[number] != constant.key {
				c.fail("%s %s = %d: the client's command %d is %q, not %q", constant.file, constant.name, constant.value, constant.value, commands[number], constant.key)
				continue
			}
		case "field":
			dot := strings.LastIndex(constant.key, ".")
			if dot <= 0 {
				c.fail("%s %s: comment %q is not message.property", constant.file, constant.name, constant.key)
				continue
			}
			message, property := constant.key[:dot], constant.key[dot+1:]
			fields, ok := messages[message]
			if !ok {
				c.fail("%s %s: message %q is not in the client schema", constant.file, constant.name, message)
				continue
			}
			if fields[number].Name != property {
				c.fail("%s %s = %d: field %d of %s is %q, not %q", constant.file, constant.name, constant.value, constant.value, message, fields[number].Name, property)
				continue
			}
		}
		matched++
	}
	c.Coverage = &Coverage{Passed: matched, Total: len(src.constants), Denominator: "generated wire constants (product and test files)"}
}

func short(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	if hash == "" {
		return "(none)"
	}
	return hash
}

// versionSourceCheck finds game version literals held anywhere but the generated constant: History, the desktop host and
// the frontend must take the version from capture.CurrentGameVersion (through the stored protocol session), never keep
// their own, or a new major.minor would leave them recording the old one.
func (g *gate) versionSourceCheck() {
	c := g.check("runtime.history-version-source", DomainRuntime, "History and every view take the game version from the generated constant only", KindStatic, true)
	files, err := productSources(g.in.Root, []string{"internal", "cmd", "frontend/src"}, map[string]bool{".go": true, ".ts": true, ".tsx": true})
	if err != nil {
		c.fail("the product sources cannot be listed: %v", err)
		return
	}
	scanned, clean := 0, 0
	for _, file := range files {
		if file == numbersFile {
			continue
		}
		body, err := readText(filepath.Join(g.in.Root, filepath.FromSlash(file)))
		if err != nil {
			c.fail("%s cannot be read: %v", file, err)
			continue
		}
		scanned++
		matches := reVersionLiteral.FindAllStringSubmatchIndex(body, -1)
		if len(matches) == 0 {
			clean++
		}
		for _, match := range matches {
			line := strings.Count(body[:match[0]], "\n") + 1
			c.fail("%s:%d %s holds the game version literal %q", file, line, body[match[2]:match[3]], body[match[4]:match[5]])
		}
	}
	c.Coverage = &Coverage{Passed: clean, Total: scanned, Denominator: "product Go and frontend source files without their own game version literal"}
}

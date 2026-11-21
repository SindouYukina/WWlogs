//go:build ignore

// readiness runs the version readiness gate (scripts/readiness/README.md) and writes its report:
//
//	go run -overlay=tests/go-overlay.json scripts/readiness.go -out <dir> [-research-inputs <json>] [-evidence <dir>] [-live-acceptance <json>]
//
// scripts/readiness.ps1 is the usual entry point. The exit code is 0 only when the version is STATIC_READY; 1 when it is
// not; 2 when the gate itself could not run.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"wwlogs/scripts/readiness"
)

func main() {
	root := flag.String("root", ".", "repository root")
	out := flag.String("out", "", "directory for readiness.json and readiness.md (required)")
	research := flag.String("research-inputs", "", "private research inputs file, read by the research extension (optional)")
	evidence := flag.String("evidence", "", "directory written by scripts/verify.ps1 -EvidenceOut (optional)")
	live := flag.String("live-acceptance", "", "the owner's Live acceptance record (optional)")
	flag.Parse()
	if err := run(*root, *out, *research, *evidence, *live); err != nil {
		fmt.Fprintf(os.Stderr, "readiness: %v\n", err)
		os.Exit(2)
	}
}

func run(root, out, research, evidence, live string) error {
	if out == "" {
		return errors.New("-out is required")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	in := readiness.Inputs{Root: root, LiveAcceptance: live}
	if research != "" {
		// Only a research extension reads the file's fields; one that is missing or no JSON object stops the gate here.
		path, err := filepath.Abs(research)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read research inputs: %w", err)
		}
		var fields map[string]any
		if err := json.Unmarshal(data, &fields); err != nil {
			return fmt.Errorf("parse research inputs: %w", err)
		}
		in.ResearchInputs = path
	}
	if evidence != "" {
		for target, name := range map[*string]string{&in.VerifyResults: "verify-results.json", &in.GoldenCoverage: "golden-coverage.json", &in.HistorySummary: "history-summary.json"} {
			if path := filepath.Join(evidence, name); fileExists(path) {
				*target = path
			}
		}
	}
	report, err := readiness.Run(in)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	for name, write := range map[string]func(*os.File) error{
		"readiness.json": func(f *os.File) error { return report.WriteJSON(f) },
		"readiness.md":   func(f *os.File) error { return report.WriteMarkdown(f) },
	} {
		file, err := os.Create(filepath.Join(out, name))
		if err != nil {
			return err
		}
		if err := write(file); err != nil {
			file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
	}
	for _, d := range report.Domains {
		fmt.Printf("%-15s %s (%d checks)\n", d.ID, d.Status, d.Checks)
	}
	for _, c := range report.Checks {
		coverage := ""
		if c.Coverage != nil {
			coverage = fmt.Sprintf(" %d/%d", c.Coverage.Passed, c.Coverage.Total)
		}
		fmt.Printf("  %-14s %-42s%s\n", c.Status, c.ID, coverage)
	}
	fmt.Printf("STATIC_READY %v, LIVE_VERIFIED %v; report %s\n", report.StaticReady, report.LiveVerified, filepath.Join(out, "readiness.md"))
	if !report.StaticReady {
		os.Exit(1)
	}
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

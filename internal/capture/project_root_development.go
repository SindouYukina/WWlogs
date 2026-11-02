//go:build wwdev

package capture

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// diagnosticLogsDirectory returns the project-root logs directory that receives every persistent
// development diagnostic. There is deliberately no fallback to a user profile or temporary directory:
// when the project root cannot be established the caller must stop and report why.
func diagnosticLogsDirectory() (string, error) {
	executable, _ := os.Executable()
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	workingDirectory, _ := os.Getwd()
	root, err := resolveProjectRoot(executable, workingDirectory)
	if err != nil {
		return "", err
	}
	logs := filepath.Join(root, "logs")
	if err := requireIgnoredLogsDirectory(root); err != nil {
		return "", err
	}
	return logs, nil
}

// resolveProjectRoot finds the WWlogs repository root. The executable's own location is tried first
// (every ancestor, so a build under bin/ or work/dist works), then the working directory's ancestors.
// The working directory alone never decides the root while the executable lives inside a project.
func resolveProjectRoot(executablePath, workingDirectory string) (string, error) {
	var searched []string
	for _, start := range []string{filepath.Dir(executablePath), workingDirectory} {
		if start == "" || start == "." {
			continue
		}
		for directory := filepath.Clean(start); ; {
			if isProjectRoot(directory) {
				return directory, nil
			}
			parent := filepath.Dir(directory)
			if parent == directory {
				break
			}
			directory = parent
		}
		searched = append(searched, start)
	}
	return "", fmt.Errorf("cannot determine the WWlogs project root from the executable location or the working directory (searched %s); "+
		"run a development build from inside the repository (for example bin/ or work/dist) so diagnostics can be written to its logs directory",
		strings.Join(searched, ", "))
}

// isProjectRoot recognizes the repository by its module declaration and product entry point, so an
// unrelated directory that happens to contain a go.mod is not mistaken for it.
func isProjectRoot(directory string) bool {
	file, err := os.Open(filepath.Join(directory, "go.mod"))
	if err != nil {
		return false
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	module := ""
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); strings.HasPrefix(line, "module ") {
			module = strings.TrimSpace(strings.TrimPrefix(line, "module "))
			break
		}
	}
	if module != "wwlogs" {
		return false
	}
	info, err := os.Stat(filepath.Join(directory, "cmd", "capture"))
	return err == nil && info.IsDir()
}

// requireIgnoredLogsDirectory refuses to write diagnostics unless the repository's .gitignore ignores the
// root logs directory, so raw captures cannot be staged by accident.
func requireIgnoredLogsDirectory(root string) error {
	file, err := os.Open(filepath.Join(root, ".gitignore"))
	if err != nil {
		return fmt.Errorf("read .gitignore to confirm that logs/ is ignored: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		switch strings.TrimSpace(scanner.Text()) {
		case "/logs/", "/logs", "logs/", "logs":
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return errors.New("logs/ is not listed in .gitignore; refusing to write development diagnostics there")
}

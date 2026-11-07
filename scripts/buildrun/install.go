package buildrun

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Artifact is one executable that a run must produce.
type Artifact struct {
	Profile string
	Output  string // final installed path
}

// Builder builds and verifies one artifact at staged, a temporary path beside its final location.
type Builder func(artifact Artifact, staged string) error

// Staged holds every verified executable of a run before any of them is installed.
type Staged struct {
	entries []stagedEntry
}

type stagedEntry struct {
	artifact Artifact
	path     string
}

// Stage builds every artifact. When any build or verification fails, everything staged so far is removed and no
// installed file has been touched.
func Stage(artifacts []Artifact, build Builder) (*Staged, error) {
	if len(artifacts) == 0 {
		return nil, errors.New("a build run needs at least one executable")
	}
	seen := map[string]bool{}
	staged := &Staged{}
	for _, artifact := range artifacts {
		output, err := filepath.Abs(artifact.Output)
		if err != nil {
			staged.Discard()
			return nil, err
		}
		key := filepath.Clean(output)
		if seen[key] {
			staged.Discard()
			return nil, fmt.Errorf("two executables of one run share the output %s", output)
		}
		seen[key] = true
		artifact.Output = output
		if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
			staged.Discard()
			return nil, fmt.Errorf("create output directory: %w", err)
		}
		path, err := reservePath(filepath.Dir(output), ".wwlogs-staged-*.exe")
		if err != nil {
			staged.Discard()
			return nil, fmt.Errorf("prepare staged executable: %w", err)
		}
		staged.entries = append(staged.entries, stagedEntry{artifact: artifact, path: path})
		if err := build(artifact, path); err != nil {
			staged.Discard()
			return nil, fmt.Errorf("%s: %w", artifact.Profile, err)
		}
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			staged.Discard()
			return nil, fmt.Errorf("%s: the builder produced no executable", artifact.Profile)
		}
	}
	return staged, nil
}

// Paths returns each staged executable by profile.
func (s *Staged) Paths() map[string]string {
	paths := map[string]string{}
	for _, entry := range s.entries {
		paths[entry.artifact.Profile] = entry.path
	}
	return paths
}

// Discard removes every staged executable.
func (s *Staged) Discard() {
	for _, entry := range s.entries {
		os.Remove(entry.path)
	}
	s.entries = nil
}

// Installation is an installed run whose previous executables are still kept for a rollback.
type Installation struct {
	entries []installedEntry
}

type installedEntry struct {
	output string
	backup string // empty when no previous executable existed
}

// Install moves every staged executable to its final path. Either all are installed or, after a failure, the
// previous executables are restored; rolledBack reports whether that restore fully succeeded.
func (s *Staged) Install() (installation *Installation, rolledBack bool, err error) {
	installation = &Installation{}
	for _, entry := range s.entries {
		installed, err := installOne(entry)
		if err != nil {
			restoreErr := installation.Rollback()
			s.Discard()
			if restoreErr != nil {
				return nil, false, fmt.Errorf("install %s: %v; restoring the previous executables failed: %w", entry.artifact.Output, err, restoreErr)
			}
			return nil, true, fmt.Errorf("install %s: %w", entry.artifact.Output, err)
		}
		installation.entries = append(installation.entries, installed)
	}
	s.entries = nil
	return installation, true, nil
}

func installOne(entry stagedEntry) (installedEntry, error) {
	result := installedEntry{output: entry.artifact.Output}
	if _, err := os.Stat(entry.artifact.Output); err == nil {
		backup, err := reservePath(filepath.Dir(entry.artifact.Output), ".wwlogs-previous-*.exe")
		if err != nil {
			return result, err
		}
		if err := os.Rename(entry.artifact.Output, backup); err != nil {
			return result, err
		}
		result.backup = backup
	}
	if err := os.Rename(entry.path, entry.artifact.Output); err != nil {
		if result.backup != "" {
			if restoreErr := os.Rename(result.backup, entry.artifact.Output); restoreErr != nil {
				return result, fmt.Errorf("%v; restoring the previous executable failed: %w", err, restoreErr)
			}
		}
		return result, err
	}
	return result, nil
}

// Rollback removes the newly installed executables and restores the previous ones, in reverse order.
func (i *Installation) Rollback() error {
	var problems []error
	for index := len(i.entries) - 1; index >= 0; index-- {
		entry := i.entries[index]
		if err := os.Remove(entry.output); err != nil && !os.IsNotExist(err) {
			problems = append(problems, err)
			continue
		}
		if entry.backup != "" {
			if err := os.Rename(entry.backup, entry.output); err != nil {
				problems = append(problems, err)
			}
		}
	}
	i.entries = nil
	return errors.Join(problems...)
}

// Finalize deletes the previous executables once the run is recorded as complete.
func (i *Installation) Finalize() {
	for _, entry := range i.entries {
		if entry.backup != "" {
			os.Remove(entry.backup)
		}
	}
	i.entries = nil
}

// Outputs returns the installed paths.
func (i *Installation) Outputs() []string {
	outputs := make([]string, 0, len(i.entries))
	for _, entry := range i.entries {
		outputs = append(outputs, entry.output)
	}
	return outputs
}

// reservePath returns a fresh unused path in directory.
func reservePath(directory, pattern string) (string, error) {
	file, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return path, nil
}

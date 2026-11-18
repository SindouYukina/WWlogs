//go:build !windows

package history

import (
	"errors"
	"os"
)

// moveDurably renames from to to. Without replace an existing file at to is never replaced.
func moveDurably(from, to string, replace bool) error {
	if !replace {
		if _, err := os.Lstat(to); err == nil {
			return errors.New("the target file already exists")
		}
	}
	return os.Rename(from, to)
}

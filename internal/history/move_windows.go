//go:build windows

package history

import "golang.org/x/sys/windows"

// moveDurably renames from to to on the same volume and returns only once the rename is on disk (MOVEFILE_WRITE_THROUGH).
// Without replace an existing file at to is never replaced: the move fails instead.
func moveDurably(from, to string, replace bool) error {
	source, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	target, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	flags := uint32(windows.MOVEFILE_WRITE_THROUGH)
	if replace {
		flags |= windows.MOVEFILE_REPLACE_EXISTING
	}
	return windows.MoveFileEx(source, target, flags)
}

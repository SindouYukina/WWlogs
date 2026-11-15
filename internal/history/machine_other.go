//go:build !windows

package history

import "errors"

// WWlogs runs on Windows. Elsewhere no firmware table is read, so no history key can be derived; tests pass their own
// machine identity (KeyInputs).
func readFirmwareTable() ([]byte, error) {
	return nil, errors.New("the SMBIOS firmware table is read on Windows only")
}

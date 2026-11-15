//go:build windows

package history

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// kernel32 is loaded from System32 only, so a DLL placed beside the executable is never used.
var procGetSystemFirmwareTable = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetSystemFirmwareTable")

// firmwareProviderRSMB is the raw SMBIOS table provider, the characters 'RSMB' as a DWORD.
const firmwareProviderRSMB = 'R'<<24 | 'S'<<16 | 'M'<<8 | 'B'

// readFirmwareTable returns the RawSMBIOSData buffer. It needs no elevation. The first call asks for the size and the
// second fills the buffer; a table that grew in between is read again.
func readFirmwareTable() ([]byte, error) {
	if err := procGetSystemFirmwareTable.Find(); err != nil {
		return nil, fmt.Errorf("GetSystemFirmwareTable: %w", err)
	}
	size, err := firmwareTable(nil)
	if err != nil {
		return nil, fmt.Errorf("GetSystemFirmwareTable size: %w", err)
	}
	for attempt := 0; attempt < 4; attempt++ {
		buffer := make([]byte, size)
		got, err := firmwareTable(buffer)
		if err != nil {
			clear(buffer)
			return nil, fmt.Errorf("GetSystemFirmwareTable: %w", err)
		}
		if got <= size {
			return buffer[:got], nil
		}
		clear(buffer)
		size = got
	}
	return nil, errors.New("GetSystemFirmwareTable: the table size kept changing")
}

// firmwareTable calls GetSystemFirmwareTable. A result larger than the buffer is the size it needs; zero is a failure,
// explained by the thread's last error.
func firmwareTable(buffer []byte) (uint32, error) {
	var pointer uintptr
	if len(buffer) > 0 {
		pointer = uintptr(unsafe.Pointer(&buffer[0]))
	}
	result, _, err := procGetSystemFirmwareTable.Call(uintptr(firmwareProviderRSMB), 0, pointer, uintptr(len(buffer)))
	if result == 0 {
		return 0, err
	}
	return uint32(result), nil
}

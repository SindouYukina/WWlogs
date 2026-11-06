package capture

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// npcapLibraryPresent looks where gopacket loads wpcap.dll from: System32\Npcap, then System32 for WinPcap-compatible
// installs. It reports true when the system directory is unknown, so a failed lookup never reads as "not installed".
func npcapLibraryPresent() bool {
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return true
	}
	for _, candidate := range []string{filepath.Join(system, "Npcap", "wpcap.dll"), filepath.Join(system, "wpcap.dll")} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

// npcapServiceState asks the service manager about the Npcap driver with query rights only. It changes nothing and
// needs no elevation.
func npcapServiceState() NpcapServiceState {
	manager, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return NpcapServiceUnknown
	}
	defer windows.CloseServiceHandle(manager)
	name, err := windows.UTF16PtrFromString("npcap")
	if err != nil {
		return NpcapServiceUnknown
	}
	service, err := windows.OpenService(manager, name, windows.SERVICE_QUERY_STATUS)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return NpcapServiceAbsent
	}
	if err != nil {
		return NpcapServiceUnknown
	}
	defer windows.CloseServiceHandle(service)
	var status windows.SERVICE_STATUS
	if err := windows.QueryServiceStatus(service, &status); err != nil {
		return NpcapServiceUnknown
	}
	if status.CurrentState == windows.SERVICE_STOPPED {
		return NpcapServiceStopped
	}
	return NpcapServiceRunning
}

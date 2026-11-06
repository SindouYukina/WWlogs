package capture

import (
	"fmt"

	"github.com/google/gopacket/pcap"
)

// EnvironmentState says whether live capture can start, and if not, the narrowest reason the system can prove.
type EnvironmentState string

const (
	// EnvironmentReady means at least one interface has an IPv4 address.
	EnvironmentReady EnvironmentState = "ready"
	// EnvironmentNpcapMissing means the library could not be loaded and its files and service are not on the system.
	EnvironmentNpcapMissing EnvironmentState = "npcap_missing"
	// EnvironmentNpcapUnavailable means something of Npcap is present but it could not be used.
	EnvironmentNpcapUnavailable EnvironmentState = "npcap_unavailable"
	// EnvironmentNoInterfaces means Npcap works and reported no interfaces.
	EnvironmentNoInterfaces EnvironmentState = "no_interfaces"
	// EnvironmentNoIPv4 means Npcap reported interfaces, none with an IPv4 address.
	EnvironmentNoIPv4 EnvironmentState = "no_ipv4"
	// EnvironmentCheckFailed means the check did not finish, so nothing is known about Npcap.
	EnvironmentCheckFailed EnvironmentState = "check_failed"
)

// CaptureEnvironment is one check result. It carries no system error text: the state is the whole public meaning.
type CaptureEnvironment struct {
	State          EnvironmentState `json:"state"`
	Adapters       []Adapter        `json:"adapters"`
	InterfaceCount int              `json:"interfaceCount"`
}

// Adapter is an Npcap interface address that can carry game traffic.
type Adapter struct {
	IPv4        string `json:"ipv4"`
	Description string `json:"description"`
}

// NpcapServiceState is what the Windows service manager reports for the Npcap driver.
type NpcapServiceState int

const (
	// NpcapServiceUnknown means the question could not be answered, which proves nothing either way.
	NpcapServiceUnknown NpcapServiceState = iota
	NpcapServiceAbsent
	NpcapServiceStopped
	NpcapServiceRunning
)

// EnvironmentProbe reads the system. The functions are fields so every state can be tested without changing the machine.
type EnvironmentProbe struct {
	ListDevices    func() ([]pcap.Interface, error)
	LibraryPresent func() bool
	Service        func() NpcapServiceState
}

// SystemEnvironmentProbe reads the real system. gopacket retries loading wpcap.dll on every enumeration until it
// succeeds, so an installation finished while WWlogs is open is found without restarting. A library that is already
// loaded stays loaded, so an in-place upgrade or repair can still require a restart.
func SystemEnvironmentProbe() EnvironmentProbe {
	return EnvironmentProbe{ListDevices: pcap.FindAllDevs, LibraryPresent: npcapLibraryPresent, Service: npcapServiceState}
}

// Check enumerates interfaces once and classifies the result. It touches no running capture.
func (p EnvironmentProbe) Check() CaptureEnvironment {
	devices, err := listDevicesSafely(p.ListDevices)
	if err != nil {
		return classifyEnvironmentFailure(p.LibraryPresent(), p.Service())
	}
	return classifyEnvironmentDevices(devices, p.Service)
}

// listDevicesSafely turns gopacket's panic on a wpcap.dll that lacks a required function into an ordinary failure, so an
// incompatible library cannot take the application down during a check.
func listDevicesSafely(list func() ([]pcap.Interface, error)) (devices []pcap.Interface, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("load wpcap.dll: %v", recovered)
		}
	}()
	return list()
}

func classifyEnvironmentFailure(libraryPresent bool, service NpcapServiceState) CaptureEnvironment {
	state := EnvironmentNpcapUnavailable
	// Only a missing library with no registered driver reads as not installed. A leftover driver is a broken install.
	if !libraryPresent && service != NpcapServiceRunning && service != NpcapServiceStopped {
		state = EnvironmentNpcapMissing
	}
	return CaptureEnvironment{State: state, Adapters: []Adapter{}}
}

func classifyEnvironmentDevices(devices []pcap.Interface, service func() NpcapServiceState) CaptureEnvironment {
	adapters := ipv4Adapters(devices)
	result := CaptureEnvironment{Adapters: adapters, InterfaceCount: len(devices)}
	switch {
	case len(adapters) > 0:
		result.State = EnvironmentReady
	case len(devices) > 0:
		result.State = EnvironmentNoIPv4
	default:
		// An empty list from a stopped driver says nothing about the machine's interfaces.
		result.State = EnvironmentNoInterfaces
		if current := service(); current == NpcapServiceStopped || current == NpcapServiceAbsent {
			result.State = EnvironmentNpcapUnavailable
		}
	}
	return result
}

func ipv4Adapters(devices []pcap.Interface) []Adapter {
	adapters := []Adapter{}
	for _, device := range devices {
		for _, address := range device.Addresses {
			if address.IP == nil || address.IP.To4() == nil {
				continue
			}
			adapters = append(adapters, Adapter{IPv4: address.IP.String(), Description: device.Description})
		}
	}
	return adapters
}

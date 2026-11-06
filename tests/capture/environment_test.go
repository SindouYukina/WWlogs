package capture

import (
	"errors"
	"net"
	"testing"

	"github.com/google/gopacket/pcap"
)

func interfaces(addresses ...string) []pcap.Interface {
	device := pcap.Interface{Name: "device", Description: "adapter"}
	for _, address := range addresses {
		device.Addresses = append(device.Addresses, pcap.InterfaceAddress{IP: net.ParseIP(address)})
	}
	return []pcap.Interface{device}
}

func TestEnvironmentStatesRestOnlyOnWhatTheSystemProves(t *testing.T) {
	loadError := errors.New("couldn't load wpcap.dll")
	for _, test := range []struct {
		name     string
		devices  []pcap.Interface
		err      error
		library  bool
		service  NpcapServiceState
		want     EnvironmentState
		adapters int
	}{
		{name: "nothing of Npcap exists", err: loadError, service: NpcapServiceAbsent, want: EnvironmentNpcapMissing},
		{name: "no library and an unanswered service query", err: loadError, service: NpcapServiceUnknown, want: EnvironmentNpcapMissing},
		{name: "library exists but will not load", err: loadError, library: true, service: NpcapServiceRunning, want: EnvironmentNpcapUnavailable},
		{name: "library exists with no service", err: loadError, library: true, service: NpcapServiceAbsent, want: EnvironmentNpcapUnavailable},
		{name: "driver left behind without its library", err: loadError, service: NpcapServiceStopped, want: EnvironmentNpcapUnavailable},
		{name: "enumeration fails on a loaded library", err: errors.New("PacketGetAdapterNames failed"), library: true, service: NpcapServiceRunning, want: EnvironmentNpcapUnavailable},
		{name: "empty list from a stopped driver", library: true, service: NpcapServiceStopped, want: EnvironmentNpcapUnavailable},
		{name: "empty list with no driver service", library: true, service: NpcapServiceAbsent, want: EnvironmentNpcapUnavailable},
		{name: "empty list from a running driver", library: true, service: NpcapServiceRunning, want: EnvironmentNoInterfaces},
		{name: "empty list with an unanswered service query", library: true, service: NpcapServiceUnknown, want: EnvironmentNoInterfaces},
		{name: "interfaces without IPv4", devices: interfaces("fe80::1"), library: true, service: NpcapServiceRunning, want: EnvironmentNoIPv4},
		{name: "interface with IPv4", devices: interfaces("fe80::1", "192.0.2.10"), library: true, service: NpcapServiceRunning, want: EnvironmentReady, adapters: 1},
		{name: "a working enumeration does not depend on the service answer", devices: interfaces("192.0.2.10"), library: false, service: NpcapServiceUnknown, want: EnvironmentReady, adapters: 1},
	} {
		probe := EnvironmentProbe{
			ListDevices:    func() ([]pcap.Interface, error) { return test.devices, test.err },
			LibraryPresent: func() bool { return test.library },
			Service:        func() NpcapServiceState { return test.service },
		}
		result := probe.Check()
		if result.State != test.want || len(result.Adapters) != test.adapters {
			t.Errorf("%s: %+v, want %s with %d adapters", test.name, result, test.want, test.adapters)
		}
		if result.Adapters == nil {
			t.Errorf("%s: adapters must encode as an empty list, not null", test.name)
		}
	}
}

func TestEnvironmentCheckSurvivesAnIncompatibleLibrary(t *testing.T) {
	probe := EnvironmentProbe{
		ListDevices:    func() ([]pcap.Interface, error) { panic("Couldn't load function pcap_create from wpcap.dll") },
		LibraryPresent: func() bool { return true },
		Service:        func() NpcapServiceState { return NpcapServiceRunning },
	}
	if got := probe.Check().State; got != EnvironmentNpcapUnavailable {
		t.Fatalf("state = %s", got)
	}
}

// A recheck must find an installation completed while the process runs, which requires a failed load not to stick.
func TestEnvironmentRecoversOnTheNextCheck(t *testing.T) {
	installed := false
	probe := EnvironmentProbe{
		ListDevices: func() ([]pcap.Interface, error) {
			if !installed {
				return nil, errors.New("couldn't load wpcap.dll")
			}
			return interfaces("192.0.2.10"), nil
		},
		LibraryPresent: func() bool { return installed },
		Service: func() NpcapServiceState {
			if installed {
				return NpcapServiceRunning
			}
			return NpcapServiceAbsent
		},
	}
	if got := probe.Check().State; got != EnvironmentNpcapMissing {
		t.Fatalf("before install: %s", got)
	}
	installed = true
	if got := probe.Check(); got.State != EnvironmentReady || got.Adapters[0].IPv4 != "192.0.2.10" {
		t.Fatalf("after install: %+v", got)
	}
}

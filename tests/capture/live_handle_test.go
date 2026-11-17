package capture

import (
	"strings"
	"testing"
	"time"
)

// The applied-settings record says exactly what the driver accepted, including refused options.

func TestLiveCaptureAppliedDescribesRefusedOptions(t *testing.T) {
	cases := []struct {
		applied LiveCaptureApplied
		want    string
	}{
		{LiveCaptureApplied{ReadTimeout: time.Second}, "read_timeout=1s immediate=off kernel_buffer=default"},
		{LiveCaptureApplied{ReadTimeout: time.Second, Immediate: true}, "read_timeout=1s immediate=on kernel_buffer=default"},
		{LiveCaptureApplied{ReadTimeout: time.Second, ImmediateError: "missing"}, "read_timeout=1s immediate=unsupported kernel_buffer=default"},
		{LiveCaptureApplied{ReadTimeout: 100 * time.Millisecond, BufferBytes: 4 << 20}, "read_timeout=100ms immediate=off kernel_buffer=4194304"},
		{LiveCaptureApplied{ReadTimeout: time.Second, BufferError: "refused"}, "read_timeout=1s immediate=off kernel_buffer=default_after_error"},
	}
	for _, c := range cases {
		if got := c.applied.String(); got != c.want {
			t.Errorf("%+v = %q, want %q", c.applied, got, c.want)
		}
	}
}

func TestOpenLiveCaptureReportsMissingDevice(t *testing.T) {
	_, _, err := OpenLiveCapture(`\Device\NPF_{00000000-0000-0000-0000-000000000000}`, liveCaptureConfig{ReadTimeout: time.Second, Immediate: true})
	if err == nil {
		t.Skip("a device with the all-zero GUID exists on this host")
	}
	if strings.TrimSpace(err.Error()) == "" {
		t.Fatal("opening a missing device must return a descriptive error")
	}
}

func TestDefaultLiveCaptureConfigUsesImmediateModeWithBoundedFallback(t *testing.T) {
	cfg := DefaultLiveCaptureConfig()
	if !cfg.Immediate || cfg.ReadTimeout != time.Second || cfg.FallbackReadTimeout != 100*time.Millisecond || cfg.BufferBytes != 0 {
		t.Fatalf("default capture config = %+v", cfg)
	}
	if (liveCaptureConfig{ReadTimeout: time.Second}).fallbackTimeout() != time.Second {
		t.Fatal("without a fallback the read timeout is kept")
	}
}

// TestOpenLiveCaptureOnNpcapLoopback opens the real Npcap loopback adapter when it exists; on hosts without Npcap it is
// skipped. It checks that the options are applied as requested, not merely configured.
func TestOpenLiveCaptureOnNpcapLoopback(t *testing.T) {
	handle, applied, err := OpenLiveCapture(`\Device\NPF_Loopback`, DefaultLiveCaptureConfig())
	if err != nil {
		t.Skipf("Npcap loopback adapter unavailable: %v", err)
	}
	defer handle.Close()
	if !applied.Immediate || applied.ImmediateError != "" || applied.ReadTimeout != time.Second {
		t.Fatalf("applied = %+v", applied)
	}
	off, appliedOff, err := OpenLiveCapture(`\Device\NPF_Loopback`, liveCaptureConfig{ReadTimeout: 250 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer off.Close()
	if appliedOff.Immediate || appliedOff.ReadTimeout != 250*time.Millisecond || appliedOff.String() != "read_timeout=250ms immediate=off kernel_buffer=default" {
		t.Fatalf("applied without immediate = %+v", appliedOff)
	}
}

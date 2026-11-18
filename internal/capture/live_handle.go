package capture

import (
	"fmt"
	"time"

	"github.com/google/gopacket/pcap"
)

// liveCaptureConfig is how a live Npcap handle is opened.
//
// Semantics on Windows with gopacket v1.1.19 and Npcap 1.90 (gopacket source, and the loopback latency probe in
// research note capture-pipeline-reliability section 7):
//   - ReadTimeout is the pcap read timeout. gopacket does not switch Windows handles to non-blocking mode, so
//     pcap_next_ex waits inside the driver until enough data is buffered or the timeout expires. Without immediate
//     mode, packets arrive in batches and can wait up to the timeout: at game-like traffic (100 packets/s of at most
//     200 bytes) a 1 s timeout gave a delivery delay of P95 830 ms. With no traffic at all, NextPacket returns
//     NextErrorTimeoutExpired after up to about twice the timeout.
//   - Immediate hands each packet over as soon as it arrives (pcap_set_immediate_mode): P99 under 0.1 ms on the probe,
//     at no measurable extra CPU and with no kernel drops up to 20,000 packets/s. gopacket loads the function
//     optionally; when it is missing or refused, the handle is opened without it and FallbackReadTimeout bounds the
//     batching delay instead (100 ms gave P99 109 ms).
//   - BufferBytes sets the kernel buffer (pcap_set_buffer_size); zero keeps the Npcap default. A 4 MiB buffer showed no
//     benefit on the probe, so the default stays.
type liveCaptureConfig struct {
	ReadTimeout         time.Duration
	Immediate           bool
	FallbackReadTimeout time.Duration
	BufferBytes         int
}

// DefaultLiveCaptureConfig is the configuration both executables use. The read timeout only paces idle wakeups (stop
// checks and status updates) once immediate mode is on.
func DefaultLiveCaptureConfig() liveCaptureConfig {
	return liveCaptureConfig{ReadTimeout: time.Second, Immediate: true, FallbackReadTimeout: 100 * time.Millisecond}
}

// LiveCaptureApplied records what the opened handle actually uses, including options the driver refused.
type LiveCaptureApplied struct {
	ReadTimeout    time.Duration
	Immediate      bool
	ImmediateError string
	BufferBytes    int // 0: Npcap default
	BufferError    string
}

func (a LiveCaptureApplied) String() string {
	immediate := "off"
	switch {
	case a.Immediate:
		immediate = "on"
	case a.ImmediateError != "":
		immediate = "unsupported"
	}
	buffer := "default"
	if a.BufferBytes > 0 {
		buffer = fmt.Sprintf("%d", a.BufferBytes)
	} else if a.BufferError != "" {
		buffer = "default_after_error"
	}
	return fmt.Sprintf("read_timeout=%s immediate=%s kernel_buffer=%s", a.ReadTimeout, immediate, buffer)
}

// LiveCaptureSnapLen keeps whole packets: game datagrams are at most about 1 KB, and the snap length only bounds copying.
const LiveCaptureSnapLen = 65535

// fallbackTimeout is the read timeout to use when immediate mode was requested but is not in effect.
func (cfg liveCaptureConfig) fallbackTimeout() time.Duration {
	if cfg.FallbackReadTimeout > 0 {
		return cfg.FallbackReadTimeout
	}
	return cfg.ReadTimeout
}

// OpenLiveCapture opens a non-promiscuous live handle with cfg. An option the driver refuses is recorded and skipped;
// the handle is still opened, because a capture with default buffering is better than no capture.
func OpenLiveCapture(device string, cfg liveCaptureConfig) (*pcap.Handle, LiveCaptureApplied, error) {
	var applied LiveCaptureApplied
	open := func(immediate bool, buffer int) (*pcap.Handle, error) {
		inactive, err := pcap.NewInactiveHandle(device)
		if err != nil {
			return nil, err
		}
		defer inactive.CleanUp()
		if err := inactive.SetSnapLen(LiveCaptureSnapLen); err != nil {
			return nil, err
		}
		if err := inactive.SetPromisc(false); err != nil {
			return nil, err
		}
		applied.Immediate, applied.BufferBytes, applied.ReadTimeout = false, 0, cfg.ReadTimeout
		if immediate {
			if err := inactive.SetImmediateMode(true); err != nil {
				applied.ImmediateError = err.Error()
			} else {
				applied.Immediate = true
			}
		}
		if cfg.Immediate && !applied.Immediate {
			applied.ReadTimeout = cfg.fallbackTimeout()
		}
		if err := inactive.SetTimeout(applied.ReadTimeout); err != nil {
			return nil, err
		}
		if buffer > 0 {
			if err := inactive.SetBufferSize(buffer); err != nil {
				applied.BufferError = err.Error()
			} else {
				applied.BufferBytes = buffer
			}
		}
		return inactive.Activate()
	}
	handle, err := open(cfg.Immediate, cfg.BufferBytes)
	if err != nil && (cfg.Immediate || cfg.BufferBytes > 0) {
		// Activation can fail on an option rather than on the device; retry with defaults and say so.
		reason := err.Error()
		if cfg.Immediate && applied.ImmediateError == "" {
			applied.ImmediateError = "activation failed: " + reason
		}
		if cfg.BufferBytes > 0 && applied.BufferError == "" {
			applied.BufferError = "activation failed: " + reason
		}
		handle, err = open(false, 0)
	}
	if err != nil {
		return nil, applied, err
	}
	return handle, applied, nil
}

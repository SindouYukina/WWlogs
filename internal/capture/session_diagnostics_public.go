//go:build !wwdev

package capture

import (
	"errors"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
)

// sessionDiagnostics observes a session for Development diagnostics. Public builds have none, so every method is a
// no-op and no file is ever created.
type sessionDiagnostics struct{}

func openSessionDiagnostics(requested bool, _ layers.LinkType) (*sessionDiagnostics, error) {
	if requested {
		return nil, errors.New("development diagnostics are not available in this build")
	}
	return &sessionDiagnostics{}, nil
}

func (*sessionDiagnostics) Attach(*Processor) {}

func (*sessionDiagnostics) Packet(gopacket.CaptureInfo, []byte) {}

func (*sessionDiagnostics) ObserveEffectDisplay(*Processor, time.Time) {}

func (*sessionDiagnostics) Finish(string, CaptureCounter) string { return "" }

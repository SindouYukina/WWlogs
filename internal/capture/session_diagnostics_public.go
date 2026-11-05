//go:build !wwdev

package capture

import (
	"errors"

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

func (*sessionDiagnostics) attach(*processor) {}

func (*sessionDiagnostics) packet(gopacket.CaptureInfo, []byte) {}

func (*sessionDiagnostics) finish(string, CaptureCounter) string { return "" }

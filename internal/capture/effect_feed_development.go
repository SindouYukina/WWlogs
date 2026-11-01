//go:build wwdev

package capture

import (
	"encoding/json"
	"fmt"
	"io"
)

// writeEffectState writes the final tracker snapshot as one JSON line.
func (p *processor) writeEffectState(w io.Writer) error {
	snapshot := p.effectSnapshotAtLastFrame()
	line, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode effect state: %w", err)
	}
	if _, err := w.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write effect state: %w", err)
	}
	return nil
}

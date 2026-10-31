//go:build !wwdev

package capture

import "errors"

// diagWriter is the type of every raw diagnostic sink. In a public build it is a pointer that nothing can
// ever set, so the sinks stay nil and every guarded diagnostic write is unreachable.
type diagWriter = *unavailableWriter

type unavailableWriter struct{}

func (*unavailableWriter) Write([]byte) (int, error) {
	return 0, errors.New("diagnostic output is not available in this build")
}

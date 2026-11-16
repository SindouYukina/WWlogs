package capture

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// QueryPerformanceCounter timing for per-call samples of the measurement collectors: Go's clock resolves only about a
// millisecond on this platform, too coarse for microsecond work.
var (
	qpcKernel32  = windows.NewLazySystemDLL("kernel32.dll")
	qpcCounter   = qpcKernel32.NewProc("QueryPerformanceCounter")
	qpcFrequency = qpcKernel32.NewProc("QueryPerformanceFrequency")
	qpcPerMicro  = func() float64 {
		var frequency int64
		qpcFrequency.Call(uintptr(unsafe.Pointer(&frequency)))
		return float64(frequency) / 1e6
	}()
)

func qpcNow() int64 {
	var value int64
	qpcCounter.Call(uintptr(unsafe.Pointer(&value)))
	return value
}

// qpcMicrosSince is the time since started, in microseconds.
func qpcMicrosSince(started int64) float64 {
	return float64(qpcNow()-started) / qpcPerMicro
}

// Command capture is the WWlogs capture command line (WWlogs-Capture.exe).
package main

// main runs the capture command line of this build profile; compose_public.go and compose_development.go are the
// build composition boundary that chooses it.
func main() {
	runCapture()
}

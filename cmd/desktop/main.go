// Command desktop is the WWlogs desktop application host (Wails v3): the Desktop window and the Overlay window.
package main

import "wwlogs/internal/desktop"

// main runs the shared host with the capabilities of this build profile; compose_public.go and compose_development.go
// are the build composition boundary that chooses them.
func main() {
	desktop.Main(composition)
}

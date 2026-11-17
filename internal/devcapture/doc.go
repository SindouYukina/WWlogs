// Package devcapture is the Development capture module of WWlogs: the research and diagnostic capabilities around the
// shared combat core (wwlogs/internal/capture). It observes the core only through the core's observation points
// (capture.Observer, the command census observers, capture.SessionDiagnostics) and writes the console lines, the
// research and diagnostic outputs, the Development PCAP and the live session spool; it is also the Development capture
// command line (Main). Nothing it computes flows back into a core decision. Only the Development build composition links
// it (cmd/capture/compose_development.go, and the desktop host through wwlogs/internal/devdesktop), so a Public
// executable contains none of it.
package devcapture

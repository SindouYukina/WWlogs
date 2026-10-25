//go:build wwdev

package buildinfo

// profile is selected by the wwdev build tag. Development builds keep the raw diagnostic outputs
// (PCAP recording, JSONL event streams, verbose console diagnostics) that internal research needs.
const profile = ProfileDevelopment

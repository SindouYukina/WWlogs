package devdesktop

import (
	"io"
	"testing"

	"wwlogs/internal/capture"
)

func TestDevelopmentDiagnosticDefaultsWithoutAutostart(t *testing.T) {
	options, err := ParseOptions(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if options.MasterDatabase != "" {
		t.Fatalf("default Master source = %q, want embedded", options.MasterDatabase)
	}
	if options.AutoStart || !options.Session.Diagnostics {
		t.Fatalf("defaults: autostart=%v diagnostics=%v, want live diagnostics without autostart", options.AutoStart, options.Session.Diagnostics)
	}
	override, err := ParseOptions([]string{"-master-db", "test-master.db"}, io.Discard)
	if err != nil || override.MasterDatabase != "test-master.db" {
		t.Fatalf("Development Master override = %+v, %v", override, err)
	}
	replay, err := ParseOptions([]string{"-replay", "sample.pcap"}, io.Discard)
	if err != nil || replay.Session.Diagnostics || !replay.AutoStart {
		t.Fatalf("replay defaults: %+v %v", replay, err)
	}
	if _, err := ParseOptions([]string{"-replay", "sample.pcap", "-capture-diagnostics=true"}, io.Discard); err == nil {
		t.Fatal("explicit replay recording accepted")
	}
}

func TestEnvironmentSimulationFlags(t *testing.T) {
	options, err := ParseOptions([]string{"-simulate-capture-environment", "npcap_missing,no_ipv4", "-simulate-npcap-offline", "-record-browser-url", "out.txt"}, io.Discard)
	if err != nil || options.EnvironmentProbe == nil || options.NpcapTransport == nil || options.OpenBrowser == nil {
		t.Fatalf("simulation options: %+v %v", options, err)
	}
	if first := options.EnvironmentProbe(); first.State != capture.EnvironmentNpcapMissing {
		t.Fatalf("first = %+v", first)
	}
	for range 2 {
		if next := options.EnvironmentProbe(); next.State != capture.EnvironmentNoIPv4 {
			t.Fatalf("the last state must repeat: %+v", next)
		}
	}
	if _, err := ParseOptions([]string{"-simulate-capture-environment", "guess"}, io.Discard); err == nil {
		t.Fatal("an unknown state was accepted")
	}
	defaults, err := ParseOptions(nil, io.Discard)
	if err != nil || defaults.EnvironmentProbe != nil || defaults.NpcapTransport != nil || defaults.OpenBrowser != nil {
		t.Fatalf("defaults must use the real system: %+v %v", defaults, err)
	}
}

func TestShowcaseFlags(t *testing.T) {
	options, err := ParseOptions([]string{"-showcase", "-showcase-overlays", "-asset-root", "example", "-font-root", "local-fonts"}, io.Discard)
	if err != nil || !options.Showcase || !options.ShowcaseOverlays || options.GameAssetRoot != "example" || options.PreviewFontRoot != "local-fonts" {
		t.Fatalf("Development Showcase options: %+v %v", options, err)
	}
	if _, err := ParseOptions([]string{"-showcase-overlays"}, io.Discard); err == nil {
		t.Fatal("Showcase overlay windows were accepted without -showcase")
	}
	defaults, err := ParseOptions(nil, io.Discard)
	if err != nil || defaults.Showcase || defaults.GameAssetRoot != "" || defaults.PreviewFontRoot != "" {
		t.Fatalf("defaults must not enable the Showcase or find assets on their own: %+v %v", defaults, err)
	}
	if _, err := ParseOptions([]string{"-font-root", "local-fonts"}, io.Discard); err == nil {
		t.Fatal("font root was accepted without -showcase")
	}
}

// The remote debugging port joins the requested browser arguments, after them; without a request there is none.
func TestWebviewArgumentsCarryTheDebuggingPort(t *testing.T) {
	options, err := ParseOptions([]string{"-webview-args", "--a --b", "-webview-debug-port", "9333"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--a", "--b", "--remote-debugging-port=9333"}
	if len(options.WebviewArguments) != len(want) {
		t.Fatalf("arguments = %q, want %q", options.WebviewArguments, want)
	}
	for i := range want {
		if options.WebviewArguments[i] != want[i] {
			t.Fatalf("arguments = %q, want %q", options.WebviewArguments, want)
		}
	}
	defaults, err := ParseOptions(nil, io.Discard)
	if err != nil || len(defaults.WebviewArguments) != 0 {
		t.Fatalf("defaults must add no browser argument: %q %v", defaults.WebviewArguments, err)
	}
}

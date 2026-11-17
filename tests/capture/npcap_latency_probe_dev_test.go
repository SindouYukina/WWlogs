//go:build wwdev && windows

package capture

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"
)

// TestNpcapLatencyProbe measures how long Npcap holds packets before the capture loop sees them, for several handle
// configurations. It is skipped unless WWLOGS_NPCAP_LATENCY_OUT is set. It captures on the Npcap loopback adapter and
// sends timestamped UDP datagrams to 127.0.0.1 only, so nothing leaves the host.
//
// Per packet it records three times on the host clock: the send time written into the payload, the Npcap capture
// timestamp, and the time NextPacket returned it to the loop (the same gopacket path the executables use). It reports
//   driver delay   = Npcap timestamp - send time (how the driver stamps packets),
//   delivery delay = loop receive time - Npcap timestamp (buffering in the driver and the read path),
//   total delay    = loop receive time - send time.
// Combat decoding after receipt costs about 13 us per packet (TestReplayPerformanceCollector) and is not included.
//
// This is a measurement of Npcap on this host with synthetic loopback traffic. It is not a measurement of game
// traffic on a network adapter; that remains a live acceptance step.
//
// Environment: WWLOGS_NPCAP_LATENCY_OUT (output file), WWLOGS_NPCAP_LATENCY_SECONDS (per configuration, default 20),
// WWLOGS_NPCAP_LATENCY_RATE (packets per second, default 300), WWLOGS_NPCAP_LATENCY_BURST_RATE (default 20000),
// WWLOGS_NPCAP_LATENCY_MAX_SIZE (largest UDP payload, default 976; sizes cycle from 64), WWLOGS_NPCAP_LATENCY_CONFIGS
// (comma-separated configuration names; default all), WWLOGS_NPCAP_LATENCY_SKIP_BURST (set to skip the burst runs).

const latencyProbeMagic = 0x5757_4c4f_4753_4c41 // "WWLOGSLA"

type latencyProbeConfig struct {
	Name     string            `json:"name"`
	OpenLive bool              `json:"usesOpenLive"`
	Config   liveCaptureConfig `json:"config"`
}

type latencyDistribution struct {
	Samples int     `json:"samples"`
	P50ms   float64 `json:"p50ms"`
	P95ms   float64 `json:"p95ms"`
	P99ms   float64 `json:"p99ms"`
	MaxMs   float64 `json:"maxMs"`
}

type latencyProbeRun struct {
	Name           string  `json:"name"`
	Applied        string  `json:"applied"`
	RatePerSecond  int     `json:"ratePerSecond"`
	Sent           int     `json:"sent"`
	Received       int     `json:"received"`
	KernelReceived uint64  `json:"kernelReceived"`
	KernelDropped  uint64  `json:"kernelDropped"`
	CPUSeconds     float64 `json:"cpuSecondsProcess"`
	// ProcessCPUSeconds is user plus kernel time from GetProcessTimes, which also covers time inside the driver
	// calls that the Go runtime counter cannot see. It includes the sender goroutine, which is the same in every run.
	ProcessCPUSeconds float64             `json:"processCpuSeconds"`
	WallSeconds       float64             `json:"wallSeconds"`
	ReadsReturned     int                 `json:"nextPacketCalls"`
	Timeouts          int                 `json:"timeoutReturns"`
	DriverDelay       latencyDistribution `json:"driverDelay"`
	DeliveryDelay     latencyDistribution `json:"deliveryDelay"`
	TotalDelay        latencyDistribution `json:"totalDelay"`
}

func distribution(values []time.Duration) latencyDistribution {
	if len(values) == 0 {
		return latencyDistribution{}
	}
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	pick := func(p float64) float64 {
		index := int(p*float64(len(sorted)-1) + 0.5)
		return float64(sorted[index].Microseconds()) / 1000
	}
	return latencyDistribution{Samples: len(sorted), P50ms: pick(0.50), P95ms: pick(0.95), P99ms: pick(0.99), MaxMs: pick(1)}
}

func TestNpcapLatencyProbe(t *testing.T) {
	out := os.Getenv("WWLOGS_NPCAP_LATENCY_OUT")
	if out == "" {
		t.Skip("WWLOGS_NPCAP_LATENCY_OUT is not set")
	}
	seconds := envInt(t, "WWLOGS_NPCAP_LATENCY_SECONDS", 20)
	rate := envInt(t, "WWLOGS_NPCAP_LATENCY_RATE", 300)
	burst := envInt(t, "WWLOGS_NPCAP_LATENCY_BURST_RATE", 20000)
	configs := []latencyProbeConfig{
		{Name: "openlive_timeout_1s (v0.5.0 1C)", OpenLive: true, Config: liveCaptureConfig{ReadTimeout: time.Second}},
		{Name: "timeout_1s", Config: liveCaptureConfig{ReadTimeout: time.Second}},
		{Name: "timeout_250ms", Config: liveCaptureConfig{ReadTimeout: 250 * time.Millisecond}},
		{Name: "timeout_100ms", Config: liveCaptureConfig{ReadTimeout: 100 * time.Millisecond}},
		{Name: "timeout_50ms", Config: liveCaptureConfig{ReadTimeout: 50 * time.Millisecond}},
		{Name: "immediate_timeout_1s", Config: liveCaptureConfig{ReadTimeout: time.Second, Immediate: true}},
		{Name: "immediate_timeout_1s_buffer_4MiB", Config: liveCaptureConfig{ReadTimeout: time.Second, Immediate: true, BufferBytes: 4 << 20}},
	}
	maxSize := envInt(t, "WWLOGS_NPCAP_LATENCY_MAX_SIZE", 976)
	if maxSize < 64 || maxSize > 976 {
		t.Fatalf("WWLOGS_NPCAP_LATENCY_MAX_SIZE must be 64..976")
	}
	if names := os.Getenv("WWLOGS_NPCAP_LATENCY_CONFIGS"); names != "" {
		wanted := map[string]bool{}
		for _, name := range strings.Split(names, ",") {
			wanted[strings.TrimSpace(name)] = true
		}
		var selected []latencyProbeConfig
		for _, config := range configs {
			if wanted[strings.Fields(config.Name)[0]] {
				selected = append(selected, config)
			}
		}
		configs = selected
	}
	result := map[string]any{"npcap": pcap.Version(), "goVersion": runtime.Version(), "numCPU": runtime.NumCPU(), "secondsPerRun": seconds,
		"ratePerSecond": rate, "maxPayloadBytes": maxSize}
	var steady, bursts []latencyProbeRun
	for _, config := range configs {
		steady = append(steady, runLatencyProbe(t, config, rate, maxSize, time.Duration(seconds)*time.Second))
	}
	if os.Getenv("WWLOGS_NPCAP_LATENCY_SKIP_BURST") == "" {
		for _, config := range configs {
			bursts = append(bursts, runLatencyProbe(t, config, burst, maxSize, 5*time.Second))
		}
	}
	result["steady"], result["burst"] = steady, bursts
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func envInt(t *testing.T, name string, fallback int) int {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		t.Fatalf("invalid %s %q", name, value)
	}
	return parsed
}

func runLatencyProbe(t *testing.T, config latencyProbeConfig, rate, maxSize int, duration time.Duration) latencyProbeRun {
	t.Helper()
	const device = `\Device\NPF_Loopback`
	const port = 13199
	var handle *pcap.Handle
	var applied LiveCaptureApplied
	var err error
	if config.OpenLive {
		handle, err = pcap.OpenLive(device, LiveCaptureSnapLen, false, config.Config.ReadTimeout)
		applied = LiveCaptureApplied{ReadTimeout: config.Config.ReadTimeout}
	} else {
		handle, applied, err = OpenLiveCapture(device, config.Config)
	}
	if err != nil {
		t.Fatalf("%s: open loopback capture: %v", config.Name, err)
	}
	defer handle.Close()
	if err := handle.SetBPFFilter(fmt.Sprintf("udp and dst port %d", port)); err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	run := latencyProbeRun{Name: config.Name, Applied: applied.String(), RatePerSecond: rate}
	sent := make(chan int, 1)
	go func() {
		interval := time.Second / time.Duration(rate)
		payload := make([]byte, 976)
		count := 0
		start := time.Now()
		for next := start; time.Since(start) < duration; next = next.Add(interval) {
			if wait := time.Until(next); wait > 0 {
				time.Sleep(wait)
			}
			size := 64
			if maxSize > 64 {
				size += (count * 131) % (maxSize - 64)
			}
			binary.LittleEndian.PutUint64(payload[0:8], latencyProbeMagic)
			binary.LittleEndian.PutUint64(payload[8:16], uint64(count))
			binary.LittleEndian.PutUint64(payload[16:24], uint64(time.Now().UnixNano()))
			if _, err := conn.Write(payload[:size]); err == nil {
				count++
			}
		}
		sent <- count
	}()

	source := gopacket.NewPacketSource(handle, handle.LinkType())
	var driver, delivery, total []time.Duration
	cpu := cpuSeconds()
	processCPU := processCPUSeconds()
	started := time.Now()
	sentCount := -1
	var drainUntil time.Time
	for {
		if sentCount < 0 {
			select {
			case sentCount = <-sent:
				drainUntil = time.Now().Add(3 * time.Second)
			default:
			}
		} else if time.Now().After(drainUntil) || run.Received >= sentCount {
			break
		}
		packet, err := source.NextPacket()
		run.ReadsReturned++
		if err == pcap.NextErrorTimeoutExpired {
			run.Timeouts++
			continue
		}
		if err != nil {
			continue
		}
		received := time.Now()
		udpLayer := packet.Layer(layers.LayerTypeUDP)
		if udpLayer == nil {
			continue
		}
		data := udpLayer.(*layers.UDP).Payload
		if len(data) < 24 || binary.LittleEndian.Uint64(data[0:8]) != latencyProbeMagic {
			continue
		}
		sentAt := time.Unix(0, int64(binary.LittleEndian.Uint64(data[16:24])))
		stamped := packet.Metadata().CaptureInfo.Timestamp
		run.Received++
		driver = append(driver, stamped.Sub(sentAt))
		delivery = append(delivery, received.Sub(stamped))
		total = append(total, received.Sub(sentAt))
	}
	run.WallSeconds = time.Since(started).Seconds()
	run.CPUSeconds = cpuSeconds() - cpu
	run.ProcessCPUSeconds = processCPUSeconds() - processCPU
	run.Sent = sentCount
	if stats, err := handle.Stats(); err == nil {
		run.KernelReceived, run.KernelDropped = uint64(stats.PacketsReceived), uint64(stats.PacketsDropped)
	}
	run.DriverDelay, run.DeliveryDelay, run.TotalDelay = distribution(driver), distribution(delivery), distribution(total)
	return run
}

// processCPUSeconds is the user and kernel CPU time of this process.
func processCPUSeconds() float64 {
	handle, err := syscall.GetCurrentProcess()
	if err != nil {
		return 0
	}
	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return 0
	}
	toSeconds := func(f syscall.Filetime) float64 {
		return float64(uint64(f.HighDateTime)<<32|uint64(f.LowDateTime)) / 1e7
	}
	return toSeconds(kernel) + toSeconds(user)
}

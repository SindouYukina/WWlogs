package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/gopacket/pcap"
)

type segment struct {
	data      []byte
	fragments byte
	timestamp time.Time
}

type stream struct {
	next              uint32
	initialized       bool
	pending           map[uint32]segment
	assembling        [][]byte
	assemblingAt      time.Time
	want              byte
	applicationBuffer []byte
	applicationSpans  []applicationSpan
}

type captureStats struct {
	udpPackets  int
	kcpPush     int
	kcpOrdered  int
	kcpMessages int
	frames      int
	badCRC      int
	malformed   int
	kernelDrops int
}

func findDeviceByIP(ip string) (string, error) {
	devices, err := pcap.FindAllDevs()
	if err != nil {
		return "", err
	}
	for _, candidate := range devices {
		for _, address := range candidate.Addresses {
			if address.IP != nil && address.IP.String() == ip {
				return candidate.Name, nil
			}
		}
	}
	var names []string
	for _, candidate := range devices {
		for _, address := range candidate.Addresses {
			if address.IP != nil {
				names = append(names, candidate.Name+"="+address.IP.String())
			}
		}
	}
	return "", fmt.Errorf("no device has IPv4 %s; available: %s", ip, strings.Join(names, ", "))
}

func consumeDatagram(payload []byte, direction string, capturedAt time.Time, streams map[string]*stream, processor *processor, stats *captureStats) {
	for len(payload) >= 24 {
		dataLen := int(binary.LittleEndian.Uint32(payload[20:24]))
		if dataLen < 0 || 24+dataLen > len(payload) {
			return
		}
		conv := binary.LittleEndian.Uint32(payload[:4])
		cmd, fragments := payload[4], payload[5]
		seq := binary.LittleEndian.Uint32(payload[12:16])
		data := append([]byte(nil), payload[24:24+dataLen]...)
		payload = payload[24+dataLen:]
		if cmd != 81 { // KCP PUSH
			continue
		}
		stats.kcpPush++
		key := fmt.Sprintf("%08x/%s", conv, direction)
		state, ok := streams[key]
		if !ok {
			state = &stream{pending: make(map[uint32]segment)}
			streams[key] = state
		}
		if !state.initialized {
			state.initialized = true
			state.next = seq
		}
		if seq < state.next {
			continue
		}
		state.pending[seq] = segment{data: data, fragments: fragments, timestamp: capturedAt}
		for {
			part, found := state.pending[state.next]
			if !found {
				break
			}
			delete(state.pending, state.next)
			state.next++
			stats.kcpOrdered++
			if part.fragments > 0 {
				if len(state.assembling) == 0 {
					state.assembling = append(state.assembling, part.data)
					state.assemblingAt = part.timestamp
				} else if part.fragments == state.want {
					state.assembling = append(state.assembling, part.data)
				} else {
					state.assembling = nil
					state.assemblingAt = time.Time{}
				}
				if len(state.assembling) > 0 {
					state.want = part.fragments - 1
				}
				continue
			}
			app := part.data
			appTimestamp := part.timestamp
			if len(state.assembling) > 0 {
				state.assembling = append(state.assembling, part.data)
				app = make([]byte, 0)
				for _, fragment := range state.assembling {
					app = append(app, fragment...)
				}
				appTimestamp = state.assemblingAt
				state.assemblingAt = time.Time{}
				state.assembling = nil
			}
			stats.kcpMessages++
			processApplicationData(app, appTimestamp, direction, streams, state, processor, stats)
		}
	}
}

const maxApplicationFrameLength = int(^uint16(0)) + 3

func processApplicationData(data []byte, capturedAt time.Time, direction string, streams map[string]*stream, state *stream, processor *processor, stats *captureStats) {
	if len(data) == 0 {
		return
	}
	state.applicationBuffer = append(state.applicationBuffer, data...)
	if spanCount := len(state.applicationSpans); spanCount > 0 && state.applicationSpans[spanCount-1].timestamp.Equal(capturedAt) {
		state.applicationSpans[spanCount-1].length += len(data)
	} else {
		state.applicationSpans = append(state.applicationSpans, applicationSpan{length: len(data), timestamp: capturedAt})
	}

	previousSession := processor.sessionGeneration
	frameCount, crcErrors, malformed, consumed := parseFrames(state.applicationBuffer, direction, state.applicationSpans, processor)
	stats.frames += frameCount
	stats.badCRC += crcErrors
	stats.malformed += malformed
	if consumed > 0 {
		state.applicationBuffer = append([]byte(nil), state.applicationBuffer[consumed:]...)
		state.applicationSpans = consumeApplicationSpans(state.applicationSpans, consumed)
	}
	if len(state.applicationBuffer) > maxApplicationFrameLength-1 {
		state.applicationBuffer = nil
		state.applicationSpans = nil
		stats.malformed++
	}
	if processor.sessionGeneration != previousSession {
		clearOtherApplicationBuffers(streams, state)
	}
}

func clearOtherApplicationBuffers(streams map[string]*stream, current *stream) {
	for _, state := range streams {
		if state != current {
			state.applicationBuffer = nil
			state.applicationSpans = nil
		}
	}
}

func consumeApplicationSpans(spans []applicationSpan, consumed int) []applicationSpan {
	for consumed > 0 && len(spans) > 0 {
		if consumed >= spans[0].length {
			consumed -= spans[0].length
			spans = spans[1:]
			continue
		}
		spans[0].length -= consumed
		consumed = 0
	}
	return spans
}

func fatal(action string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", action, err)
	os.Exit(1)
}

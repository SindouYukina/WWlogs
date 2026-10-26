package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"

	"github.com/google/gopacket/pcap"
)

type segment struct {
	data      []byte
	fragments byte
}

type stream struct {
	next        uint32
	initialized bool
	pending     map[uint32]segment
	assembling  [][]byte
	want        byte
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

func consumeDatagram(payload []byte, direction string, streams map[string]*stream, processor *processor, stats *captureStats) {
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
		state.pending[seq] = segment{data: data, fragments: fragments}
		for {
			part, found := state.pending[state.next]
			if !found {
				break
			}
			delete(state.pending, state.next)
			state.next++
			stats.kcpOrdered++
			if part.fragments > 0 {
				if len(state.assembling) == 0 || part.fragments == state.want {
					state.assembling = append(state.assembling, part.data)
					state.want = part.fragments - 1
				} else {
					state.assembling = nil
				}
				if len(state.assembling) > 0 {
					state.want = part.fragments - 1
				}
				continue
			}
			app := part.data
			if len(state.assembling) > 0 {
				state.assembling = append(state.assembling, part.data)
				app = make([]byte, 0)
				for _, fragment := range state.assembling {
					app = append(app, fragment...)
				}
				state.assembling = nil
			}
			stats.kcpMessages++
			frameCount, crcErrors, malformed := parseFrames(app, direction, processor)
			stats.frames += frameCount
			stats.badCRC += crcErrors
			stats.malformed += malformed
		}
	}
}

func defaultPrivateKeyPath() string {
	return "privateKey.pem"
}

func fatal(action string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", action, err)
	os.Exit(1)
}

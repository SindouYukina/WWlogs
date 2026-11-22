package instance

import (
	"encoding/binary"
	"slices"
	"strings"
	"testing"
)

func TestRequestCarriesTheLaunchDesktop(t *testing.T) {
	const desktop = `WinSta0\Default`
	request := encodeRequest(desktop)
	length, err := decodeRequestHeader(request[:requestHeaderSize])
	if err != nil {
		t.Fatal(err)
	}
	if length != len(desktop) || string(request[requestHeaderSize:]) != desktop {
		t.Fatalf("request %q decodes to a %d-byte desktop name", request, length)
	}
}

// The pipe offers one request; any other byte in the fixed part makes the running instance ignore the connection.
func TestRequestHeaderRefusesAnythingElse(t *testing.T) {
	valid := encodeRequest("x")[:requestHeaderSize]
	cases := map[string]func([]byte) []byte{
		"magic":   func(b []byte) []byte { b[0] = 'X'; return b },
		"version": func(b []byte) []byte { b[6] = requestVersion + 1; return b },
		"command": func(b []byte) []byte { b[7] = 'R'; return b },
		"length":  func(b []byte) []byte { binary.LittleEndian.PutUint16(b[8:], maxDesktopName+1); return b },
		"short":   func(b []byte) []byte { return b[:requestHeaderSize-1] },
		"long":    func(b []byte) []byte { return append(b, 0) },
	}
	for name, mutate := range cases {
		if _, err := decodeRequestHeader(mutate(slices.Clone(valid))); err == nil {
			t.Errorf("%s: a changed header was accepted", name)
		}
	}
}

func TestRequestBoundsTheDesktopName(t *testing.T) {
	request := encodeRequest(strings.Repeat("d", 2*maxDesktopName))
	length, err := decodeRequestHeader(request[:requestHeaderSize])
	if err != nil || length != maxDesktopName || len(request) != requestHeaderSize+maxDesktopName {
		t.Fatalf("long desktop name: length %d, request %d bytes, %v", length, len(request), err)
	}
}

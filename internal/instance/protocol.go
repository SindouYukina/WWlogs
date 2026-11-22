package instance

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// The request of a later launch: "WWLOGS", the request version, the command, then the window station and desktop of
// the launch as a little-endian uint16 length and that many UTF-8 bytes. There is one command, bringing the window back,
// and the pipe offers nothing else: the running instance ignores any other request without answering. A later WWlogs
// must keep sending a request an earlier one can read, since either may be the one already running.
const (
	requestVersion    = 1
	commandActivate   = 'A'
	requestHeaderSize = 10
	// maxDesktopName bounds the name, so a request never needs more than a small fixed buffer.
	maxDesktopName = 512
)

var requestMagic = []byte("WWLOGS")

// encodeRequest builds the request to bring the window back from a launch on the named window station and desktop.
func encodeRequest(desktop string) []byte {
	name := []byte(desktop)
	if len(name) > maxDesktopName {
		name = name[:maxDesktopName]
	}
	request := make([]byte, 0, requestHeaderSize+len(name))
	request = append(request, requestMagic...)
	request = append(request, requestVersion, commandActivate)
	request = binary.LittleEndian.AppendUint16(request, uint16(len(name)))
	return append(request, name...)
}

// decodeRequestHeader checks the fixed part of a request and returns the length of the desktop name that follows.
func decodeRequestHeader(header []byte) (int, error) {
	if len(header) != requestHeaderSize || !bytes.Equal(header[:len(requestMagic)], requestMagic) {
		return 0, errors.New("not a WWlogs request")
	}
	if header[6] != requestVersion {
		return 0, fmt.Errorf("unsupported request version %d", header[6])
	}
	if header[7] != commandActivate {
		return 0, fmt.Errorf("unsupported request command %d", header[7])
	}
	length := int(binary.LittleEndian.Uint16(header[8:]))
	if length > maxDesktopName {
		return 0, errors.New("the desktop name is too long")
	}
	return length, nil
}

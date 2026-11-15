package history

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// The machine a history belongs to.
//
// WWlogs treats the motherboard as the computer: the history key (keyderive.go) is derived from the identity the
// motherboard's firmware reports in its SMBIOS table, read with GetSystemFirmwareTable('RSMB') (machine_windows.go). Nothing
// else of the machine takes part: no processor, disk, graphics, memory or network adapter (all of them can be swapped), no
// Windows installation, account, language or time zone. A Windows reinstall on the same motherboard therefore gives the
// same key; a new motherboard, or a firmware update that rewrites the identity, gives another one, and a history made
// before can no longer be read (research/runs/2026-11-15/v0.8.0-hwid-preflight-safety, section 1).
//
// Sources, version 1 of their canonical forms:
//
//   - smbios-type1-uuid (the source): the 16 bytes at offset 08h of the SMBIOS Type 1 structure, exactly as the firmware
//     stores them. No text form is involved, so letter case, braces, hyphens and the byte order of the first three fields
//     in text never matter.
//   - smbios-type2-serial (fallback): the motherboard's Type 2 Serial Number as ASCII: leading and trailing white space and
//     NUL removed, inner runs of white space collapsed to one space, a-z upper-cased. A byte outside printable ASCII makes
//     the serial unusable rather than transcoded.
//
// A new history takes the UUID when it is a usable identifier and the serial only when the UUID is not; the source it took
// is recorded with it (keyformat.go) and is the only one it is ever opened with. An identifier that is absent, ambiguous or
// a known placeholder is never replaced by a default: no history is created from it.

// Machine holds the motherboard identifiers as the firmware reports them. Both fields are identifiers and must never be
// printed or logged.
type Machine struct {
	// SystemUUID is the 16 bytes at offset 08h of the SMBIOS Type 1 structure; nil when the table holds no Type 1 UUID, or
	// several that disagree.
	SystemUUID []byte
	// BaseboardSerial is the Serial Number string of the motherboard's SMBIOS Type 2 structure, without its terminator; nil
	// when there is none or the motherboard cannot be told apart from other boards in the table.
	BaseboardSerial []byte
}

func (m Machine) wipe() {
	clear(m.SystemUUID)
	clear(m.BaseboardSerial)
}

// Identifier sources, as recorded with a history.
const (
	sourceSystemUUID      = "smbios-type1-uuid"
	sourceBaseboardSerial = "smbios-type2-serial"
)

var (
	errSMBIOSMalformed = errors.New("the SMBIOS table is malformed")
	// errNoMachineIdentity reports a machine whose firmware names no usable motherboard identifier.
	errNoMachineIdentity = errors.New("the motherboard reports no usable identifier")
)

// SMBIOS structure types and offsets (DMTF DSP0134).
const (
	smbiosRawHeader       = 8
	smbiosEndOfTable      = 127
	smbiosSystem          = 1
	smbiosBaseboard       = 2
	type1UUIDOffset       = 0x08
	type2SerialOffset     = 0x07
	type2BoardTypeOffset  = 0x0D
	boardTypeMotherboard  = 0x0A
	systemUUIDSize        = 16
	minimumSerialLength   = 4
	minimumUUIDByteValues = 5
)

type smbiosStructure struct {
	kind      byte
	formatted []byte
	strings   [][]byte
}

// stringAt resolves the string whose number the formatted area holds at offset; nil for none, a number past the string set
// or a structure too short for the field.
func (s smbiosStructure) stringAt(offset int) []byte {
	if offset >= len(s.formatted) {
		return nil
	}
	number := int(s.formatted[offset])
	if number == 0 || number > len(s.strings) {
		return nil
	}
	return s.strings[number-1]
}

// parseSMBIOS reads the structures of a RawSMBIOSData buffer (GetSystemFirmwareTable 'RSMB'). It is strict: a structure
// that runs past the table or a string set without its terminator makes the whole table malformed, never guessed at. The
// structures point into raw.
func parseSMBIOS(raw []byte) ([]smbiosStructure, error) {
	if len(raw) < smbiosRawHeader {
		return nil, fmt.Errorf("%w: %d bytes", errSMBIOSMalformed, len(raw))
	}
	length := binary.LittleEndian.Uint32(raw[4:8])
	data := raw[smbiosRawHeader:]
	if uint64(length) > uint64(len(data)) {
		return nil, fmt.Errorf("%w: the table declares %d bytes of %d", errSMBIOSMalformed, length, len(data))
	}
	data = data[:length]
	var structures []smbiosStructure
	for offset := 0; len(data)-offset >= 4; {
		kind, size := data[offset], int(data[offset+1])
		if size < 4 || offset+size > len(data) {
			return nil, fmt.Errorf("%w: type %d at %d declares %d bytes", errSMBIOSMalformed, kind, offset, size)
		}
		structure := smbiosStructure{kind: kind, formatted: data[offset : offset+size]}
		next := offset + size
		if next+1 < len(data) && data[next] == 0 && data[next+1] == 0 {
			next += 2
		} else {
			for {
				end := bytes.IndexByte(data[next:], 0)
				if end < 0 || next+end+1 >= len(data) {
					return nil, fmt.Errorf("%w: unterminated strings of type %d at %d", errSMBIOSMalformed, kind, offset)
				}
				structure.strings = append(structure.strings, data[next:next+end])
				next += end + 1
				if data[next] == 0 {
					next++
					break
				}
			}
		}
		structures = append(structures, structure)
		if kind == smbiosEndOfTable {
			break
		}
		offset = next
	}
	return structures, nil
}

// machineFromSMBIOS takes the motherboard identifiers out of a RawSMBIOSData buffer. The identifiers it returns are copies;
// the caller wipes them and raw.
func machineFromSMBIOS(raw []byte) (Machine, error) {
	structures, err := parseSMBIOS(raw)
	if err != nil {
		return Machine{}, err
	}
	var machine Machine
	ambiguousUUID := false
	var boards []smbiosStructure
	for _, structure := range structures {
		switch structure.kind {
		case smbiosSystem:
			if len(structure.formatted) < type1UUIDOffset+systemUUIDSize {
				continue
			}
			uuid := structure.formatted[type1UUIDOffset : type1UUIDOffset+systemUUIDSize]
			if machine.SystemUUID != nil && !bytes.Equal(machine.SystemUUID, uuid) {
				ambiguousUUID = true
			}
			if machine.SystemUUID == nil {
				machine.SystemUUID = append([]byte(nil), uuid...)
			}
		case smbiosBaseboard:
			boards = append(boards, structure)
		}
	}
	if ambiguousUUID {
		clear(machine.SystemUUID)
		machine.SystemUUID = nil
	}
	if len(boards) > 1 {
		// Several boards: only the single one marked as the motherboard names the computer.
		var motherboards []smbiosStructure
		for _, board := range boards {
			if len(board.formatted) > type2BoardTypeOffset && board.formatted[type2BoardTypeOffset] == boardTypeMotherboard {
				motherboards = append(motherboards, board)
			}
		}
		boards = motherboards
	}
	if len(boards) == 1 {
		if serial := boards[0].stringAt(type2SerialOffset); serial != nil {
			machine.BaseboardSerial = append([]byte(nil), serial...)
		}
	}
	return machine, nil
}

// identifierFor returns the canonical identifier of source on machine. A new history takes it only when it is usable
// (creating): not a known placeholder or a value too uniform to tell machines apart. An existing history takes the
// identifier its key was made from, whatever it is, because its key check proves it (keyformat.go). The caller wipes the
// result.
func identifierFor(source string, machine Machine, creating bool) ([]byte, error) {
	switch source {
	case sourceSystemUUID:
		if len(machine.SystemUUID) != systemUUIDSize {
			return nil, fmt.Errorf("%w: no SMBIOS Type 1 UUID", errNoMachineIdentity)
		}
		if creating {
			if problem := uuidProblem(machine.SystemUUID); problem != "" {
				return nil, fmt.Errorf("%w: the SMBIOS Type 1 UUID is %s", errNoMachineIdentity, problem)
			}
		}
		return append([]byte(nil), machine.SystemUUID...), nil
	case sourceBaseboardSerial:
		if machine.BaseboardSerial == nil {
			return nil, fmt.Errorf("%w: no SMBIOS Type 2 serial number", errNoMachineIdentity)
		}
		serial, ok := canonicalSerial(machine.BaseboardSerial)
		if !ok || len(serial) == 0 {
			clear(serial)
			return nil, fmt.Errorf("%w: the SMBIOS Type 2 serial number is empty or not printable ASCII", errNoMachineIdentity)
		}
		if creating {
			if problem := serialProblem(string(serial)); problem != "" {
				clear(serial)
				return nil, fmt.Errorf("%w: the SMBIOS Type 2 serial number is %s", errNoMachineIdentity, problem)
			}
		}
		return serial, nil
	}
	return nil, fmt.Errorf("unknown identifier source %q", source)
}

// newHistorySource is the source and identifier a new history takes: the UUID, or the serial when the UUID is unusable.
func newHistorySource(machine Machine) (string, []byte, error) {
	identifier, uuidErr := identifierFor(sourceSystemUUID, machine, true)
	if uuidErr == nil {
		return sourceSystemUUID, identifier, nil
	}
	identifier, serialErr := identifierFor(sourceBaseboardSerial, machine, true)
	if serialErr == nil {
		return sourceBaseboardSerial, identifier, nil
	}
	return "", nil, errors.Join(uuidErr, serialErr)
}

// uuidByteOrderSwapped is the UUID with its first three fields in the other byte order. Some firmware updates report the
// same UUID that way (research/runs/2026-11-15/v0.8.0-hwid-key-derivation, section 2.3); an existing history accepts it
// only when its key check matches, so a different UUID is never accepted.
func uuidByteOrderSwapped(uuid []byte) []byte {
	swapped := append([]byte(nil), uuid...)
	swapped[0], swapped[1], swapped[2], swapped[3] = uuid[3], uuid[2], uuid[1], uuid[0]
	swapped[4], swapped[5] = uuid[5], uuid[4]
	swapped[6], swapped[7] = uuid[7], uuid[6]
	return swapped
}

// uuidProblem says why a UUID cannot name a machine, or "" when it can.
func uuidProblem(uuid []byte) string {
	if bytes.Count(uuid, []byte{0}) == len(uuid) {
		return "all zero (not present)"
	}
	if bytes.Count(uuid, []byte{0xFF}) == len(uuid) {
		return "all FFh (not set)"
	}
	display := uuidDisplay(uuidByteOrderSwapped(uuid))
	stored := uuidDisplay(uuid)
	for _, known := range knownDefaultUUIDs {
		if display == known || stored == known {
			return "a published default"
		}
	}
	var seen [256]bool
	distinct := 0
	for _, value := range uuid {
		if !seen[value] {
			seen[value] = true
			distinct++
		}
	}
	if distinct < minimumUUIDByteValues {
		return "too uniform"
	}
	return ""
}

// uuidDisplay formats 16 bytes as an upper-case 8-4-4-4-12 UUID, in the order given. Only deny-list comparisons use it.
func uuidDisplay(uuid []byte) string {
	const digits = "0123456789ABCDEF"
	var text strings.Builder
	for i, value := range uuid {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			text.WriteByte('-')
		}
		text.WriteByte(digits[value>>4])
		text.WriteByte(digits[value&0x0F])
	}
	return text.String()
}

// canonicalSerial applies the version 1 serial canonicalization; ok is false for a byte outside printable ASCII, which the
// same firmware string may reach other software in through different code pages.
func canonicalSerial(raw []byte) ([]byte, bool) {
	space := func(c byte) bool {
		return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == 0 || c == '\v' || c == '\f'
	}
	start, end := 0, len(raw)
	for start < end && space(raw[start]) {
		start++
	}
	for end > start && space(raw[end-1]) {
		end--
	}
	out := make([]byte, 0, end-start)
	pending := false
	for _, c := range raw[start:end] {
		if space(c) {
			pending = true
			continue
		}
		if c < 0x21 || c > 0x7E {
			clear(out)
			return nil, false
		}
		if pending {
			out = append(out, ' ')
			pending = false
		}
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		out = append(out, c)
	}
	return out, true
}

// serialProblem says why a canonical serial cannot name a machine, or "" when it can.
func serialProblem(serial string) string {
	if placeholderSerials[serial] {
		return "a published placeholder"
	}
	for _, phrase := range placeholderPhrases {
		if strings.Contains(serial, phrase) {
			return "a placeholder phrase"
		}
	}
	if stripped := strings.NewReplacer("-", "", "/", "", " ", "").Replace(serial); stripped == "" || strings.Count(stripped, stripped[:1]) == len(stripped) {
		return "one repeated character"
	}
	if strings.Trim(serial, "01") == "" {
		return "only the digits 0 and 1"
	}
	if parts := strings.Split(serial, "."); len(parts) > 1 {
		version := true
		for _, part := range parts {
			if part == "" || strings.Trim(part, "0123456789") != "" {
				version = false
				break
			}
		}
		if version {
			return "a version number"
		}
	}
	if len(serial) < minimumSerialLength {
		return "too short"
	}
	return ""
}

// Published placeholder values (research/runs/2026-11-15/v0.8.0-hwid-key-derivation, section 2.3: the GLPI default
// blacklist, the FOG Project host identity reference, Broadcom KB 451886 and field reports; the values are data, not code
// of those projects). A match only keeps a new history from taking that source: an existing history keeps the source and
// key it was made with, so extending these lists never changes the key of a history.
var knownDefaultUUIDs = []string{
	// AMI Aptio default, wire bytes 00 02 00 03 00 04 00 05 00 06 00 07 00 08 00 09.
	"03000200-0400-0500-0006-000700080009",
	"6AB5B300-538D-1014-9FB5-B0684D007B53",
	"01010101-0101-0101-0101-010101010101",
	"12345678-1234-5678-90AB-CDDEEFAABBCC",
}

var placeholderSerials = map[string]bool{
	"TO BE FILLED BY O.E.M.": true, "TO BE FILLED BY OEM": true, "TO BE SET BY OEM": true, "FILL BY OEM": true,
	"DEFAULT STRING": true, "BASE BOARD SERIAL NUMBER": true, "BASEBOARD SERIAL NUMBER": true, "SYSTEM SERIAL NUMBER": true,
	"SYSTEMSERIALNUMB": true, "CHASSIS SERIAL NUMBER": true, "CHASSIS ASSET TAG": true, "ASSET TAG": true, "NO ASSET TAG": true,
	"NO ASSET INFORMATION": true, "ASSET-1234567890": true, "ENTER SERIAL": true, "NOT SPECIFIED": true, "NOT PRESENT": true,
	"NOT SETTABLE": true, "NOT APPLICABLE": true, "NOT AVAILABLE": true, "N/A": true, "NA": true, "NONE": true, "NULL": true,
	"UNKNOWN": true, "UNKNOW": true, "INVALID": true, "EMPTY": true, "DEFAULT": true, "SERIAL": true, "SN": true, "S/N": true,
	"OEM": true, "O.E.M.": true, "OEM_SERIAL": true, "EVAL": true, "IATPASS": true, "ALASKA": true, "TULIP COMPUTERS": true,
	"NNNNNNN": true, "XXXXXXXXXX": true, "XXXXXXXXXXX": true, "SYS-1234567890": true, "SYS-9876543210": true, "SN-12345": true,
	"SN-1234567890": true, "SN-123456FVGV3I0B8O5N6N7K": true, "MB-1234567890": true, "0123456789": true, "01234567890": true,
	"12345": true, "123456": true, "1234567": true, "12345678": true, "123456789": true, "1234567890": true,
	"123456789000": true, "12345678901234567": true,
}

var placeholderPhrases = []string{
	"O.E.M", "FILLED BY", "FILL BY OEM", "SET BY OEM", "DEFAULT STRING", "SERIAL NUMBER", "ASSET TAG", "ENTER SERIAL",
	"NOT SPECIFIED", "NOT APPLICABLE", "NOT AVAILABLE", "NOT PRESENT", "NOT SETTABLE",
}

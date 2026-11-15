package history

import (
	"bytes"
	"encoding/binary"
	"errors"
	"runtime"
	"testing"
)

// Synthetic SMBIOS tables (DMTF DSP0134 layouts) for the motherboard identity (machine.go).

type testStructure struct {
	kind byte
	// formatted is the formatted area after the 4-byte structure header.
	formatted []byte
	strings   []string
}

// smbiosTable lays structures out as a RawSMBIOSData buffer, ended by a type 127 structure.
func smbiosTable(structures ...testStructure) []byte {
	var data []byte
	for i, structure := range structures {
		data = append(data, structure.kind, byte(4+len(structure.formatted)), byte(i), 0)
		data = append(data, structure.formatted...)
		if len(structure.strings) == 0 {
			data = append(data, 0, 0)
			continue
		}
		for _, text := range structure.strings {
			data = append(append(data, text...), 0)
		}
		data = append(data, 0)
	}
	data = append(data, smbiosEndOfTable, 4, 0xFF, 0xFF, 0, 0)
	raw := []byte{0, 3, 5, 0, 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(raw[4:], uint32(len(data)))
	return append(raw, data...)
}

// type1 is a System Information structure of SMBIOS 2.4 (1Bh bytes) whose UUID at 08h is uuid.
func type1(uuid []byte) testStructure {
	formatted := make([]byte, 0x1B-4)
	formatted[0x04-4] = 1
	copy(formatted[type1UUIDOffset-4:], uuid)
	return testStructure{kind: smbiosSystem, formatted: formatted, strings: []string{"WWlogs Test Systems"}}
}

// type2 is a Baseboard Information structure whose serial at 07h is string 2 and whose board type at 0Dh is boardType.
func type2(serial string, boardType byte) testStructure {
	formatted := make([]byte, 0x0F-4)
	formatted[0x04-4] = 1
	formatted[type2SerialOffset-4] = 2
	formatted[type2BoardTypeOffset-4] = boardType
	return testStructure{kind: smbiosBaseboard, formatted: formatted, strings: []string{"WWlogs Test Boards", serial}}
}

var testUUID = mustHex("474c5757455454538000000000000001")

func TestSMBIOSIdentifiersAreTakenAsStored(t *testing.T) {
	machine, err := machineFromSMBIOS(smbiosTable(testStructure{kind: 0, formatted: make([]byte, 20), strings: []string{"BIOS"}}, type1(testUUID), type2("WWLOGSTEST0001", boardTypeMotherboard)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(machine.SystemUUID, testUUID) || string(machine.BaseboardSerial) != "WWLOGSTEST0001" {
		t.Fatalf("machine = %x / %q", machine.SystemUUID, machine.BaseboardSerial)
	}
	// The identifiers are copies: wiping the table leaves them.
	table := smbiosTable(type1(testUUID))
	machine, err = machineFromSMBIOS(table)
	clear(table)
	if err != nil || !bytes.Equal(machine.SystemUUID, testUUID) {
		t.Fatalf("machine after the table was wiped = %x (%v)", machine.SystemUUID, err)
	}
	machine.wipe()
	if !bytes.Equal(machine.SystemUUID, make([]byte, 16)) {
		t.Fatal("wipe left the UUID")
	}
}

func TestAmbiguousOrMissingIdentifiersAreLeftOut(t *testing.T) {
	other := mustHex("474c575745545453800000000000ff02")
	short := testStructure{kind: smbiosSystem, formatted: make([]byte, 0x08-4)}
	noSerial := type2("unused", boardTypeMotherboard)
	noSerial.formatted[type2SerialOffset-4] = 0
	noSerial.strings = noSerial.strings[:1]
	for name, c := range map[string]struct {
		table        []byte
		uuid, serial bool
	}{
		"two agreeing UUIDs":                {smbiosTable(type1(testUUID), type1(testUUID)), true, false},
		"two different UUIDs":               {smbiosTable(type1(testUUID), type1(other)), false, false},
		"an SMBIOS 2.0 system structure":    {smbiosTable(short), false, false},
		"no board":                          {smbiosTable(type1(testUUID)), true, false},
		"two boards, one motherboard":       {smbiosTable(type2("WWLOGSDAUGHTER1", 0x03), type2("WWLOGSTEST0001", boardTypeMotherboard)), false, true},
		"two boards, no motherboard":        {smbiosTable(type2("WWLOGSDAUGHTER1", 0x03), type2("WWLOGSTEST0001", 0x03)), false, false},
		"two motherboards":                  {smbiosTable(type2("WWLOGSBOARD0001", boardTypeMotherboard), type2("WWLOGSTEST0001", boardTypeMotherboard)), false, false},
		"a board without a serial":          {smbiosTable(noSerial), false, false},
		"one board, whatever its type says": {smbiosTable(type2("WWLOGSTEST0001", 0x03)), false, true},
	} {
		machine, err := machineFromSMBIOS(c.table)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if (machine.SystemUUID != nil) != c.uuid || (machine.BaseboardSerial != nil) != c.serial {
			t.Fatalf("%s: uuid %t, serial %t", name, machine.SystemUUID != nil, machine.BaseboardSerial != nil)
		}
	}
}

func TestMalformedSMBIOSTableIsRefused(t *testing.T) {
	good := smbiosTable(type1(testUUID))
	overlong := bytes.Clone(good)
	binary.LittleEndian.PutUint32(overlong[4:], uint32(len(good)))
	structureOverflow := bytes.Clone(good)
	structureOverflow[smbiosRawHeader+1] = 0xF0
	unterminated := append(bytes.Clone(good[:smbiosRawHeader]), []byte{smbiosSystem, 0x1B}...)
	unterminated = append(unterminated, make([]byte, 0x1B-2)...)
	unterminated = append(unterminated, 'A', 'B')
	binary.LittleEndian.PutUint32(unterminated[4:], uint32(len(unterminated)-smbiosRawHeader))
	for name, table := range map[string][]byte{
		"shorter than its header":          good[:5],
		"declares more than it holds":      overlong,
		"a structure past the end":         structureOverflow,
		"a string set without terminators": unterminated,
	} {
		if _, err := machineFromSMBIOS(table); !errors.Is(err, errSMBIOSMalformed) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestNewHistoryRefusesPlaceholderIdentifiers(t *testing.T) {
	for name, uuid := range map[string][]byte{
		"all zero":             make([]byte, 16),
		"all FFh":              bytes.Repeat([]byte{0xFF}, 16),
		"the AMI default":      mustHex("00020003000400050006000700080009"),
		"a published default":  mustHex("00b3b56a8d531410" + "9fb5b0684d007b53"),
		"too few byte values":  mustHex("01020304010203040102030401020304"),
		"the low-diversity 01": bytes.Repeat([]byte{0x01}, 16),
	} {
		if _, err := identifierFor(sourceSystemUUID, Machine{SystemUUID: uuid}, true); !errors.Is(err, errNoMachineIdentity) {
			t.Fatalf("a new history took the UUID %s: %v", name, err)
		}
		// An existing history made from it still finds it; its key check decides.
		if got, err := identifierFor(sourceSystemUUID, Machine{SystemUUID: uuid}, false); err != nil || !bytes.Equal(got, uuid) {
			t.Fatalf("an existing history did not get the UUID %s: %v", name, err)
		}
	}
	if got, err := identifierFor(sourceSystemUUID, Machine{SystemUUID: testUUID}, true); err != nil || !bytes.Equal(got, testUUID) {
		t.Fatalf("a usable UUID was refused: %v", err)
	}
	for _, serial := range []string{"To Be Filled By O.E.M.", "Default string", "  ", "123456789", "00-00-00", "0101", "1.2.3", "ABC", "Base Board Serial Number"} {
		if _, err := identifierFor(sourceBaseboardSerial, Machine{BaseboardSerial: []byte(serial)}, true); !errors.Is(err, errNoMachineIdentity) {
			t.Fatalf("a new history took the serial %q: %v", serial, err)
		}
	}
	for raw, want := range map[string]string{"WWLOGSTEST0001": "WWLOGSTEST0001", "  wwlogs \t test\x000001\x00 ": "WWLOGS TEST 0001", "ab12-CD34": "AB12-CD34"} {
		got, err := identifierFor(sourceBaseboardSerial, Machine{BaseboardSerial: []byte(raw)}, true)
		if err != nil || string(got) != want {
			t.Fatalf("serial %q gives %q (%v), want %q", raw, got, err, want)
		}
	}
	if _, err := identifierFor(sourceBaseboardSerial, Machine{BaseboardSerial: []byte("WWLOGS\xc3\xa9TEST")}, false); !errors.Is(err, errNoMachineIdentity) {
		t.Fatalf("a serial outside printable ASCII was taken: %v", err)
	}
}

func TestNewHistoryPrefersTheUUID(t *testing.T) {
	for name, c := range map[string]struct {
		machine Machine
		source  string
	}{
		"both usable":                {testMachine(), sourceSystemUUID},
		"UUID unset, serial usable":  {Machine{SystemUUID: make([]byte, 16), BaseboardSerial: []byte("WWLOGSTEST0001")}, sourceBaseboardSerial},
		"no UUID, serial usable":     {Machine{BaseboardSerial: []byte("WWLOGSTEST0001")}, sourceBaseboardSerial},
		"UUID usable, serial absent": {Machine{SystemUUID: testUUID}, sourceSystemUUID},
		"neither usable":             {Machine{SystemUUID: make([]byte, 16), BaseboardSerial: []byte("Default string")}, ""},
	} {
		source, identifier, err := newHistorySource(c.machine)
		if source != c.source || (c.source == "") != (err != nil) || (err == nil && len(identifier) == 0) {
			t.Fatalf("%s: source %q (%v)", name, source, err)
		}
	}
}

func TestUUIDByteOrderSwapIsItsOwnInverse(t *testing.T) {
	swapped := uuidByteOrderSwapped(testUUID)
	want := mustHex("57574c47" + "5445" + "5354" + "8000000000000001")
	if !bytes.Equal(swapped, want) || !bytes.Equal(uuidByteOrderSwapped(swapped), testUUID) {
		t.Fatalf("swapped %x", swapped)
	}
}

// TestThisMachinesFirmwareNamesAMotherboard reads this computer's SMBIOS table, as the product does, and checks that a new
// history could take an identifier from it. Nothing of the table or the identifier is printed.
func TestThisMachinesFirmwareNamesAMotherboard(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the firmware table is read on Windows")
	}
	raw, err := readFirmwareTable()
	if err != nil {
		t.Fatal(err)
	}
	defer clear(raw)
	machine, err := machineFromSMBIOS(raw)
	if err != nil {
		t.Fatal(err)
	}
	defer machine.wipe()
	source, identifier, err := newHistorySource(machine)
	if err != nil {
		t.Fatalf("this computer's firmware names no usable motherboard identifier: %v", err)
	}
	clear(identifier)
	t.Logf("this computer gives a new history the source %s (%d SMBIOS bytes read)", source, len(raw))
}

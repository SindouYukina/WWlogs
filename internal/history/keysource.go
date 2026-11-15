package history

import (
	"bytes"
	"crypto/subtle"
	"errors"
	"fmt"

	"wwlogs/internal/capture"
)

// KeyInputs supply what the key of a history derives from (keyderive.go). A zero KeyInputs uses this computer and this
// build: the motherboard identifiers in the firmware's SMBIOS table and the game protocol key the build embeds. Tests pass
// synthetic ones and never derive a real key.
type KeyInputs struct {
	// Machine returns the motherboard identifiers; nil reads them from the SMBIOS table (GetSystemFirmwareTable 'RSMB').
	// The store wipes what it returns.
	Machine func() (Machine, error)
	// ProtocolKey returns the game protocol RSA private key as PEM or DER; nil uses the key this build embeds for traffic
	// decryption (capture.ProtocolKeyMaterial). The store clears what it returns.
	ProtocolKey func() ([]byte, error)
}

// platformKeyInputs is what a zero KeyInputs uses. Tests of this package replace it with synthetic inputs.
var platformKeyInputs = KeyInputs{}

var (
	// errHardwareIdentity: the motherboard identifiers could not be read, or none of them can be used.
	errHardwareIdentity = errors.New("the motherboard identity is unavailable")
	// errKeyMaterial: the protocol key this build embeds is missing or not an RSA private key.
	errKeyMaterial = errors.New("the history key material is unavailable")
)

func (k KeyInputs) zero() bool { return k.Machine == nil && k.ProtocolKey == nil }

func (k KeyInputs) machine() (Machine, error) {
	if k.Machine != nil {
		machine, err := k.Machine()
		if err != nil {
			return Machine{}, fmt.Errorf("%w: %v", errHardwareIdentity, err)
		}
		return machine, nil
	}
	raw, err := readFirmwareTable()
	if err != nil {
		return Machine{}, fmt.Errorf("%w: %v", errHardwareIdentity, err)
	}
	defer clear(raw)
	machine, err := machineFromSMBIOS(raw)
	if err != nil {
		return Machine{}, fmt.Errorf("%w: %v", errHardwareIdentity, err)
	}
	return machine, nil
}

// protocolKey returns the canonical protocol key; the caller clears it.
func (k KeyInputs) protocolKey() ([]byte, error) {
	read := k.ProtocolKey
	if read == nil {
		read = capture.ProtocolKeyMaterial
	}
	material, err := read()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errKeyMaterial, err)
	}
	defer clear(material)
	canonical, err := canonicalProtocolKey(material)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errKeyMaterial, err)
	}
	return canonical, nil
}

// derivedKey is one key a history may have been made with, with the inputs it came from.
type derivedKey struct {
	key, check []byte
	source     string
	// variant names a tolerated form of the identifier ("uuid-byte-order-swapped"); empty for the identifier as read.
	variant string
}

func wipeKeys(keys []derivedKey) {
	for _, key := range keys {
		clear(key.key)
	}
}

// newHistoryKey derives the key of a new history: the UUID when it is usable, the serial otherwise (machine.go). It also
// returns the record to keep beside the history.
func newHistoryKey(inputs KeyInputs) (derivedKey, keyRecord, error) {
	machine, err := inputs.machine()
	if err != nil {
		return derivedKey{}, keyRecord{}, err
	}
	defer machine.wipe()
	source, identifier, err := newHistorySource(machine)
	if err != nil {
		return derivedKey{}, keyRecord{}, fmt.Errorf("%w: %v", errHardwareIdentity, err)
	}
	defer clear(identifier)
	protocolKey, err := inputs.protocolKey()
	if err != nil {
		return derivedKey{}, keyRecord{}, err
	}
	defer clear(protocolKey)
	key, check, err := deriveHistoryKey(source, identifier, protocolKey)
	if err != nil {
		return derivedKey{}, keyRecord{}, err
	}
	return derivedKey{key: key, check: check, source: source}, newKeyRecord(source, check, protocolKeyID(protocolKey)), nil
}

// existingHistoryKeys derives every key an existing history of sources may have: the identifier of each source as read and,
// for the UUID, in its other byte order. A source this machine does not report is left out; none at all is an error. The
// protocol key id is returned to tell a changed protocol key apart in diagnostics.
func existingHistoryKeys(inputs KeyInputs, sources ...string) ([]derivedKey, string, error) {
	machine, err := inputs.machine()
	if err != nil {
		return nil, "", err
	}
	defer machine.wipe()
	protocolKey, err := inputs.protocolKey()
	if err != nil {
		return nil, "", err
	}
	defer clear(protocolKey)
	var keys []derivedKey
	var missing []error
	for _, source := range sources {
		identifier, err := identifierFor(source, machine, false)
		if err != nil {
			missing = append(missing, err)
			continue
		}
		identifiers := [][]byte{identifier}
		if source == sourceSystemUUID {
			if swapped := uuidByteOrderSwapped(identifier); !bytes.Equal(swapped, identifier) {
				identifiers = append(identifiers, swapped)
			}
		}
		for i, value := range identifiers {
			key, check, err := deriveHistoryKey(source, value, protocolKey)
			clear(value)
			if err != nil {
				wipeKeys(keys)
				return nil, "", err
			}
			variant := ""
			if i > 0 {
				variant = "uuid-byte-order-swapped"
			}
			keys = append(keys, derivedKey{key: key, check: check, source: source, variant: variant})
		}
	}
	if len(keys) == 0 {
		return nil, "", fmt.Errorf("%w: %v", errHardwareIdentity, errors.Join(missing...))
	}
	return keys, protocolKeyID(protocolKey), nil
}

// matchingKey is the key whose check value equals the record's, or nil.
func matchingKey(keys []derivedKey, record keyRecord) *derivedKey {
	recorded := record.check()
	for i := range keys {
		if keys[i].source == record.Source && subtle.ConstantTimeCompare(keys[i].check, recorded) == 1 {
			return &keys[i]
		}
	}
	return nil
}

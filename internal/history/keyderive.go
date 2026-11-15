package history

import (
	"crypto/hkdf"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
)

// The history key, derivation version 2.
//
// The key of a history is not stored anywhere: it is derived, every time the history opens, from two inputs that stay the
// same on the same computer and the same game protocol key:
//
//   - the motherboard identifier (machine.go): the SMBIOS Type 1 UUID, or the Type 2 serial number a history recorded;
//   - the game protocol RSA private key this build embeds for traffic decryption (capture.ProtocolKeyMaterial), in its
//     canonical PKCS#1 DER form: parsed from PEM or DER (PKCS#1 or PKCS#8), validated, and encoded again by crypto/x509,
//     which is deterministic for a given key. PEM against DER, PKCS#1 against PKCS#8, line endings, comment lines and
//     surrounding white space therefore never change the key; another key always does.
//
// The specification is fixed (research/runs/2026-11-15/v0.8.0-hwid-preflight-safety, section 2.1):
//
//	IKM  = "WWLOGS-HWID-IKM" || 0x02
//	       || len(source) u8 || source
//	       || len(identifier) u16be || identifier
//	       || len(protocol key) u32be || protocol key (canonical PKCS#1 DER)
//	salt = "WWlogs/history/hwid-kdf/v1/salt"
//	key  = HKDF-SHA256(IKM, salt, "WWlogs/history/aes-256-xts/v2" || 0x00 || source, 64)   K1 = key[0:32], K2 = key[32:64]
//	kcv  = HKDF-SHA256(IKM, salt, "WWlogs/history/key-check/v2"   || 0x00 || source, 16)
//	key id of the protocol key = SHA-256("WWlogs/history/rsa-key-id/v1" || 0x00 || protocol key)[0:8]
//
// Every field is length-prefixed, so no two different inputs encode to the same bytes. There is no random salt, no secret
// and nothing to keep or back up; the build, the Windows account, the language, the time zone and the clock take no part.
// A WWlogs update that keeps the game protocol key keeps the key of every history. Changing any constant, encoding or
// canonicalization rule above is a new derivation version, never an edit of version 2: the fixed test vectors in
// tests/history/keyderive_test.go hold it.
//
// HKDF adds no work factor: anyone who has both inputs (the UUID is readable by every program on the computer, and the
// protocol key ships inside every WWlogs build) can derive the key. The encryption keeps the file unreadable away from that
// computer and that key, not from software running on it.

const (
	derivationVersion = 2
	historyKeyCheck   = 16

	derivationMagic      = "WWLOGS-HWID-IKM"
	derivationSalt       = "WWlogs/history/hwid-kdf/v1/salt"
	derivationKeyInfo    = "WWlogs/history/aes-256-xts/v2"
	derivationCheckInfo  = "WWlogs/history/key-check/v2"
	protocolKeyIDLabel   = "WWlogs/history/rsa-key-id/v1\x00"
	protocolKeyIDBytes   = 8
	maxIdentifierLength  = 0xFFFF
	maxProtocolKeyLength = 0x7FFFFFFF
)

var (
	errDerivationInput = errors.New("invalid history key derivation input")
	errEqualKeyHalves  = errors.New("the derived XTS key halves are equal")
	errNotRSAKey       = errors.New("the protocol key is not an RSA private key")
)

// derivationInput encodes the input keying material of version 2.
func derivationInput(source string, identifier, protocolKey []byte) ([]byte, error) {
	if len(source) == 0 || len(source) > 255 || len(identifier) == 0 || len(identifier) > maxIdentifierLength ||
		len(protocolKey) == 0 || int64(len(protocolKey)) > maxProtocolKeyLength {
		return nil, errDerivationInput
	}
	input := make([]byte, 0, len(derivationMagic)+2+len(source)+2+len(identifier)+4+len(protocolKey))
	input = append(input, derivationMagic...)
	input = append(input, derivationVersion, byte(len(source)))
	input = append(input, source...)
	input = binary.BigEndian.AppendUint16(input, uint16(len(identifier)))
	input = append(input, identifier...)
	input = binary.BigEndian.AppendUint32(input, uint32(len(protocolKey)))
	input = append(input, protocolKey...)
	return input, nil
}

// deriveHistoryKey derives the 64-byte AES-256-XTS key of a history and its 16-byte key check value from the identifier of
// source and the canonical protocol key. The key is secret; the caller clears it. The check value only lets someone who
// already has both inputs confirm them, as the encrypted file itself does.
func deriveHistoryKey(source string, identifier, protocolKey []byte) (key, check []byte, err error) {
	input, err := derivationInput(source, identifier, protocolKey)
	if err != nil {
		return nil, nil, err
	}
	defer clear(input)
	key, err = hkdf.Key(sha256.New, input, []byte(derivationSalt), derivationKeyInfo+"\x00"+source, historyKeySize)
	if err != nil {
		return nil, nil, fmt.Errorf("derive history key: %w", err)
	}
	// IEEE 1619 and NIST SP 800-38E need two different AES keys; equal halves (probability 2^-256) are refused.
	if subtle.ConstantTimeCompare(key[:historyKeySize/2], key[historyKeySize/2:]) == 1 {
		clear(key)
		return nil, nil, errEqualKeyHalves
	}
	check, err = hkdf.Key(sha256.New, input, []byte(derivationSalt), derivationCheckInfo+"\x00"+source, historyKeyCheck)
	if err != nil {
		clear(key)
		return nil, nil, fmt.Errorf("derive history key check: %w", err)
	}
	return key, check, nil
}

// canonicalProtocolKey returns the canonical PKCS#1 DER of an RSA private key given as PEM (PKCS#1 "RSA PRIVATE KEY" or
// PKCS#8 "PRIVATE KEY") or as DER of either. The result holds the private exponent: it is secret and the caller clears it.
func canonicalProtocolKey(material []byte) ([]byte, error) {
	der := material
	if block, _ := pem.Decode(material); block != nil {
		der = block.Bytes
		defer clear(block.Bytes)
	}
	key, err := x509.ParsePKCS1PrivateKey(der)
	if err != nil {
		parsed, pkcs8Err := x509.ParsePKCS8PrivateKey(der)
		if pkcs8Err != nil {
			return nil, errNotRSAKey
		}
		rsaKey, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("%w: PKCS#8 holds a %T", errNotRSAKey, parsed)
		}
		key = rsaKey
	}
	if err := key.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", errNotRSAKey, err)
	}
	key.Precompute()
	return x509.MarshalPKCS1PrivateKey(key), nil
}

// protocolKeyID names a canonical protocol key without revealing it: it says which protocol key a history was made with,
// for a later protocol key change, and lets only someone who has the key confirm it.
func protocolKeyID(protocolKey []byte) string {
	digest := sha256.New()
	digest.Write([]byte(protocolKeyIDLabel))
	digest.Write(protocolKey)
	return hex.EncodeToString(digest.Sum(nil)[:protocolKeyIDBytes])
}

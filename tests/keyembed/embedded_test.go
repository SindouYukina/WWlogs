package keyembed

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	want := []byte("synthetic RSA PEM test payload")
	payload, err := Encrypt(want)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payload.Ciphertext, string(want)) || strings.Contains(payload.EncryptionKey, string(want)) {
		t.Fatal("encrypted payload contains its plaintext")
	}

	got, err := Decrypt(payload)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(got)
	if string(got) != string(want) {
		t.Fatal("decrypted payload does not match its input")
	}
}

func TestDecryptRejectsTamperedCiphertext(t *testing.T) {
	payload, err := Encrypt([]byte("synthetic RSA PEM test payload"))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := base64.RawStdEncoding.DecodeString(payload.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	encoded[len(encoded)-1] ^= 1
	payload.Ciphertext = base64.RawStdEncoding.EncodeToString(encoded)
	clear(encoded)

	if plaintext, err := Decrypt(payload); err == nil {
		clear(plaintext)
		t.Fatal("Decrypt() accepted a modified ciphertext")
	}
}

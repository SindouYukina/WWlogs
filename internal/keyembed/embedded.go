package keyembed

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
)

var associatedData = []byte("WWlogs embedded RSA PEM v1")

// Payload holds an authenticated ciphertext and its per-build AES key.
type Payload struct {
	Ciphertext    string
	EncryptionKey string
}

// Encrypt protects a PEM payload for inclusion in a self-contained executable.
func Encrypt(plaintext []byte) (Payload, error) {
	var key [32]byte
	if _, err := io.ReadFull(rand.Reader, key[:]); err != nil {
		return Payload{}, err
	}
	defer clear(key[:])

	block, err := aes.NewCipher(key[:])
	if err != nil {
		return Payload{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return Payload{}, err
	}

	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return Payload{}, err
	}
	sealed := aead.Seal(nil, nonce, plaintext, associatedData)
	defer clear(sealed)

	encoded := make([]byte, 0, len(nonce)+len(sealed))
	encoded = append(encoded, nonce...)
	encoded = append(encoded, sealed...)
	defer clear(encoded)

	return Payload{
		Ciphertext:    base64.RawStdEncoding.EncodeToString(encoded),
		EncryptionKey: base64.RawStdEncoding.EncodeToString(key[:]),
	}, nil
}

// Decrypt authenticates and decodes an embedded PEM payload in memory.
func Decrypt(payload Payload) ([]byte, error) {
	if payload.Ciphertext == "" || payload.EncryptionKey == "" {
		return nil, errors.New("embedded key payload is missing")
	}

	key, err := base64.RawStdEncoding.DecodeString(payload.EncryptionKey)
	if err != nil || len(key) != 32 {
		clear(key)
		return nil, errors.New("embedded key material is invalid")
	}
	defer clear(key)

	encoded, err := base64.RawStdEncoding.DecodeString(payload.Ciphertext)
	if err != nil {
		return nil, errors.New("embedded key payload encoding is invalid")
	}
	defer clear(encoded)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("embedded key cipher is unavailable")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("embedded key cipher is unavailable")
	}
	if len(encoded) < aead.NonceSize()+aead.Overhead() {
		return nil, errors.New("embedded key ciphertext is truncated")
	}

	plaintext, err := aead.Open(nil, encoded[:aead.NonceSize()], encoded[aead.NonceSize():], associatedData)
	if err != nil {
		return nil, errors.New("embedded key payload authentication failed")
	}
	return plaintext, nil
}

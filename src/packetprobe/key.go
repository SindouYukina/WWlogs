package main

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"

	"wwlogs/internal/keyembed"
)

const (
	rsaWidth = 256
)

type staticRSA struct {
	e int
	n *big.Int
	d *big.Int
}

func parsePrivateKeyPEM(data []byte) (*staticRSA, error) {
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "RSA PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("expected one PKCS#1 RSA PRIVATE KEY PEM block")
	}
	defer clear(block.Bytes)
	privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse local RSA PEM: %w", err)
	}
	if err := privateKey.Validate(); err != nil {
		return nil, fmt.Errorf("validate local RSA PEM: %w", err)
	}
	if privateKey.N.BitLen() != rsaWidth*8 {
		return nil, errors.New("local RSA PEM has an unexpected modulus size")
	}
	return &staticRSA{e: privateKey.E, n: new(big.Int).Set(privateKey.N), d: new(big.Int).Set(privateKey.D)}, nil
}

func loadEmbeddedPrivateKey() (*staticRSA, error) {
	data, err := keyembed.Decrypt(keyembed.Payload{
		Ciphertext:    embeddedKeyCiphertext,
		EncryptionKey: embeddedKeyEncryptionKey,
	})
	if err != nil {
		return nil, fmt.Errorf("decrypt embedded RSA key: %w", err)
	}
	defer clear(data)
	return parsePrivateKeyPEM(data)
}

func (key *staticRSA) decryptSessionKey(ciphertext []byte) ([]byte, error) {
	if key == nil || key.n == nil || key.d == nil {
		return nil, errors.New("RSA material is unavailable")
	}
	if len(ciphertext) != rsaWidth {
		return nil, fmt.Errorf("RSA ciphertext length %d, expected %d", len(ciphertext), rsaWidth)
	}
	c := new(big.Int).SetBytes(ciphertext)
	if c.Cmp(key.n) >= 0 {
		return nil, errors.New("RSA ciphertext is out of range")
	}
	m := new(big.Int).Exp(c, key.d, key.n)
	em := make([]byte, rsaWidth)
	encoded := m.Bytes()
	if len(encoded) > len(em) {
		return nil, errors.New("RSA plaintext exceeds modulus size")
	}
	copy(em[len(em)-len(encoded):], encoded)
	for i := range encoded {
		encoded[i] = 0
	}
	m.SetInt64(0)
	c.SetInt64(0)
	if len(em) < 11 || em[0] != 0 || em[1] != 2 {
		clear(em)
		return nil, errors.New("RSA block is not PKCS#1 v1.5 encryption padding")
	}
	separator := -1
	for i := 2; i < len(em); i++ {
		if em[i] == 0 {
			separator = i
			break
		}
	}
	if separator < 10 || len(em)-separator-1 != 32 {
		clear(em)
		return nil, errors.New("RSA block does not contain a 32-byte session key")
	}
	sessionKey := append([]byte(nil), em[separator+1:]...)
	clear(em)
	return sessionKey, nil
}

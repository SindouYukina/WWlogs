package main

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
)

const rsaWidth = 256

type staticRSA struct {
	e int
	n *big.Int
	d *big.Int
}

func loadPrivateKeyPEM(path string) (*staticRSA, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read local RSA key file: %w", err)
	}
	block, rest := pem.Decode(data)
	clear(data)
	if block == nil || block.Type != "RSA PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("expected one PKCS#1 RSA PRIVATE KEY PEM block")
	}
	privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	clear(block.Bytes)
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
	clear(encoded)
	m.SetInt64(0)
	c.SetInt64(0)
	if em[0] != 0 || em[1] != 2 {
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

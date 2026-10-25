package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
)

func TestPEMKeyDecryptsSessionKey(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der := x509.MarshalPKCS1PrivateKey(privateKey)
	encoded := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})
	key, err := parsePrivateKeyPEM(encoded)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte("0123456789abcdef0123456789abcdef")
	ciphertext, err := rsa.EncryptPKCS1v15(rand.Reader, &privateKey.PublicKey, want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := key.decryptSessionKey(ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("session key mismatch")
	}
}

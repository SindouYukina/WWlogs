//go:build windows

package history

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// dpapiProvider seals the history key with DPAPI (CryptProtectData) for the current Windows user. The sealed key opens only
// in a process of the same user on the same machine (or the user's roaming profile); DPAPI protects it with the user's
// logon credentials, not with a TPM, and any process running as the same user can unseal it. The entropy only separates
// this key from other DPAPI blobs of the user; it is not a secret.
type dpapiProvider struct{}

var dpapiEntropy = []byte("WWlogs Combat History key v1")

// DPAPIKeyProvider is the default key provider on Windows.
func DPAPIKeyProvider() KeyProvider { return dpapiProvider{} }

func defaultKeyProvider() KeyProvider { return dpapiProvider{} }

func (dpapiProvider) ID() string { return "dpapi-user" }

func blobOf(data []byte) *windows.DataBlob {
	if len(data) == 0 {
		return &windows.DataBlob{}
	}
	return &windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
}

// takeBlob copies a DPAPI output buffer and frees it, clearing it first.
func takeBlob(blob windows.DataBlob) []byte {
	if blob.Data == nil {
		return nil
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(blob.Data)))
	raw := unsafe.Slice(blob.Data, blob.Size)
	out := append([]byte(nil), raw...)
	clear(raw)
	return out
}

func (dpapiProvider) Seal(key []byte) ([]byte, error) {
	var out windows.DataBlob
	description, err := windows.UTF16PtrFromString("WWlogs Combat History")
	if err != nil {
		return nil, err
	}
	if err := windows.CryptProtectData(blobOf(key), description, blobOf(dpapiEntropy), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, fmt.Errorf("DPAPI seal: %w", err)
	}
	return takeBlob(out), nil
}

func (dpapiProvider) Unseal(sealed []byte) ([]byte, error) {
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(blobOf(sealed), nil, blobOf(dpapiEntropy), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, fmt.Errorf("DPAPI unseal: %w", err)
	}
	return takeBlob(out), nil
}

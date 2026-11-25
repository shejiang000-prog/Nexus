package main

import (
	"crypto/sha256"
	"encoding/asn1"
	"encoding/binary"
	"errors"
	"math/big"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	platformKSP = "Microsoft Platform Crypto Provider"
	softwareKSP = "Microsoft Software Key Storage Provider"
	silentFlag  = 0x40
)

var (
	ncrypt       = windows.NewLazySystemDLL("ncrypt.dll")
	openProvider = ncrypt.NewProc("NCryptOpenStorageProvider")
	openKey      = ncrypt.NewProc("NCryptOpenKey")
	createKey    = ncrypt.NewProc("NCryptCreatePersistedKey")
	finalizeKey  = ncrypt.NewProc("NCryptFinalizeKey")
	exportKey    = ncrypt.NewProc("NCryptExportKey")
	signHash     = ncrypt.NewProc("NCryptSignHash")
	freeObject   = ncrypt.NewProc("NCryptFreeObject")
)

type deviceKey struct {
	handle uintptr
}

// Caller supplies only fixed identifiers and a validated hexadecimal alias.
func wide(s string) *uint16 {
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		panic(err)
	}
	return p
}

func acquireKey(provider, alias string, create bool) (*deviceKey, error) {
	var storage, key uintptr
	rc, _, _ := openProvider.Call(uintptr(unsafe.Pointer(&storage)), uintptr(unsafe.Pointer(wide(provider))), 0)
	if rc != 0 {
		return nil, syscall.Errno(rc)
	}
	defer freeObject.Call(storage)
	name := wide("Nexus.Desktop." + alias)
	rc, _, _ = openKey.Call(storage, uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(name)), 0, silentFlag)
	if rc == 0 {
		return &deviceKey{key}, nil
	}
	if !create {
		return nil, syscall.Errno(rc)
	}
	// No overwrite flag, machine-key flag or export permission.
	rc, _, _ = createKey.Call(storage, uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(wide("ECDSA_P256"))), uintptr(unsafe.Pointer(name)), 0, 0)
	if rc != 0 {
		return nil, syscall.Errno(rc)
	}
	rc, _, _ = finalizeKey.Call(key, silentFlag)
	if rc != 0 {
		freeObject.Call(key)
		return nil, syscall.Errno(rc)
	}
	return &deviceKey{key}, nil
}

func (k *deviceKey) Close() { freeObject.Call(k.handle) }

func (k *deviceKey) public() ([]byte, error) {
	var blob [72]byte // BCRYPT_ECCKEY_BLOB, followed by 32-byte X and Y
	var size uint32
	rc, _, _ := exportKey.Call(k.handle, 0, uintptr(unsafe.Pointer(wide("ECCPUBLICBLOB"))), 0,
		uintptr(unsafe.Pointer(&blob[0])), uintptr(len(blob)), uintptr(unsafe.Pointer(&size)), silentFlag)
	if rc != 0 {
		return nil, syscall.Errno(rc)
	}
	if size != 72 || string(blob[:4]) != "ECS1" || binary.LittleEndian.Uint32(blob[4:8]) != 32 {
		return nil, errors.New("unexpected P-256 public key")
	}
	return append([]byte{2 | (blob[71] & 1)}, blob[8:40]...), nil
}

func (k *deviceKey) Sign(message []byte) ([]byte, error) {
	hash := sha256.Sum256(message)
	var signature [64]byte
	var size uint32
	rc, _, _ := signHash.Call(k.handle, 0, uintptr(unsafe.Pointer(&hash[0])), uintptr(len(hash)),
		uintptr(unsafe.Pointer(&signature[0])), uintptr(len(signature)), uintptr(unsafe.Pointer(&size)), silentFlag)
	if rc != 0 {
		return nil, syscall.Errno(rc)
	}
	if size != 64 {
		return nil, errors.New("invalid P-256 signature length")
	}
	return asn1.Marshal(struct{ R, S *big.Int }{new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:])})
}

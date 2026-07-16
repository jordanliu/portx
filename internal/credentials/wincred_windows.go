//go:build windows

package credentials

import (
	"syscall"
	"unsafe"

	"portx/internal/apperr"
)

// Windows Credential Manager via CredWrite/CredRead/CredDelete.
// Secret is not placed on process argv.

type wincredStore struct{}

func openWincred() (Store, error) {
	return &wincredStore{}, nil
}

func (w *wincredStore) Backend() string { return "wincred" }

func (w *wincredStore) target(key string) string {
	return serviceName + ":" + key
}

func (w *wincredStore) Set(key, value string) error {
	_ = w.Delete(key)
	target, err := syscall.UTF16PtrFromString(w.target(key))
	if err != nil {
		return err
	}
	user, err := syscall.UTF16PtrFromString(key)
	if err != nil {
		return err
	}
	blob := []byte(value)
	var pBlob *byte
	if len(blob) > 0 {
		pBlob = &blob[0]
	}
	cred := credWrite{
		Flags:      0,
		Type:       credTypeGeneric,
		TargetName: target,
		CredentialBlobSize: uint32(len(blob)),
		CredentialBlob:     pBlob,
		Persist:            credPersistLocalMachine, // user logon sessions on this machine
		UserName:           user,
	}
	r, _, callErr := procCredWriteW.Call(uintptr(unsafe.Pointer(&cred)), 0)
	if r == 0 {
		return apperr.Wrap(apperr.ExitAuth, "CredWrite failed", callErr)
	}
	return nil
}

func (w *wincredStore) Get(key string) (string, error) {
	target, err := syscall.UTF16PtrFromString(w.target(key))
	if err != nil {
		return "", err
	}
	var pcred *credRead
	r, _, err := procCredReadW.Call(
		uintptr(unsafe.Pointer(target)),
		uintptr(credTypeGeneric),
		0,
		uintptr(unsafe.Pointer(&pcred)),
	)
	if r == 0 {
		return "", apperr.New(apperr.ExitAuth, "credential not found")
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(pcred)))
	if pcred.CredentialBlobSize == 0 || pcred.CredentialBlob == nil {
		return "", nil
	}
	data := unsafe.Slice(pcred.CredentialBlob, pcred.CredentialBlobSize)
	// copy out
	out := make([]byte, len(data))
	copy(out, data)
	return string(out), nil
}

func (w *wincredStore) Delete(key string) error {
	target, err := syscall.UTF16PtrFromString(w.target(key))
	if err != nil {
		return err
	}
	r, _, _ := procCredDeleteW.Call(uintptr(unsafe.Pointer(target)), uintptr(credTypeGeneric), 0)
	if r == 0 {
		return nil // already gone is fine
	}
	return nil
}

var (
	modadvapi32      = syscall.NewLazyDLL("advapi32.dll")
	procCredWriteW   = modadvapi32.NewProc("CredWriteW")
	procCredReadW    = modadvapi32.NewProc("CredReadW")
	procCredDeleteW  = modadvapi32.NewProc("CredDeleteW")
	procCredFree     = modadvapi32.NewProc("CredFree")
)

const (
	credTypeGeneric         = 1
	credPersistLocalMachine = 2
)

type credWrite struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

type credRead struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

type filetime struct {
	LowDateTime  uint32
	HighDateTime uint32
}


package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// secretScheme names how sealSecret protects data, recorded next to it.
const secretScheme = "dpapi"

// sealSecret encrypts data with Windows DPAPI: only this Windows user on this computer can decrypt it.
func sealSecret(data []byte) ([]byte, error) {
	return dpapi(data, true)
}

func openSecret(data []byte) ([]byte, error) {
	return dpapi(data, false)
}

func dpapi(data []byte, seal bool) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	var err error
	if seal {
		err = windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	} else {
		err = windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	}
	if err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}

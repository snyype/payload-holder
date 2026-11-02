//go:build !windows

package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
)

// secretScheme names how sealSecret protects data, recorded next to it.
const secretScheme = "aes-gcm"

// sealSecret encrypts data with AES-256-GCM. The key lives in the user's config folder
// (~/.config/payload/secret.key, mode 0600), apart from the payload folder, so copying or syncing
// the payload folder does not carry a usable token.
func sealSecret(data []byte) ([]byte, error) {
	gcm, err := secretCipher(true)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, data, nil), nil
}

func openSecret(data []byte) ([]byte, error) {
	gcm, err := secretCipher(false)
	if err != nil {
		return nil, err
	}
	if len(data) < gcm.NonceSize() {
		return nil, errors.New("encrypted data is too short")
	}
	return gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
}

func secretKeyPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(dir, "payload", "secret.key")
}

func secretCipher(create bool) (cipher.AEAD, error) {
	path := secretKeyPath()
	key, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && create {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		err = os.WriteFile(path, key, 0o600)
	}
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errors.New(path + " is not a 32-byte key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

package connection

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// MasterKey is kept outside MongoDB and every student VM. Losing it makes stored secrets unreadable.
func MasterKey() ([]byte, error) {
	if raw := os.Getenv("AGENTLAB_CREDENTIAL_KEY"); raw != "" {
		key, err := base64.StdEncoding.DecodeString(raw)
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("AGENTLAB_CREDENTIAL_KEY must be base64 of 32 random bytes")
		}
		return key, nil
	}
	path := os.Getenv("AGENTLAB_CREDENTIAL_KEY_FILE")
	if path == "" {
		root, err := os.UserConfigDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(root, "AgentLab", "credentials", "master.key")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 20; attempt++ {
		raw, err := os.ReadFile(path)
		if err == nil {
			if len(raw) == 0 {
				time.Sleep(25 * time.Millisecond)
				continue
			}
			if err = checkKeyPermissions(path); err != nil {
				return nil, err
			}
			key, err := unprotectKey(raw)
			if err != nil || len(key) != 32 {
				return nil, fmt.Errorf("cannot unlock credential master key; restore the original host key")
			}
			return key, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		key := make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		raw, err = protectKey(key)
		if err != nil {
			return nil, err
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		_, writeErr := file.Write(raw)
		syncErr := file.Sync()
		closeErr := file.Close()
		if writeErr != nil || syncErr != nil || closeErr != nil {
			return nil, fmt.Errorf("cannot persist credential master key")
		}
		return key, nil
	}
	return nil, fmt.Errorf("credential master key is being initialized; retry startup")
}
func seal(key []byte, secret, context string) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, []byte(secret), []byte(context)), nil
}
func unseal(key, raw []byte, context string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < aead.NonceSize() {
		return "", fmt.Errorf("invalid credential")
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], []byte(context))
	return string(plain), err
}

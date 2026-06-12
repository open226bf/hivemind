package persistence

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
)

// Cipher handles transparent encryption/decryption of sensitive string values.
type Cipher interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}

// NopCipher stores values as plain text — development only.
type NopCipher struct{}

func (NopCipher) Encrypt(p string) (string, error) { return p, nil }
func (NopCipher) Decrypt(c string) (string, error) { return c, nil }

// AESCipher encrypts using AES-256-GCM with a random nonce prepended.
type AESCipher struct{ gcm cipher.AEAD }

func NewAESCipher(key []byte) (*AESCipher, error) {
	if len(key) != 32 {
		return nil, errors.New("AES key must be exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &AESCipher{gcm: gcm}, nil
}

func (c *AESCipher) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, c.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := c.gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func (c *AESCipher) Decrypt(encoded string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	ns := c.gcm.NonceSize()
	if len(data) < ns {
		return "", errors.New("ciphertext too short")
	}
	plain, err := c.gcm.Open(nil, data[:ns], data[ns:], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

package persistence_test

import (
	"testing"

	"github.com/orange/hivemind/internal/adapters/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNopCipher(t *testing.T) {
	c := persistence.NopCipher{}
	enc, err := c.Encrypt("hello")
	require.NoError(t, err)
	assert.Equal(t, "hello", enc)

	dec, err := c.Decrypt("hello")
	require.NoError(t, err)
	assert.Equal(t, "hello", dec)
}

func TestAESCipher_RoundTrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	c, err := persistence.NewAESCipher(key)
	require.NoError(t, err)

	plaintext := "super-secret-password"
	enc, err := c.Encrypt(plaintext)
	require.NoError(t, err)
	assert.NotEqual(t, plaintext, enc)

	dec, err := c.Decrypt(enc)
	require.NoError(t, err)
	assert.Equal(t, plaintext, dec)
}

func TestAESCipher_DifferentCiphertextEachTime(t *testing.T) {
	key := make([]byte, 32)
	c, _ := persistence.NewAESCipher(key)

	enc1, _ := c.Encrypt("value")
	enc2, _ := c.Encrypt("value")
	assert.NotEqual(t, enc1, enc2, "nonces should differ")
}

func TestAESCipher_WrongKeySize(t *testing.T) {
	_, err := persistence.NewAESCipher([]byte("tooshort"))
	assert.Error(t, err)
}

func TestAESCipher_TamperedCiphertext(t *testing.T) {
	key := make([]byte, 32)
	c, _ := persistence.NewAESCipher(key)
	enc, _ := c.Encrypt("value")
	_, err := c.Decrypt(enc + "tampered")
	assert.Error(t, err)
}

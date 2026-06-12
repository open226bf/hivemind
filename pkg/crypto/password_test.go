package crypto_test

import (
	"testing"

	"github.com/open226bf/hivemind/pkg/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashAndCheck(t *testing.T) {
	hash, err := crypto.HashPassword("supersecret")
	require.NoError(t, err)
	assert.NotEqual(t, "supersecret", hash)

	err = crypto.CheckPassword(hash, "supersecret")
	assert.NoError(t, err)
}

func TestCheckPassword_WrongPassword(t *testing.T) {
	hash, _ := crypto.HashPassword("correct")
	err := crypto.CheckPassword(hash, "wrong")
	assert.ErrorIs(t, err, crypto.ErrPasswordMismatch)
}

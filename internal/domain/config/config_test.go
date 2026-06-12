package config_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/open226bf/hivemind/internal/domain/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_Valid(t *testing.T) {
	content := []byte("server { listen 80; }")
	c, v, err := config.New("nginx.conf", "/etc/nginx/nginx.conf", content, "initial", uuid.New())
	require.NoError(t, err)
	assert.Equal(t, 1, c.CurrentVersion)
	assert.Equal(t, 1, v.Version)
	assert.Equal(t, content, v.Content)
}

func TestNew_TooLarge(t *testing.T) {
	large := []byte(strings.Repeat("a", 500*1024+1))
	_, _, err := config.New("big.conf", "/etc/big.conf", large, "", uuid.New())
	assert.ErrorIs(t, err, config.ErrContentTooLarge)
}

func TestNew_InvalidUTF8(t *testing.T) {
	_, _, err := config.New("bad.conf", "/etc/bad.conf", []byte{0xff, 0xfe}, "", uuid.New())
	assert.ErrorIs(t, err, config.ErrInvalidUTF8)
}

func TestNewVersion_IncrementsVersion(t *testing.T) {
	c, _, _ := config.New("app.yml", "/app/app.yml", []byte("key: val"), "v1", uuid.New())
	v2, err := c.NewVersion([]byte("key: val2"), "v2", uuid.New())
	require.NoError(t, err)
	assert.Equal(t, 2, c.CurrentVersion)
	assert.Equal(t, 2, v2.Version)
}

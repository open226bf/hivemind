package hive_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/orange/hivemind/internal/domain/hive"
)

func TestNew_Valid(t *testing.T) {
	h, err := hive.New(uuid.Nil, "  Paiement  ", "Services de paiement", "#1e88e5")
	require.NoError(t, err)
	assert.Equal(t, "Paiement", h.Name) // trimmed
	assert.Equal(t, "#1e88e5", h.Color)
}

func TestNew_EmptyName(t *testing.T) {
	_, err := hive.New(uuid.Nil, "   ", "", "")
	assert.ErrorIs(t, err, hive.ErrInvalidName)
}

func TestNew_NameTooLong(t *testing.T) {
	_, err := hive.New(uuid.Nil, strings.Repeat("a", 65), "", "")
	assert.ErrorIs(t, err, hive.ErrInvalidName)
}

func TestNew_InvalidColor(t *testing.T) {
	for _, c := range []string{"blue", "#xyzxyz", "#1e88e", "1e88e5"} {
		_, err := hive.New(uuid.Nil, "p", "", c)
		assert.ErrorIs(t, err, hive.ErrInvalidColor, "color=%q", c)
	}
}

func TestNew_EmptyColorAllowed(t *testing.T) {
	_, err := hive.New(uuid.Nil, "p", "", "")
	require.NoError(t, err)
}

func TestUpdate(t *testing.T) {
	h, _ := hive.New(uuid.Nil, "p", "", "")
	require.NoError(t, h.Update("Nouveau nom", "desc", "#abcdef"))
	assert.Equal(t, "Nouveau nom", h.Name)
	assert.Equal(t, "#abcdef", h.Color)

	assert.ErrorIs(t, h.Update("", "", ""), hive.ErrInvalidName)
}

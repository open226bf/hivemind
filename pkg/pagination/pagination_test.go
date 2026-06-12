package pagination_test

import (
	"testing"

	"github.com/orange/hivemind/pkg/pagination"
	"github.com/stretchr/testify/assert"
)

func TestNew_Defaults(t *testing.T) {
	p := pagination.New(0, 0)
	assert.Equal(t, 1, p.Number)
	assert.Equal(t, pagination.DefaultLimit, p.Size)
}

func TestNew_CapsAtMaxLimit(t *testing.T) {
	p := pagination.New(1, 9999)
	assert.Equal(t, pagination.MaxLimit, p.Size)
}

func TestOffset(t *testing.T) {
	p := pagination.New(3, 10)
	assert.Equal(t, 20, p.Offset())
}

package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/open226bf/hivemind/internal/adapters/agentca"
)

const agentCAID = "ca"

// AgentCARepository persists the agent CA, encrypting its private key at rest.
type AgentCARepository struct {
	db     *gorm.DB
	cipher Cipher
}

func NewAgentCARepository(db *gorm.DB, cipher Cipher) *AgentCARepository {
	return &AgentCARepository{db: db, cipher: cipher}
}

// LoadOrCreate returns the persisted CA, generating and storing one on first use.
func (r *AgentCARepository) LoadOrCreate(ctx context.Context) (*agentca.CA, error) {
	var m agentCAModel
	err := r.db.WithContext(ctx).Where("id = ?", agentCAID).First(&m).Error
	switch {
	case err == nil:
		keyPEM, derr := r.cipher.Decrypt(m.EncryptedKeyPEM)
		if derr != nil {
			return nil, fmt.Errorf("decrypt ca key: %w", derr)
		}
		return agentca.Load([]byte(m.CertPEM), []byte(keyPEM))
	case errors.Is(err, gorm.ErrRecordNotFound):
		ca, gerr := agentca.Generate()
		if gerr != nil {
			return nil, fmt.Errorf("generate ca: %w", gerr)
		}
		encKey, eerr := r.cipher.Encrypt(string(ca.KeyPEM()))
		if eerr != nil {
			return nil, fmt.Errorf("encrypt ca key: %w", eerr)
		}
		row := &agentCAModel{ID: agentCAID, CertPEM: string(ca.CertPEM()), EncryptedKeyPEM: encKey, CreatedAt: time.Now().UTC()}
		if cerr := r.db.WithContext(ctx).Create(row).Error; cerr != nil {
			return nil, fmt.Errorf("save ca: %w", cerr)
		}
		return ca, nil
	default:
		return nil, fmt.Errorf("load ca: %w", err)
	}
}

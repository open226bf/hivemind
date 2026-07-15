package persistence

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Migrate runs GORM AutoMigrate for all tables.
// Replace with a proper migration tool (goose, golang-migrate) before production.
func Migrate(db *gorm.DB) error {
	if err := db.AutoMigrate(
		&clusterModel{},
		&agentCAModel{},
		&userModel{},
		&hiveModel{},
		&hiveEnvVarModel{},
		&serviceModel{},
		&envVarModel{},
		&networkModel{},
		&serviceNetworkModel{},
		&volumeModel{},
		&serviceMountModel{},
		&servicePortModel{},
		&templateModel{},
		&secretModel{},
		&secretVersionModel{},
		&serviceSecretModel{},
		&configModel{},
		&configVersionModel{},
		&serviceConfigModel{},
		&deploymentModel{},
		&serviceSnapshotModel{},
		&auditLogModel{},
		&aclGrantModel{},
	); err != nil {
		return fmt.Errorf("auto migrate: %w", err)
	}

	// Multi-cluster made resource names unique per cluster, not globally. On a
	// database created before that change, the old single-column unique index on
	// `name` survives AutoMigrate and would still enforce global uniqueness, so
	// drop it explicitly. Idempotent: only dropped when present.
	legacyNameIndexes := map[any]string{
		&serviceModel{}: "idx_services_name",
		&networkModel{}: "idx_networks_name",
		&secretModel{}:  "idx_secrets_name",
		&configModel{}:  "idx_configs_name",
		&volumeModel{}:  "idx_volumes_name",
		&hiveModel{}:    "idx_hives_name",
	}
	for model, idx := range legacyNameIndexes {
		if db.Migrator().HasIndex(model, idx) {
			if err := db.Migrator().DropIndex(model, idx); err != nil {
				return fmt.Errorf("drop legacy index %s: %w", idx, err)
			}
		}
	}
	return nil
}

// SeedDefaultGrants maps each non-admin user's global role to an ACL grant on
// the default cluster (operator→write, viewer→read), so flipping
// HIVEMIND_ACL_ENFORCED to true preserves everyone's current access (ADR 0003).
// Idempotent: a user who already has a grant on the default cluster is skipped.
// Admins are never seeded — they bypass grants entirely.
func SeedDefaultGrants(db *gorm.DB, defaultClusterID string) error {
	var users []userModel
	if err := db.Where("role <> ?", "admin").Find(&users).Error; err != nil {
		return fmt.Errorf("seed grants: list users: %w", err)
	}
	now := time.Now().UTC()
	for _, u := range users {
		verb := "read"
		if u.Role == "operator" {
			verb = "write"
		}
		var count int64
		if err := db.Model(&aclGrantModel{}).
			Where("subject_id = ? AND resource_type = ? AND resource_id = ?", u.ID, "cluster", defaultClusterID).
			Count(&count).Error; err != nil {
			return fmt.Errorf("seed grants: check existing: %w", err)
		}
		if count > 0 {
			continue
		}
		g := aclGrantModel{
			ID:           uuid.NewString(),
			SubjectID:    u.ID,
			ResourceType: "cluster",
			ResourceID:   defaultClusterID,
			Verb:         verb,
			CreatedAt:    now,
		}
		if err := db.Create(&g).Error; err != nil {
			return fmt.Errorf("seed grants: create for %s: %w", u.ID, err)
		}
	}
	return nil
}

// BackfillClusterID assigns the default cluster to every resource that predates
// multi-cluster (cluster_id IS NULL), so the (cluster_id, name) uniqueness holds
// and the rows resolve explicitly instead of relying on the NULL→default
// fallback. Idempotent: rows already scoped to a cluster are untouched.
func BackfillClusterID(db *gorm.DB, defaultClusterID string) error {
	tables := []string{"services", "networks", "secrets", "configs", "volumes", "hives"}
	for _, t := range tables {
		if err := db.Exec(
			"UPDATE "+t+" SET cluster_id = ? WHERE cluster_id IS NULL", defaultClusterID,
		).Error; err != nil {
			return fmt.Errorf("backfill cluster_id on %s: %w", t, err)
		}
	}
	return nil
}

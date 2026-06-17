package persistence

import (
	"fmt"

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

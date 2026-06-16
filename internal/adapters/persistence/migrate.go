package persistence

import (
	"fmt"

	"gorm.io/gorm"
)

// Migrate runs GORM AutoMigrate for all tables.
// Replace with a proper migration tool (goose, golang-migrate) before production.
func Migrate(db *gorm.DB) error {
	if err := db.AutoMigrate(
		&userModel{},
		&hiveModel{},
		&serviceModel{},
		&envVarModel{},
		&networkModel{},
		&serviceNetworkModel{},
		&volumeModel{},
		&serviceMountModel{},
		&templateModel{},
		&secretModel{},
		&secretVersionModel{},
		&serviceSecretModel{},
		&configModel{},
		&configVersionModel{},
		&serviceConfigModel{},
		&deploymentModel{},
		&auditLogModel{},
	); err != nil {
		return fmt.Errorf("auto migrate: %w", err)
	}
	return nil
}

package main

import (
	"log"
	"os"

	"github.com/joho/godotenv"
	"github.com/orange/hivemind/internal/adapters/persistence"
)

func main() {
	_ = godotenv.Load()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is required")
	}

	db, err := persistence.NewDB(persistence.DBConfig{DSN: dsn})
	if err != nil {
		log.Fatalf("connect db: %v", err)
	}

	if err := persistence.Migrate(db); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	// Seed ACL grants for existing non-admins so enabling HIVEMIND_ACL_ENFORCED
	// preserves their access (ADR 0003). Only runs once a default cluster exists;
	// idempotent otherwise.
	var defaultClusterID string
	db.Table("clusters").Where("is_default = ?", true).Limit(1).Pluck("id", &defaultClusterID)
	if defaultClusterID != "" {
		if err := persistence.SeedDefaultGrants(db, defaultClusterID); err != nil {
			log.Fatalf("seed default grants: %v", err)
		}
		log.Println("seeded default ACL grants")
	}

	log.Println("migrations completed successfully")
}

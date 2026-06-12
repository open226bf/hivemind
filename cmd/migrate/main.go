package main

import (
	"log"
	"os"

	"github.com/joho/godotenv"
	"github.com/open226bf/hivemind/internal/adapters/persistence"
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

	log.Println("migrations completed successfully")
}

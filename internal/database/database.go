// Package database initializes and returns a GORM DB connection.
// The driver is selected at runtime via DB_DRIVER env var (postgres or sqlite).
// SQLite requires CGO; in CGO_ENABLED=0 builds only Postgres is available.
package database

import (
	"fmt"
	"os"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Connect opens a database connection based on the DB_DRIVER env var.
// Defaults to "sqlite" when unset (local dev with CGO).
func Connect() (*gorm.DB, error) {
	driver := os.Getenv("DB_DRIVER")
	if driver == "" {
		driver = "sqlite"
	}

	var db *gorm.DB
	var err error

	switch driver {
	case "postgres":
		db, err = openPostgres()
	case "sqlite":
		db, err = openSQLite()
	default:
		return nil, fmt.Errorf("unsupported DB_DRIVER: %s", driver)
	}

	if err != nil {
		return nil, err
	}

	return autoMigrate(db)
}

// Init opens (or creates) the SQLite database at the given path and auto-migrates.
// Kept for backward compatibility and tests.
func Init(dbPath string) (*gorm.DB, error) {
	db, err := openSQLiteAt(dbPath)
	if err != nil {
		return nil, err
	}
	return autoMigrate(db)
}

// GormConfig returns a shared GORM config.
func GormConfig(level logger.LogLevel) *gorm.Config {
	return &gorm.Config{
		Logger: logger.Default.LogMode(level),
	}
}

func autoMigrate(db *gorm.DB) (*gorm.DB, error) {
	if err := db.AutoMigrate(
		&models.Trip{},
		&models.Day{},
		&models.Hotel{},
		&models.List{},
		&models.ListCheck{},
		&models.ListCustomItem{},
		&models.ListHidden{},
		&models.MagicToken{},
		&models.Group{},
		&models.GroupMember{},
		&models.TripAccess{},
	); err != nil {
		return nil, err
	}
	return db, nil
}

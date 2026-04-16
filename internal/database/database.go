// Package database initializes and returns a GORM DB connection.
package database

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Init opens (or creates) the SQLite database and auto-migrates all models.
func Init(dbPath string) (*gorm.DB, error) {
	if dir := filepath.Dir(dbPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	db, err := gorm.Open(sqlite.Open(dbPath+"?_foreign_keys=on&_journal_mode=WAL"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, err
	}

	return autoMigrate(db)
}

// InitMemory opens an in-memory SQLite database (for tests).
// Each call creates a unique database to avoid cross-test pollution.
func InitMemory() (*gorm.DB, error) {
	// Use a unique name per call to isolate test databases
	name := fmt.Sprintf("file:memdb_%d?mode=memory&cache=shared&_foreign_keys=on", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(name), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, err
	}

	return autoMigrate(db)
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
	); err != nil {
		return nil, err
	}
	return db, nil
}

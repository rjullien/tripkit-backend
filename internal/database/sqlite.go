//go:build cgo

package database

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func openSQLite() (*gorm.DB, error) {
	path := os.Getenv("DB_PATH")
	if path == "" {
		path = "./data/tripkit.db"
	}
	return openSQLiteAt(path)
}

func openSQLiteAt(path string) (*gorm.DB, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	return gorm.Open(sqlite.Open(path+"?_foreign_keys=on&_journal_mode=WAL"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
}

// InitMemory opens an in-memory SQLite database (for tests).
// Each call creates a unique database to avoid cross-test pollution.
func InitMemory() (*gorm.DB, error) {
	name := fmt.Sprintf("file:memdb_%d?mode=memory&cache=shared&_foreign_keys=on", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(name), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, err
	}

	return autoMigrate(db)
}

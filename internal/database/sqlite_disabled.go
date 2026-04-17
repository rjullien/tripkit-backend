//go:build !cgo

package database

import (
	"fmt"

	"gorm.io/gorm"
)

func openSQLite() (*gorm.DB, error) {
	return nil, fmt.Errorf("sqlite not available (built without CGO)")
}

func openSQLiteAt(_ string) (*gorm.DB, error) {
	return nil, fmt.Errorf("sqlite not available (built without CGO)")
}

// InitMemory is not available without CGO.
func InitMemory() (*gorm.DB, error) {
	return nil, fmt.Errorf("in-memory sqlite not available (built without CGO)")
}

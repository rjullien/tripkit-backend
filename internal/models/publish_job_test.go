package models

import (
	"testing"

	"gorm.io/gorm"
)

func TestPublishJob_BeforeSave_EmptyJSON(t *testing.T) {
	j := &PublishJob{}
	if err := j.BeforeSave(&gorm.DB{}); err != nil {
		t.Fatal(err)
	}
	if j.ErrorsJSON != "[]" || j.WarningsJSON != "[]" || j.SummaryJSON != "null" {
		t.Fatalf("got errors=%q warnings=%q summary=%q", j.ErrorsJSON, j.WarningsJSON, j.SummaryJSON)
	}
}

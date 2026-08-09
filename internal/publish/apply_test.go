package publish_test

import (
	"encoding/json"
	"testing"

	"github.com/rjullien/tripkit-backend/internal/database"
	"github.com/rjullien/tripkit-backend/internal/models"
	"github.com/rjullien/tripkit-backend/internal/publish"
)

func TestApplyCanonical_CreateUpdateRollbackACL(t *testing.T) {
	db, err := database.InitMemory()
	if err != nil {
		t.Fatal(err)
	}

	payload := publish.CanonicalPayload{
		TripID: "quebec-2026",
		Name:   "Boucle Québec 2026",
		TripData: map[string]any{
			"travelers": []any{},
			"people":    map[string]any{},
			"homeTz":    "Europe/Paris",
		},
		Days: []map[string]any{
			{"day": 1, "title": "Arrivée"},
			{"day": 2, "title": "Ville"},
		},
		Lists: map[string]publish.ListUpsert{
			"checklist-quebec": {
				ID:    "checklist-quebec",
				Type:  "packing",
				Title: "Valises",
				Data:  map[string]any{"type": "packing", "title": "Valises", "sections": []any{}},
			},
		},
		ACLMembers: []string{"rene", "nicole"},
	}

	res, err := publish.ApplyCanonical(db, payload, []string{"rene"})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !res.Created {
		t.Fatal("expected created")
	}
	if res.GroupID != "trip-quebec-2026" {
		t.Fatalf("group %s", res.GroupID)
	}

	var trip models.Trip
	if err := db.First(&trip, "id = ?", "quebec-2026").Error; err != nil {
		t.Fatal(err)
	}
	var days int64
	db.Model(&models.Day{}).Where("trip_id = ?", "quebec-2026").Count(&days)
	if days != 2 {
		t.Fatalf("days=%d", days)
	}
	var members []models.GroupMember
	db.Where("group_id = ?", "trip-quebec-2026").Find(&members)
	if len(members) != 2 {
		t.Fatalf("members=%d %#v", len(members), members)
	}

	// Preserve checks on reseed
	db.Create(&models.ListCheck{ListID: "checklist-quebec", ItemID: "item-1", Checked: true, UpdatedAt: 1})

	payload.Days = []map[string]any{{"day": 1, "title": "Arrivée only"}}
	payload.Name = "Québec updated"
	res2, err := publish.ApplyCanonical(db, payload, []string{"rene"})
	if err != nil {
		t.Fatalf("reseed: %v", err)
	}
	if res2.Created {
		t.Fatal("expected update")
	}
	db.Model(&models.Day{}).Where("trip_id = ?", "quebec-2026").Count(&days)
	if days != 1 {
		t.Fatalf("stale day not removed, days=%d", days)
	}
	var check models.ListCheck
	if err := db.First(&check, "list_id = ? AND item_id = ?", "checklist-quebec", "item-1").Error; err != nil {
		t.Fatal("check should be preserved")
	}
	if trip.Name == "Québec updated" {
		// name updated in DB — reload
	}
	if err := db.First(&trip, "id = ?", "quebec-2026").Error; err != nil {
		t.Fatal(err)
	}
	if trip.Name != "Québec updated" {
		t.Fatalf("name=%q", trip.Name)
	}
}

func TestApplyCanonical_TransactionRollback(t *testing.T) {
	db, err := database.InitMemory()
	if err != nil {
		t.Fatal(err)
	}
	payload := publish.CanonicalPayload{
		TripID:   "t1",
		Name:     "T1",
		TripData: map[string]any{},
		Days:     []map[string]any{{"day": 1}},
		Lists: map[string]publish.ListUpsert{
			"shared-list": {ID: "shared-list", Type: "x", Title: "x", Data: map[string]any{}},
		},
	}
	// Pre-create colliding list on another trip
	if err := db.Create(&models.Trip{ID: "other", Name: "O"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.List{ID: "shared-list", TripID: "other", Type: "x", Title: "x", Data: "{}"}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := publish.ApplyCanonical(db, payload, nil); err == nil {
		t.Fatal("expected error")
	}
	var count int64
	db.Model(&models.Trip{}).Where("id = ?", "t1").Count(&count)
	if count != 0 {
		t.Fatalf("trip should not exist after rollback, count=%d", count)
	}
}

func TestParseSeedFile_QuebecShape(t *testing.T) {
	code := `var SEED_TEST = {
  "trip": { "id": "test-2026", "name": "Test", "emoji": "✈️", "travelers": [{"personId":"rene"}] },
  "days": [ { "day": 1, "title": "A" } ],
  "hotels": {},
  "lists": { "l1": { "type": "packing", "title": "Pack" } }
};`
	seed, err := publish.ParseSeedFile(code)
	if err != nil {
		t.Fatal(err)
	}
	peopleCode := `var PEOPLE = {
  rene: { id: "rene", name: "René", login: "rene", emoji: "👨" }
};`
	people, err := publish.ParsePeopleFile(peopleCode)
	if err != nil {
		t.Fatal(err)
	}
	cfgCode := `var CHECKLIST_CONFIG = { family: "jullien" };`
	cfg, err := publish.ParseChecklistConfig(cfgCode)
	if err != nil {
		t.Fatal(err)
	}
	if errs := publish.StructuralValidate(seed, "test-2026", "jullien", cfg.Family); len(errs) != 0 {
		t.Fatalf("validate: %v", errs)
	}
	p, err := publish.BuildCanonical(seed, people, cfg.Family, "jullien", "abc", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.ACLMembers) != 1 || p.ACLMembers[0] != "rene" {
		t.Fatalf("acl %#v", p.ACLMembers)
	}
	// login must not leak into trip people
	raw, _ := json.Marshal(p.TripData["people"])
	if string(raw) != "" && jsonContainsLogin(raw) {
		t.Fatalf("login leaked: %s", raw)
	}
}

func jsonContainsLogin(raw []byte) bool {
	var m map[string]map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return false
	}
	for _, p := range m {
		if _, ok := p["login"]; ok {
			return true
		}
	}
	return false
}

func TestRegistry_CanPublish(t *testing.T) {
	reg := publish.NewRegistry([]publish.Source{{
		ID: "jullien", Enabled: true,
		PublisherLogins: []string{"rene"},
		Seeds:           []publish.SeedRef{{TripID: "quebec-2026", Path: "quebec-2026.js"}},
	}})
	if !reg.CanPublish("jullien", "rene", false) {
		t.Fatal("rene should publish")
	}
	if reg.CanPublish("jullien", "nadia", false) {
		t.Fatal("nadia must not")
	}
	reg2 := publish.NewRegistry([]publish.Source{{
		ID: "jullien", Enabled: false, PublisherLogins: []string{"rene"},
	}})
	if reg2.CanPublish("jullien", "rene", true) {
		t.Fatal("disabled blocks even admin")
	}
}

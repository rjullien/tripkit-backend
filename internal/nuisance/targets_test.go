package nuisance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/rjullien/tripkit-backend/internal/geocode"
	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// stubGeocoder resolves one known address and fails on anything else.
type stubGeocoder struct {
	points map[string]geocode.Point
	err    error
	calls  int
	seen   []string
}

func (g *stubGeocoder) Geocode(_ context.Context, address string) (geocode.Point, error) {
	g.calls++
	g.seen = append(g.seen, address)
	if g.err != nil {
		return geocode.Point{}, g.err
	}
	if p, ok := g.points[address]; ok {
		return p, nil
	}
	return geocode.Point{}, geocode.ErrNotFound
}

// tripWith builds an in-memory trip from a seed-shaped payload.
func tripWith(t *testing.T, data map[string]any) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.Trip{}, &models.ConstructionCheck{}, &models.DiscoveryCache{}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(data)
	s := string(raw)
	if err := db.Create(&models.Trip{ID: "test-trip", Name: "T", Data: &s}).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

// matabiauSeed: two hotels in the same city, one booked with an address, one
// still a candidate. The stop point is Place du Capitole, ~1 km from the
// station — far enough that the two verdicts must differ.
func matabiauSeed() map[string]any {
	return map[string]any{
		"locations": map[string]any{
			"toulouse": map[string]any{"name": "Toulouse", "lat": 43.6045, "lon": 1.4440},
		},
		"days": []any{
			map[string]any{"day": 1, "locationId": "toulouse", "hotelId": "hotel-matabiau"},
			map[string]any{"day": 2, "locationId": "toulouse", "hotelId": "hotel-candidat"},
		},
		"hotels": map[string]any{
			"hotel-matabiau": map[string]any{
				"name":          "Hôtel Matabiau",
				"addr":          "64 Boulevard Pierre Sémard, 31000 Toulouse",
				"bookingStatus": "booked",
			},
			"hotel-candidat": map[string]any{
				"name":   "Hôtel Candidat",
				"addr":   "1 Place du Capitole, 31000 Toulouse",
				"status": "candidate",
			},
		},
	}
}

func findTarget(tgs []target, id string) (target, bool) {
	for _, tg := range tgs {
		if tg.id == id {
			return tg, true
		}
	}
	return target{}, false
}

// The rule: a booked hotel is analysed at ITS OWN address, not at the centre of
// its city. Analysing the stop gave every hotel of a city the same verdict.
func TestResolveTargets_BookedHotelUsesItsOwnAddress(t *testing.T) {
	db := tripWith(t, matabiauSeed())
	g := &stubGeocoder{points: map[string]geocode.Point{
		"64 Boulevard Pierre Sémard, 31000 Toulouse": {Lat: 43.6112, Lon: 1.4536, DisplayName: "64 Bd Pierre Semard, Toulouse"},
	}}
	svc := &Service{DB: db, Geocoder: g}

	tgs, err := svc.resolveTargets(context.Background(), "test-trip", nil, true)
	if err != nil {
		t.Fatal(err)
	}

	booked, ok := findTarget(tgs, "hotel-matabiau")
	if !ok {
		t.Fatalf("booked hotel missing from targets: %+v", tgs)
	}
	if booked.source != SourceHotel {
		t.Errorf("source=%q, want %q", booked.source, SourceHotel)
	}
	if booked.lat != 43.6112 || booked.lon != 1.4536 {
		t.Errorf("point=%f,%f, want the geocoded hotel address", booked.lat, booked.lon)
	}
	if booked.locationID != "toulouse" {
		t.Errorf("locationID=%q, want the stop it belongs to", booked.locationID)
	}
	if booked.addr == "" {
		t.Error("want the analysed address reported")
	}
	if booked.note != "" {
		t.Errorf("note=%q, want none when the hotel address was used", booked.note)
	}

	// A hotel that is not booked stays on the stop, and says so.
	cand, ok := findTarget(tgs, "hotel-candidat")
	if !ok {
		t.Fatal("candidate hotel missing from targets")
	}
	if cand.source != SourceStep {
		t.Errorf("source=%q, want %q for a non-booked hotel", cand.source, SourceStep)
	}
	if cand.lat != 43.6045 || cand.lon != 1.4440 {
		t.Errorf("point=%f,%f, want the stop", cand.lat, cand.lon)
	}
	if cand.note == "" {
		t.Error("a non-booked hotel analysed at the stop must say so")
	}
	if g.calls != 1 {
		t.Errorf("geocoder calls=%d, want 1 (only the booked hotel)", g.calls)
	}

	// Two hotels in one city = two distinct targets, no overwrite.
	if len(tgs) != 2 {
		t.Errorf("targets=%d, want 2 (one per hotel, the stop is claimed)", len(tgs))
	}
}

// Explicit coordinates in the seed win: no network call at all.
func TestResolveTargets_HotelLatLonSkipsGeocoding(t *testing.T) {
	seed := matabiauSeed()
	h := seed["hotels"].(map[string]any)["hotel-matabiau"].(map[string]any)
	h["lat"], h["lon"] = 43.6112, 1.4536

	db := tripWith(t, seed)
	g := &stubGeocoder{}
	svc := &Service{DB: db, Geocoder: g}

	tgs, err := svc.resolveTargets(context.Background(), "test-trip", []string{"hotel-matabiau"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(tgs) != 1 {
		t.Fatalf("targets=%d, want 1", len(tgs))
	}
	if tgs[0].source != SourceHotel || tgs[0].lat != 43.6112 {
		t.Errorf("target=%+v, want the seed coordinates", tgs[0])
	}
	if g.calls != 0 {
		t.Errorf("geocoder calls=%d, want 0", g.calls)
	}
}

// A booked hotel whose address cannot be resolved must NOT quietly become its
// city: the fallback is allowed, staying silent about it is not.
func TestResolveTargets_UnresolvableAddressFallsBackAndSaysSo(t *testing.T) {
	cases := []struct {
		name     string
		geocoder geocode.Geocoder
		wantNote string
	}{
		{"address unknown", &stubGeocoder{}, "adresse introuvable"},
		{"geocoder down", &stubGeocoder{err: errors.New("nominatim 503")}, "non résolue"},
		{"no geocoder at all", nil, "géocodage indisponible"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := tripWith(t, matabiauSeed())
			svc := &Service{DB: db, Geocoder: tc.geocoder}

			tgs, err := svc.resolveTargets(context.Background(), "test-trip", []string{"hotel-matabiau"}, false)
			if err != nil {
				t.Fatal(err)
			}
			if len(tgs) != 1 {
				t.Fatalf("targets=%d, want 1", len(tgs))
			}
			tg := tgs[0]
			if tg.source != SourceStep {
				t.Errorf("source=%q, want the stop fallback", tg.source)
			}
			if tg.lat != 43.6045 || tg.lon != 1.4440 {
				t.Errorf("point=%f,%f, want the stop", tg.lat, tg.lon)
			}
			if tg.note == "" {
				t.Fatal("want a note explaining the hotel address was not used")
			}
			if !strings.Contains(tg.note, tc.wantNote) {
				t.Errorf("note=%q, want it to mention %q", tg.note, tc.wantNote)
			}
		})
	}
}

// A booked hotel with no address in the seed: same honesty requirement.
func TestResolveTargets_BookedWithoutAddress(t *testing.T) {
	seed := matabiauSeed()
	delete(seed["hotels"].(map[string]any)["hotel-matabiau"].(map[string]any), "addr")

	db := tripWith(t, seed)
	g := &stubGeocoder{}
	svc := &Service{DB: db, Geocoder: g}

	tgs, _ := svc.resolveTargets(context.Background(), "test-trip", []string{"hotel-matabiau"}, false)
	if len(tgs) != 1 {
		t.Fatalf("targets=%d", len(tgs))
	}
	if tgs[0].source != SourceStep || tgs[0].note == "" {
		t.Errorf("target=%+v, want a step fallback with a note", tgs[0])
	}
	if g.calls != 0 {
		t.Errorf("geocoder calls=%d, want 0 without an address", g.calls)
	}
}

// The Résa button has always sent the STOP id: it must still select the hotel
// target, otherwise the existing UI silently analyses nothing.
func TestResolveTargets_StopIDStillSelectsTheHotelTarget(t *testing.T) {
	db := tripWith(t, matabiauSeed())
	g := &stubGeocoder{points: map[string]geocode.Point{
		"64 Boulevard Pierre Sémard, 31000 Toulouse": {Lat: 43.6112, Lon: 1.4536},
	}}
	svc := &Service{DB: db, Geocoder: g}

	tgs, err := svc.resolveTargets(context.Background(), "test-trip", []string{"toulouse"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(tgs) != 2 {
		t.Fatalf("targets=%d, want the 2 hotels of that stop", len(tgs))
	}
	if _, ok := findTarget(tgs, "hotel-matabiau"); !ok {
		t.Error("asking for the stop must include its booked hotel")
	}
}

// bookingRef alone means booked (same retro-compat as the QA extractor).
func TestHotelStatus_Precedence(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want string
	}{
		{"bookingStatus wins", map[string]any{"status": "candidate", "bookingStatus": "booked"}, "booked"},
		{"status alone", map[string]any{"status": "to_book"}, "to_book"},
		{"bookingRef implies booked", map[string]any{"bookingRef": "ABC123"}, "booked"},
		{"confirmationNumber implies booked", map[string]any{"confirmationNumber": "XYZ"}, "booked"},
		{"nothing", map[string]any{}, ""},
		{"empty bookingStatus does not erase status", map[string]any{"status": "booked", "bookingStatus": ""}, "booked"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hotelStatus(tc.in); got != tc.want {
				t.Errorf("hotelStatus=%q, want %q", got, tc.want)
			}
		})
	}
}

// Stops with no hotel keep the previous behaviour: they are analysed.
func TestResolveTargets_StopsWithoutHotelAreStillAnalysed(t *testing.T) {
	seed := matabiauSeed()
	seed["locations"].(map[string]any)["nice"] = map[string]any{"name": "Nice", "lat": 43.7102, "lon": 7.2620}

	db := tripWith(t, seed)
	svc := &Service{DB: db, Geocoder: &stubGeocoder{}}

	tgs, _ := svc.resolveTargets(context.Background(), "test-trip", nil, true)
	nice, ok := findTarget(tgs, "nice")
	if !ok {
		t.Fatalf("stop without hotel missing: %+v", tgs)
	}
	if nice.source != SourceStep || nice.hotelID != "" {
		t.Errorf("target=%+v, want a plain stop target", nice)
	}
	if nice.note != "" {
		t.Errorf("note=%q, want none: a stop with no hotel is not a fallback", nice.note)
	}
}

// hotels{} may arrive as an array carrying its own id (seed-import writes both
// shapes).
func TestExtractHotelInfos_DictAndArrayShapes(t *testing.T) {
	dict := extractHotelInfos(map[string]any{"hotels": map[string]any{
		"h1": map[string]any{"name": "A", "addr": "1 rue A", "bookingStatus": "booked"},
	}})
	if h := dict["h1"]; h.name != "A" || h.addr != "1 rue A" || !h.booked() {
		t.Errorf("dict shape: %+v", h)
	}

	arr := extractHotelInfos(map[string]any{"hotels": []any{
		map[string]any{"hotelId": "h2", "name": "B", "address": "2 rue B", "status": "booked"},
	}})
	if h := arr["h2"]; h.name != "B" || h.addr != "2 rue B" || !h.booked() {
		t.Errorf("array shape: %+v", h)
	}
}

// The resolved address is cached: the public Nominatim instance allows one
// request per second, and a street address does not move.
func TestGeocodeCache_AvoidsASecondLookup(t *testing.T) {
	db := tripWith(t, matabiauSeed())
	g := &stubGeocoder{points: map[string]geocode.Point{
		"64 Boulevard Pierre Sémard, 31000 Toulouse": {Lat: 43.6112, Lon: 1.4536},
	}}
	svc := &Service{DB: db, Geocoder: g}

	for i := 0; i < 3; i++ {
		if _, err := svc.resolveTargets(context.Background(), "test-trip", []string{"hotel-matabiau"}, false); err != nil {
			t.Fatal(err)
		}
	}
	if g.calls != 1 {
		t.Errorf("geocoder calls=%d over 3 runs, want 1", g.calls)
	}
}

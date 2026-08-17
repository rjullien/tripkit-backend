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
	if err := db.AutoMigrate(&models.Trip{}, &models.Day{}, &models.Hotel{}, &models.ConstructionCheck{}, &models.DiscoveryCache{}); err != nil {
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

// The construction rule: a hotel with an address is analysed at THAT building,
// whether it is booked, to_book or still a candidate. Analysing the stop gave
// every hotel of a city the same verdict — useless when comparing alternatives.
func TestResolveTargets_HotelWithAddressUsesItsOwnBuilding(t *testing.T) {
	db := tripWith(t, matabiauSeed())
	g := &stubGeocoder{points: map[string]geocode.Point{
		"64 Boulevard Pierre Sémard, 31000 Toulouse": {Lat: 43.6112, Lon: 1.4536, DisplayName: "64 Bd Pierre Semard, Toulouse"},
		"1 Place du Capitole, 31000 Toulouse":        {Lat: 43.6043, Lon: 1.4437, DisplayName: "Place du Capitole, Toulouse"},
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

	// Construction: a candidate with an address is a potential hotel, not the city centre.
	cand, ok := findTarget(tgs, "hotel-candidat")
	if !ok {
		t.Fatal("candidate hotel missing from targets")
	}
	if cand.source != SourceHotel {
		t.Errorf("candidate source=%q, want %q (construction checks alternatives)", cand.source, SourceHotel)
	}
	if cand.lat != 43.6043 || cand.lon != 1.4437 {
		t.Errorf("candidate point=%f,%f, want its own geocoded address", cand.lat, cand.lon)
	}
	if cand.note != "" {
		t.Errorf("candidate note=%q, want none when its address was used", cand.note)
	}
	if g.calls != 2 {
		t.Errorf("geocoder calls=%d, want 2 (booked + candidate)", g.calls)
	}

	// Two hotels in one city = two distinct targets, no overwrite.
	if len(tgs) != 2 {
		t.Errorf("targets=%d, want 2 (one per hotel, the stop is claimed)", len(tgs))
	}
}

func TestResolveTargets_ToBookWithAddressUsesHotel(t *testing.T) {
	seed := matabiauSeed()
	h := seed["hotels"].(map[string]any)["hotel-candidat"].(map[string]any)
	h["bookingStatus"] = "to_book"
	delete(h, "status")

	db := tripWith(t, seed)
	g := &stubGeocoder{points: map[string]geocode.Point{
		"1 Place du Capitole, 31000 Toulouse": {Lat: 43.6043, Lon: 1.4437},
	}}
	svc := &Service{DB: db, Geocoder: g}

	tgs, err := svc.resolveTargets(context.Background(), "test-trip", []string{"hotel-candidat"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(tgs) != 1 {
		t.Fatalf("targets=%d, want 1", len(tgs))
	}
	if tgs[0].source != SourceHotel || tgs[0].lat != 43.6043 {
		t.Errorf("to_book target=%+v, want the hotel address", tgs[0])
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

// A hotel whose address cannot be resolved must NOT quietly become its
// city: we demand hotels[].addr instead of scoring the stop.
func TestResolveTargets_UnresolvableAddressDemandsIt(t *testing.T) {
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
			if tg.source != SourceMissing {
				t.Errorf("source=%q, want %q (do not score the city)", tg.source, SourceMissing)
			}
			if tg.lat != 0 || tg.lon != 0 {
				t.Errorf("point=%f,%f, want none: no building to measure", tg.lat, tg.lon)
			}
			if tg.note == "" {
				t.Fatal("want a note asking for hotels[].addr")
			}
			if !strings.Contains(tg.note, tc.wantNote) {
				t.Errorf("note=%q, want it to mention %q", tg.note, tc.wantNote)
			}
			if !strings.Contains(tg.note, "hotels[].addr") {
				t.Errorf("note=%q, want it to demand hotels[].addr", tg.note)
			}
		})
	}
}

// No addr in the seed: search the name + city and SHOW the hit so the user
// can object if Nominatim picked the wrong building.
func TestResolveTargets_NoAddrSearchesByNameAndShowsIt(t *testing.T) {
	seed := matabiauSeed()
	delete(seed["hotels"].(map[string]any)["hotel-matabiau"].(map[string]any), "addr")

	db := tripWith(t, seed)
	g := &stubGeocoder{points: map[string]geocode.Point{
		"Hôtel Matabiau, Toulouse": {Lat: 43.6112, Lon: 1.4536, DisplayName: "Hôtel Matabiau, 64 Bd Pierre Semard, Toulouse"},
	}}
	svc := &Service{DB: db, Geocoder: g}

	tgs, err := svc.resolveTargets(context.Background(), "test-trip", []string{"hotel-matabiau"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(tgs) != 1 {
		t.Fatalf("targets=%d, want 1", len(tgs))
	}
	tg := tgs[0]
	if tg.source != SourceGuessed {
		t.Errorf("source=%q, want %q", tg.source, SourceGuessed)
	}
	if tg.lat != 43.6112 || tg.lon != 1.4536 {
		t.Errorf("point=%f,%f, want the name-search hit", tg.lat, tg.lon)
	}
	if !strings.Contains(tg.addr, "Hôtel Matabiau") {
		t.Errorf("addr=%q, want the Nominatim label shown so the user can object", tg.addr)
	}
	if !strings.Contains(tg.note, "point proposé") {
		t.Errorf("note=%q, want it to say this is a proposal", tg.note)
	}
	if g.calls != 1 {
		t.Errorf("geocoder calls=%d, want 1 (name + city)", g.calls)
	}
}

// No addr and the name search is empty: demand the address, do not score the city.
func TestResolveTargets_HotelWithoutAddressDemandsIt(t *testing.T) {
	seed := matabiauSeed()
	delete(seed["hotels"].(map[string]any)["hotel-matabiau"].(map[string]any), "addr")

	db := tripWith(t, seed)
	g := &stubGeocoder{}
	svc := &Service{DB: db, Geocoder: g}

	tgs, _ := svc.resolveTargets(context.Background(), "test-trip", []string{"hotel-matabiau"}, false)
	if len(tgs) != 1 {
		t.Fatalf("targets=%d", len(tgs))
	}
	if tgs[0].source != SourceMissing {
		t.Errorf("source=%q, want %q", tgs[0].source, SourceMissing)
	}
	if tgs[0].note == "" || !strings.Contains(tgs[0].note, "hotels[].addr") {
		t.Errorf("note=%q, want a demand for hotels[].addr", tgs[0].note)
	}
	if tgs[0].lat != 0 || tgs[0].lon != 0 {
		t.Errorf("point=%f,%f, want none", tgs[0].lat, tgs[0].lon)
	}
	if g.calls != 1 {
		t.Errorf("geocoder calls=%d, want 1 (name search)", g.calls)
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


// ── Fix #1: Pre-departure (day < 1) exclusion ──────────────────────────────

// Day 0 (J-1) locations and hotels are excluded from the all=true scan.
func TestResolveTargets_PreDepartureExcludedFromAll(t *testing.T) {
	seed := map[string]any{
		"locations": map[string]any{
			"nice":     map[string]any{"name": "Nice", "lat": 43.7102, "lon": 7.2620},
			"toulouse": map[string]any{"name": "Toulouse", "lat": 43.6045, "lon": 1.4440},
		},
		"days": []any{
			map[string]any{"day": 0, "locationId": "nice", "hotelId": "hotel-nice"},
			map[string]any{"day": 1, "locationId": "toulouse", "hotelId": "hotel-matabiau"},
		},
		"hotels": map[string]any{
			"hotel-nice": map[string]any{
				"name": "Hôtel Nice J-1",
				"addr": "1 Promenade des Anglais, Nice",
			},
			"hotel-matabiau": map[string]any{
				"name": "Hôtel Matabiau",
				"addr": "64 Boulevard Pierre Sémard, 31000 Toulouse",
			},
		},
	}
	db := tripWith(t, seed)
	g := &stubGeocoder{points: map[string]geocode.Point{
		"64 Boulevard Pierre Sémard, 31000 Toulouse": {Lat: 43.6112, Lon: 1.4536},
	}}
	svc := &Service{DB: db, Geocoder: g}

	tgs, err := svc.resolveTargets(context.Background(), "test-trip", nil, true)
	if err != nil {
		t.Fatal(err)
	}

	// Nice (day 0) must NOT appear.
	if _, ok := findTarget(tgs, "hotel-nice"); ok {
		t.Error("hotel-nice (day 0 / J-1) should be excluded from all=true")
	}
	if _, ok := findTarget(tgs, "nice"); ok {
		t.Error("location nice (day 0 / J-1) should be excluded from all=true")
	}

	// Toulouse (day 1) must appear.
	if _, ok := findTarget(tgs, "hotel-matabiau"); !ok {
		t.Error("hotel-matabiau (day 1) should be included")
	}
}

// When explicitly requested, a pre-departure location is still returned.
func TestResolveTargets_PreDepartureIncludedWhenExplicit(t *testing.T) {
	seed := map[string]any{
		"locations": map[string]any{
			"nice":     map[string]any{"name": "Nice", "lat": 43.7102, "lon": 7.2620},
			"toulouse": map[string]any{"name": "Toulouse", "lat": 43.6045, "lon": 1.4440},
		},
		"days": []any{
			map[string]any{"day": 0, "locationId": "nice", "hotelId": "hotel-nice"},
			map[string]any{"day": 1, "locationId": "toulouse", "hotelId": "hotel-matabiau"},
		},
		"hotels": map[string]any{
			"hotel-nice": map[string]any{
				"name": "Hôtel Nice J-1",
				"lat":  43.7102,
				"lon":  7.2620,
			},
			"hotel-matabiau": map[string]any{
				"name": "Hôtel Matabiau",
				"addr": "64 Boulevard Pierre Sémard, 31000 Toulouse",
			},
		},
	}
	db := tripWith(t, seed)
	svc := &Service{DB: db, Geocoder: &stubGeocoder{}}

	// Explicit request for the J-1 hotel: should still work.
	tgs, err := svc.resolveTargets(context.Background(), "test-trip", []string{"hotel-nice"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(tgs) != 1 {
		t.Fatalf("targets=%d, want 1 (explicit request overrides filter)", len(tgs))
	}
	if tgs[0].id != "hotel-nice" {
		t.Errorf("target.id=%q, want hotel-nice", tgs[0].id)
	}
}

// A location that appears on BOTH day 0 and day 3 is NOT excluded.
func TestResolveTargets_SharedLocationNotExcluded(t *testing.T) {
	seed := map[string]any{
		"locations": map[string]any{
			"montreal": map[string]any{"name": "Montréal", "lat": 45.5017, "lon": -73.5673},
		},
		"days": []any{
			map[string]any{"day": 0, "locationId": "montreal"},
			map[string]any{"day": 17, "locationId": "montreal", "hotelId": "hotel-mtl"},
		},
		"hotels": map[string]any{
			"hotel-mtl": map[string]any{
				"name": "Hôtel Montréal",
				"lat":  45.5017,
				"lon":  -73.5673,
			},
		},
	}
	db := tripWith(t, seed)
	svc := &Service{DB: db, Geocoder: &stubGeocoder{}}

	tgs, err := svc.resolveTargets(context.Background(), "test-trip", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findTarget(tgs, "hotel-mtl"); !ok {
		t.Error("hotel on both day 0 and day 17 should NOT be excluded")
	}
}

// ── Fix #2: Hotel addr enrichment from DB Hotels table ──────────────────────

// When trip.Data.hotels lacks addr but the Hotels DB table has it, the addr
// is enriched before geocoding.
func TestResolveTargets_EnrichesAddrFromHotelsTable(t *testing.T) {
	// trip.Data has the hotel without addr
	seed := map[string]any{
		"locations": map[string]any{
			"toulouse": map[string]any{"name": "Toulouse", "lat": 43.6045, "lon": 1.4440},
		},
		"days": []any{
			map[string]any{"day": 1, "locationId": "toulouse", "hotelId": "hotel-matabiau"},
		},
		"hotels": map[string]any{
			"hotel-matabiau": map[string]any{
				"name": "Hôtel Matabiau",
				// addr intentionally missing from trip.Data
			},
		},
	}
	db := tripWith(t, seed)

	// But the Hotels DB table has the full data with addr
	hotelData := `{"hotelId":"hotel-matabiau","name":"Hôtel Matabiau","addr":"64 Boulevard Pierre Sémard, 31000 Toulouse","dayNums":[1]}`
	if err := db.Create(&models.Hotel{TripID: "test-trip", DayNum: 1, Data: hotelData}).Error; err != nil {
		t.Fatal(err)
	}

	g := &stubGeocoder{points: map[string]geocode.Point{
		"64 Boulevard Pierre Sémard, 31000 Toulouse": {Lat: 43.6112, Lon: 1.4536},
	}}
	svc := &Service{DB: db, Geocoder: g}

	tgs, err := svc.resolveTargets(context.Background(), "test-trip", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	tg, ok := findTarget(tgs, "hotel-matabiau")
	if !ok {
		t.Fatalf("hotel-matabiau missing from targets: %+v", tgs)
	}
	// Should have used the addr from the Hotels table.
	if tg.source != SourceHotel {
		t.Errorf("source=%q, want %q (addr enriched from Hotels table)", tg.source, SourceHotel)
	}
	if tg.lat != 43.6112 {
		t.Errorf("lat=%f, want 43.6112 (geocoded from enriched addr)", tg.lat)
	}
}

// When trip.Data.hotels has no entry at all but the Hotels DB table does,
// the hotel is still discovered and analysed.
func TestResolveTargets_HotelOnlyInDBTable(t *testing.T) {
	seed := map[string]any{
		"locations": map[string]any{
			"toulouse": map[string]any{"name": "Toulouse", "lat": 43.6045, "lon": 1.4440},
		},
		"days": []any{
			map[string]any{"day": 1, "locationId": "toulouse", "hotelId": "hotel-missing"},
		},
		"hotels": map[string]any{
			// hotel-missing is NOT in trip.Data.hotels
		},
	}
	db := tripWith(t, seed)

	// But it IS in the Hotels DB table
	hotelData := `{"hotelId":"hotel-missing","name":"Hôtel Fantôme","addr":"10 Rue Test, Toulouse","dayNums":[1]}`
	if err := db.Create(&models.Hotel{TripID: "test-trip", DayNum: 1, Data: hotelData}).Error; err != nil {
		t.Fatal(err)
	}

	g := &stubGeocoder{points: map[string]geocode.Point{
		"10 Rue Test, Toulouse": {Lat: 43.600, Lon: 1.440},
	}}
	svc := &Service{DB: db, Geocoder: g}

	tgs, err := svc.resolveTargets(context.Background(), "test-trip", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	tg, ok := findTarget(tgs, "hotel-missing")
	if !ok {
		t.Fatalf("hotel-missing should be discovered from DB table: %+v", tgs)
	}
	if tg.source != SourceHotel {
		t.Errorf("source=%q, want %q", tg.source, SourceHotel)
	}
	if tg.addr == "" {
		t.Error("addr should be filled from DB table")
	}
}

// ── Fix #1: DB days used when trip.Data has no days ─────────────────────────

// When trip.Data has no "days" key (publish path), the Days DB table is used
// for ordering, linking, and pre-departure filtering.
func TestResolveTargets_UsesDBDaysWhenTripDataHasNone(t *testing.T) {
	// trip.Data has no "days" key — like the publish path
	seed := map[string]any{
		"locations": map[string]any{
			"nice":     map[string]any{"name": "Nice", "lat": 43.7102, "lon": 7.2620},
			"toulouse": map[string]any{"name": "Toulouse", "lat": 43.6045, "lon": 1.4440},
		},
		"hotels": map[string]any{
			"hotel-nice": map[string]any{
				"name": "Hôtel Nice J-1",
				"lat":  43.7102,
				"lon":  7.2620,
			},
			"hotel-matabiau": map[string]any{
				"name": "Hôtel Matabiau",
				"addr": "64 Boulevard Pierre Sémard, 31000 Toulouse",
			},
		},
		// No "days" key!
	}
	db := tripWith(t, seed)

	// Insert days into the DB table (like the publish path does)
	day0 := `{"day":0,"locationId":"nice","hotelId":"hotel-nice"}`
	day1 := `{"day":1,"locationId":"toulouse","hotelId":"hotel-matabiau"}`
	if err := db.Create(&models.Day{TripID: "test-trip", DayNum: 0, Data: day0}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.Day{TripID: "test-trip", DayNum: 1, Data: day1}).Error; err != nil {
		t.Fatal(err)
	}

	g := &stubGeocoder{points: map[string]geocode.Point{
		"64 Boulevard Pierre Sémard, 31000 Toulouse": {Lat: 43.6112, Lon: 1.4536},
	}}
	svc := &Service{DB: db, Geocoder: g}

	tgs, err := svc.resolveTargets(context.Background(), "test-trip", nil, true)
	if err != nil {
		t.Fatal(err)
	}

	// Nice (day 0) excluded.
	if _, ok := findTarget(tgs, "hotel-nice"); ok {
		t.Error("hotel-nice (day 0) should be excluded from all=true when days are from DB")
	}
	if _, ok := findTarget(tgs, "nice"); ok {
		t.Error("location nice (day 0) should be excluded when days are from DB")
	}

	// Toulouse (day 1) included with correct location link.
	tg, ok := findTarget(tgs, "hotel-matabiau")
	if !ok {
		t.Fatalf("hotel-matabiau (day 1) should be included: %+v", tgs)
	}
	if tg.locationID != "toulouse" {
		t.Errorf("locationID=%q, want toulouse (linked via DB days)", tg.locationID)
	}
}

// ── preDepartureOnly unit test ──────────────────────────────────────────────

func TestPreDepartureOnly(t *testing.T) {
	daySlice := []map[string]any{
		{"day": float64(0), "locationId": "nice", "hotelId": "hotel-nice"},
		{"day": float64(0), "locationId": "nice"},
		{"day": float64(1), "locationId": "toulouse", "hotelId": "hotel-toulouse"},
		{"day": float64(2), "locationId": "montreal", "hotelId": "hotel-montreal"},
	}
	locOnly, hotelOnly := preDepartureOnly(daySlice)

	if !locOnly["nice"] {
		t.Error("nice should be pre-departure-only (only on day 0)")
	}
	if !hotelOnly["hotel-nice"] {
		t.Error("hotel-nice should be pre-departure-only")
	}
	if locOnly["toulouse"] {
		t.Error("toulouse should NOT be pre-departure-only (on day 1)")
	}
	if hotelOnly["hotel-toulouse"] {
		t.Error("hotel-toulouse should NOT be pre-departure-only")
	}
	if locOnly["montreal"] {
		t.Error("montreal should NOT be pre-departure-only")
	}
}

func TestPreDepartureOnly_SharedLocation(t *testing.T) {
	daySlice := []map[string]any{
		{"day": float64(0), "locationId": "montreal"},
		{"day": float64(17), "locationId": "montreal", "hotelId": "hotel-roberval"},
	}
	locOnly, hotelOnly := preDepartureOnly(daySlice)

	if locOnly["montreal"] {
		t.Error("montreal on day 0 AND day 17 should NOT be pre-departure-only")
	}
	if hotelOnly["hotel-roberval"] {
		t.Error("hotel-roberval on day 17 should NOT be pre-departure-only")
	}
}

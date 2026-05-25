# TripKit Backend — Data Model & Import Guide

> **How it all works** — from seed file to visible trip in the frontend.

## Architecture Overview

```
┌─────────────────┐     seed-import.cjs      ┌──────────────────┐
│  Seed file (.js) │ ─────────────────────────▶ │  Backend (Go)    │
│  (DATA-MODEL.md) │                            │  SQLite DB       │
└─────────────────┘                            └──────────────────┘
                                                        │
                                                        │ GET /api/trips/:id/seed
                                                        ▼
                                               ┌──────────────────┐
                                               │  Frontend (PWA)  │
                                               │  localStorage    │
                                               └──────────────────┘
```

## How a Trip Becomes Visible

### Flow: First Visit (no localStorage cache)

1. Frontend boots → calls `API.getTrips()` → `GET /api/trips`
2. Backend returns array of trips (or if `TRIPKIT_CONFIG.defaultTripId` set, skips this)
3. Frontend picks `trips[0].id` (first trip)
4. Frontend calls `API.checkVersion(tripId)` → `GET /api/trips/:id/version`
5. If version differs from cached → `API.fetchSeed(tripId)` → `GET /api/trips/:id/seed`
6. Frontend stores seed in `localStorage` via `Store.setTripData(tripId, data)`
7. Frontend registers trip: `Store.registerTrip(tripId)` → adds to `tk-trips` array in localStorage
8. Renders UI

### Flow: Subsequent Visits (has localStorage)

1. Frontend reads `Store.getCurrentTripId()` from localStorage
2. Renders immediately from localStorage cache
3. Background: version check → if changed, re-fetches seed

### ⚠️ Critical: ACL & Group Access (MUST DO for new trips)

The backend uses **group-based ACL**. A trip MUST be assigned to a group
for users to see it in the listing.

**Without this step, the trip is created but INVISIBLE in the frontend listing.**

Even admin users will see all trips (admin bypass in `AllowedTripIDs`),
but non-admin users will only see trips assigned to their groups.

#### Steps to grant access:

```bash
# 1. Check existing groups
curl -s $API/api/groups -H "Remote-User: <admin-user>"

# 2. Add the new trip to the appropriate group
curl -s $API/api/groups/<group-id> -X PUT \
  -H "Content-Type: application/json" \
  -H "Remote-User: <admin-user>" \
  -d '{
    "name": "<Group Name>",
    "members": ["user1", "user2"],
    "trips": ["existing-trip-1", "existing-trip-2", "NEW-TRIP-ID"]
  }'
```

#### How the ACL works (middleware/tripacl.go):

- `trip_accesses` table links `trip_id` → `group_id`
- `group_members` table links `group_id` → `username`
- `AllowedTripIDs(db, username)`: 
  - If table is empty → nil (open mode, all visible)
  - If user is admin → nil (all visible) *(fixed in v1.10.0)*
  - Otherwise → returns only trip IDs where user has group membership
- `TripACL` middleware: per-request check on single-trip endpoints

#### Gotcha:
The `seed-import.cjs` does NOT add the trip to any group.
You MUST manually add it via `PUT /api/groups/:groupId` after import.

## Key Insight: Why a Trip Might Not Show

The frontend expects `GET /api/trips` to return a **flat JSON array**:
```json
[{"id": "ecosse-2026", "name": "...", ...}]
```

**Known issue:** The deployed backend image wraps the response:
```json
{"results": []}
```

The frontend code does `if (trips && trips.length)` — this fails on an object.

### Workarounds

1. **Set `defaultTripId` in frontend config** (Docker env `DEFAULT_TRIP_ID`):
   - ConfigMap or `config.js` → `TRIPKIT_CONFIG.defaultTripId = "ecosse-2026"`
   - This skips the listing entirely

2. **Fix the backend response** — make `ListTrips` return a flat array
   
3. **Access directly via URL** — `https://tripkit.bapttf.com/#programme` 
   (only works if the FE can resolve the trip ID through config)

---

## Backend Database Schema (SQLite / Postgres)

### Tables

| Table | Purpose | Key |
|-------|---------|-----|
| `trips` | Trip metadata (name, dates, emoji, freeform `data` JSON) | `id` (string slug) |
| `days` | Per-day content (timeline, activities, highlights) | `trip_id` + `day_num` |
| `hotels` | Accommodation entries per day-num | `trip_id` + `day_num` |
| `lists` | Checklists (packing, todo) | `id` (string) |
| `list_checks` | Per-item check state (multi-device sync) | `list_id` + `item_id` |
| `list_custom_items` | User-added items | `id` |
| `list_hidden` | Per-device hidden items | `list_id` + `device_id` + `item_id` |

### Trip Record

```sql
CREATE TABLE trips (
  id         TEXT PRIMARY KEY,   -- slug: "ecosse-2026"
  name       TEXT NOT NULL,      -- "Écosse — 30 ans de mariage"
  emoji      TEXT,               -- "🏴󠁧󠁢󠁳󠁣󠁴󠁿"
  start_date TEXT,               -- "2026-06-24"
  end_date   TEXT,               -- "2026-06-29"
  data       TEXT,               -- JSON blob (trip.data from seed: travelers, phases, hotels, locations, restaurants, culture, sharedLinks)
  created_at DATETIME,
  updated_at DATETIME
);
```

### Day Record

```sql
CREATE TABLE days (
  id      INTEGER PRIMARY KEY AUTOINCREMENT,
  trip_id TEXT NOT NULL REFERENCES trips(id) ON DELETE CASCADE,
  day_num INTEGER NOT NULL,       -- 0-based
  data    TEXT NOT NULL,           -- JSON blob (full day object from seed)
  UNIQUE(trip_id, day_num)
);
```

### Hotel Record

```sql
CREATE TABLE hotels (
  id      INTEGER PRIMARY KEY AUTOINCREMENT,
  trip_id TEXT NOT NULL REFERENCES trips(id) ON DELETE CASCADE,
  day_num INTEGER NOT NULL,       -- index position (0, 1, 2...)
  data    TEXT NOT NULL,           -- JSON blob (hotel object)
  INDEX(trip_id, day_num)
);
```

---

## API Endpoints

| Method | Path | Description | Response |
|--------|------|-------------|----------|
| GET | `/health` | Health check | `{"status":"ok"}` |
| GET | `/api/trips` | List all trips | `[{id, name, emoji, start_date, end_date, data, created_at, updated_at}, ...]` |
| POST | `/api/trips` | Create trip | Trip object |
| GET | `/api/trips/:id` | Get single trip + daysCount | Trip + `daysCount` field |
| PUT | `/api/trips/:id` | Update trip | Trip object |
| DELETE | `/api/trips/:id` | Delete trip (cascade) | 204 |
| GET | `/api/trips/:id/version` | Lightweight version check (~50 bytes) | `{"version": <unix_ms>, "updated_at": "..."}` |
| GET | `/api/trips/:id/seed` | **Full trip data export** (what FE consumes) | See below |
| GET | `/api/trips/:id/days` | List days | `[{id, trip_id, day_num, data}, ...]` |
| PUT | `/api/trips/:id/days/:num` | Upsert day | Day object |
| GET | `/api/trips/:id/hotels` | List hotels | `[{id, trip_id, day_num, data}, ...]` |
| PUT | `/api/trips/:id/hotels/:num` | Upsert hotel | Hotel object |
| GET | `/api/trips/:id/lists` | List checklists | `[{id, trip_id, type, title, data}, ...]` |
| PUT | `/api/trips/:id/lists/:lid` | Upsert list | List object |
| DELETE | `/api/trips/:id/lists/:lid` | Delete list | 204 |
| PATCH | `/api/trips/:id/lists/:lid/sync` | Multi-device sync | Merged state |

### Seed Response Structure

`GET /api/trips/:id/seed` returns the complete trip data that the frontend stores:

```json
{
  "trip": {
    "id": "ecosse-2026",
    "name": "Écosse — 30 ans de mariage",
    "emoji": "🏴󠁧󠁢󠁳󠁣󠁴󠁿",
    "start_date": "2026-06-24",
    "end_date": "2026-06-29",
    "data": {
      "travelers": [...],
      "phases": [...],
      "hotels": { "hotel-id": {...}, ... },
      "locations": { "loc-id": {...}, ... },
      "restaurants": { "0": {...}, ... },
      "culture": [...],
      "sharedLinks": [...]
    }
  },
  "days": [
    { "day_num": 0, "data": { "day": 0, "emoji": "✈️", "label": "...", "timeline": [...], ... } },
    ...
  ],
  "hotels": [
    { "day_num": 0, "data": { "name": "The Witchery", ... } },
    ...
  ],
  "lists": [
    { "id": "checklist-ecosse", "type": "packing", "title": "🧳 Valise", "data": {...} }
  ]
}
```

---

## Importing a Trip (seed-import.cjs)

### Seed File Format

A JS file exporting a structured object (see `DATA-MODEL.md` for full schema):

```javascript
var SEED_ECOSSE_2026 = {
  trip: { id: "ecosse-2026", name: "...", startDate: "2026-06-24", ... },
  days: [{ day: 0, emoji: "✈️", label: "...", timeline: [...], ... }],
  hotels: { "the-witchery": { name: "...", addr: "...", ... } },
  locations: { "edinburgh": { lat: 55.95, lon: -3.19, tz: "Europe/London" } },
  restaurants: { "0": { main: { name: "..." } } },
  culture: [{ title: "...", sections: [...] }],
  lists: { "checklist-ecosse": { id: "...", type: "packing", ... } },
};
```

### Import Command

```bash
node seed-import.cjs --api http://tripkit-backend:3001 --seed path/to/seed.js
# With auth token:
node seed-import.cjs --api https://tripkit.bapttf.com --token $TRIPKIT_API_TOKEN --seed seed.js
```

### What seed-import Does

1. Reads the `.js` file, evals to extract the data object
2. `POST /api/trips` — creates the trip with:
   - `id`, `name`, `emoji`, `start_date`, `end_date`
   - `data` = JSON blob with travelers, phases, hotels, locations, restaurants, culture, sharedLinks
3. For each day: `PUT /api/trips/:id/days/:num` — upserts day data
4. For each hotel: `PUT /api/trips/:id/hotels/:num` — upserts hotel data  
5. For each list: `PUT /api/trips/:id/lists/:lid` — upserts list

### What the Frontend Expects from Seed

The frontend's `refreshFromBackend()` fetches `/api/trips/:id/seed` and transforms it into:

```javascript
tripData = {
  trip: { id, name, emoji, startDate, endDate, travelers, phases, ... },
  days: [ /* day objects with timeline, highlights, hotelId, locationId */ ],
  hotels: { "hotel-id": { name, addr, ... } },
  locations: { "loc-id": { lat, lon, tz } },
  restaurants: { "0": { main: {...}, alts: [...] } },
  culture: [ { title, sections } ],
  lists: { "list-id": { sections, items } },
}
```

This is stored in `localStorage` under key `tk-trip-<tripId>`.

---

## Making a New Trip Appear in the Frontend

### Option A: Set as default trip (single-trip deployment)

In the frontend ConfigMap / Docker env:

```yaml
# k3s/configmap.yaml (or Docker ENV)
DEFAULT_TRIP_ID: "ecosse-2026"
```

The frontend config.js template:
```javascript
var TRIPKIT_CONFIG = {
  apiUrl: "${API_URL}",
  apiPrefix: "${API_PREFIX}",
  defaultTripId: "${DEFAULT_TRIP_ID}",
};
```

### Option B: Fix GET /api/trips response (multi-trip)

The deployed image returns `{"results":[]}` instead of `[...]`.  
The frontend expects a flat array. Fix the handler or update the image.

### Option C: Direct URL access

If the frontend is already configured with the trip in localStorage (from a previous seed-import + page visit), it loads directly. Users can share `https://tripkit.bapttf.com/#programme` after first successful load.

---

## Environment Variables (Backend)

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `PORT` | No | `3001` | HTTP listen port |
| `DB_DRIVER` | No | `sqlite` | Database driver (`sqlite` or `postgres`) |
| `DB_PATH` | No (sqlite) | `data/tripkit.db` | SQLite file path |
| `DB_HOST` | Yes (pg) | — | Postgres host |
| `DB_PORT` | No (pg) | `5432` | Postgres port |
| `DB_USER` | Yes (pg) | — | Postgres user |
| `DB_PASSWORD` | Yes (pg) | — | Postgres password |
| `DB_NAME` | Yes (pg) | — | Postgres database name |
| `DB_SSLMODE` | No (pg) | `disable` | Postgres SSL mode |
| `TRIPKIT_API_TOKEN` | Prod | — | Bearer token (unset = dev/no-auth mode) |

## Environment Variables (Frontend)

| Variable | Description |
|----------|-------------|
| `API_URL` | Backend URL (e.g. `https://tripkit.bapttf.com`) |
| `API_PREFIX` | API path prefix (default `/api`) |
| `DEFAULT_TRIP_ID` | Trip slug to load on first visit |

---

## Quick Checklist: New Trip End-to-End

- [ ] Create seed file following `DATA-MODEL.md` schema (in frontend repo)
- [ ] Run 10-point zero-duplication audit
- [ ] Import: `node seed-import.cjs --api <url> --seed <file>`
- [ ] **⚠️ Add trip to group ACL:** `PUT /api/groups/<group-id>` with the new trip ID in `trips` array
- [ ] Verify: `curl <url>/api/trips -H "Remote-User: <admin-user>"` → new trip appears in list
- [ ] Verify: `curl <url>/api/trips/<id>/seed` returns full data
- [ ] Test: open browser, clear localStorage, reload → trip should appear in selector

---

## Database: PostgreSQL (prod)

- **Driver:** Postgres (CGO_ENABLED=0 build excludes SQLite)
- **Connection:** env vars `DB_DRIVER=postgres`, `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME`
- **JSON fields:** `trips.data` is stored as `json` type (not jsonb) — freeform JSON blob
- **ACL tables:** `groups`, `group_members`, `trip_accesses`

### ACL Schema

```sql
CREATE TABLE groups (
  id   TEXT PRIMARY KEY,   -- "family", "friends"
  name TEXT NOT NULL       -- "My Family"
);

CREATE TABLE group_members (
  group_id TEXT NOT NULL REFERENCES groups(id),
  username TEXT NOT NULL,
  PRIMARY KEY (group_id, username)
);

CREATE TABLE trip_accesses (
  trip_id  TEXT NOT NULL REFERENCES trips(id),
  group_id TEXT NOT NULL REFERENCES groups(id),
  PRIMARY KEY (trip_id, group_id)
);
```

### Example Groups

| Group | Members | Trips |
|-------|---------|-------|
| `family` | user1, user2, user3 | trip-a, trip-b |
| `friends` | user4 | trip-a |

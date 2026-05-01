# TripKit Backend

API REST en Go pour TripKit — gestion de voyages, jours, hébergements, listes avec sync multi-device.

## Stack
- **Go 1.24** + chi router
- **GORM** + SQLite
- **47 tests** avec DB in-memory isolée
- **Docker** — image Alpine légère (~15 Mo)

## API

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check (no auth) |
| `GET` | `/api/trips` | List trips |
| `POST` | `/api/trips` | Create trip |
| `GET` | `/api/trips/:id` | Get trip + daysCount |
| `PUT` | `/api/trips/:id` | Update trip |
| `DELETE` | `/api/trips/:id` | Delete trip (cascade) |
| `GET` | `/api/trips/:id/seed` | Full export |
| `GET/PUT` | `/api/trips/:id/days[/:num]` | Days CRUD (upsert) |
| `GET/PUT` | `/api/trips/:id/hotels[/:num]` | Hotels CRUD (upsert) |
| `GET/PUT/DELETE` | `/api/trips/:id/lists[/:lid]` | Lists CRUD |
| `PATCH` | `/api/trips/:id/lists/:lid/sync` | Multi-device sync |

## Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `PORT` | No | `3001` | HTTP listen port |
| `DB_PATH` | No | `data/tripkit.db` | SQLite database path |
| `TRIPKIT_API_TOKEN` | Prod | — | Bearer token. Unset = dev mode |

## Quick Start
```bash
go run ./cmd/api
# or
docker build -t tripkit-backend . && docker run -p 3001:3001 -v data:/data tripkit-backend
```

## Test
```bash
go test -v -count=1 ./...
```

## k3s
See [k3s/README.md](k3s/README.md)

## Frontend
Separate repo: [rjullien/tripkit-frontend](https://github.com/rjullien/tripkit-frontend)

## ⚠️ Ownership

**Ce repo est géré par Léa (agent IA de René).** Pas besoin de Baptiste pour le backend — Léa a accès complet au repo, peut coder, tester, pusher et releaser. Baptiste gère uniquement l'infra K3s/ArgoCD (`BaptTF/vps-infra`) qui déploie automatiquement les images Docker.

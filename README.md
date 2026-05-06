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

---

## 🚀 Process de release (CHECKLIST OBLIGATOIRE)

**🚨 RÈGLE ABSOLUE : PAS DE RELEASE SANS ACCORD DE RENÉ !**

### Étapes — dans cet ordre strict

```
☐  1. Code terminé + testé localement (go test ./...)
☐  2. Commit + push main
☐  3. Attendre CI verte (jobs test + e2e)
☐  4. Créer le tag LOCALEMENT sur le bon commit:
         git tag vX.Y.Z
         git push origin vX.Y.Z
☐  5. Créer la release GitHub:
         gh release create vX.Y.Z --title "vX.Y.Z — Description" --notes "..."
☐  6. Attendre CI release (build-and-push, ~2min)
☐  7. Vérifier image pushée:
         gh run view <id> --log | grep "pushing ghcr"
☐  8. Attendre ArgoCD auto-deploy (~3-5min):
         curl http://tripkit-backend.tripkit.svc.cluster.local:3001/health
☐  9. Re-upload assets si nécessaire (assets en DB depuis v1.7.0)
```

### Ce que fait la CI (`.github/workflows/ci.yaml`)

| Événement | Jobs | Résultat |
|-----------|------|----------|
| `push main` | test + e2e | Tests seulement, pas de build Docker |
| `release published` | test + e2e + **build-and-push** | Image Docker → ghcr.io |

### Tags Docker générés sur release

```
ghcr.io/rjullien/tripkit-backend:1.7.0        (semver sans v)
ghcr.io/rjullien/tripkit-backend:1.7           (major.minor)
ghcr.io/rjullien/tripkit-backend:latest
ghcr.io/rjullien/tripkit-backend:sha-XXXXXXX   (commit SHA)
```

### Déploiement automatique (ArgoCD Image Updater)

```
Release créée
  → CI build Docker (ghcr.io/rjullien/tripkit-backend:sha-XXX)
  → ArgoCD Image Updater détecte la nouvelle image (~2 min)
  → Met à jour .argocd-source-tripkit-backend.yaml dans BaptTF/vps-infra
  → ArgoCD sync → nouveau pod → déployé
  → Temps total : ~5 min
```

**Fichier clé côté infra :**
```
BaptTF/vps-infra/workloads/tripkit-backend/.argocd-source-tripkit-backend.yaml
```

### APP_VERSION (affiché dans /health)

Le Dockerfile reçoit `APP_VERSION` en build-arg depuis la CI :
```dockerfile
ARG APP_VERSION=dev
ENV APP_VERSION=${APP_VERSION}
```
La CI passe `--build-args APP_VERSION=${{ github.event.release.tag_name }}`.
Le handler `/health` lit `os.Getenv("APP_VERSION")`.

### Vérifier le déploiement

```bash
# Health check
curl http://tripkit-backend.tripkit.svc.cluster.local:3001/health
# → {"status":"ok","version":"v1.7.0"}

# Image utilisée par ArgoCD
gh api repos/BaptTF/vps-infra/contents/workloads/tripkit-backend/.argocd-source-tripkit-backend.yaml --jq '.content' | base64 -d
```

### Si le deploy ne se fait pas

1. **CI verte ?** → `gh run list --repo rjullien/tripkit-backend --limit 3`
2. **Image pushée ?** → Vérifier dans les logs CI `pushing ghcr.io/...`
3. **Image Updater a écrit ?** → Vérifier `.argocd-source-tripkit-backend.yaml`
4. **Pod crash ?** → Le pod peut crasher si la migration SQLite échoue
5. **ArgoCD sync ?** → Demander à Baptiste de vérifier l'UI ArgoCD

### ⚠️ Pièges

1. **Tag sur le bon commit** : TOUJOURS créer le tag APRÈS le push du code. Si tag avant push → Docker build sans le code.
2. **Ne jamais release depuis un commit qui n'est pas sur main** : Sinon ArgoCD ne sync pas.
3. **APP_VERSION vient du tag** : Le `/health` affiche le tag de la release, pas un fichier.
4. **Assets en DB (v1.7.0+)** : Les assets sont des BLOBs SQLite. Après un fresh deploy, re-upload les images.

---

## 🔐 Auth & ACL (v1.6.0+)

### Modes d'authentification

| Mode | Condition | Résultat |
|------|-----------|----------|
| Dev mode | Ni `TRIPKIT_API_TOKEN` ni `TRIPKIT_JWT_SECRET` set | Tout le monde = admin |
| Admin token | `Authorization: Bearer <TRIPKIT_API_TOKEN>` | Full access |
| JWT (magic link) | `Authorization: Bearer <jwt>` | Scopé par trip_id |
| Authelia | Header `Remote-User` (sans Bearer) | role = viewer |

### Groupes ACL (v1.6.0+)

Trips protégés par groupes. Voir frontend README pour la doc complète.

```bash
# Créer/modifier un groupe
PUT /api/groups/{groupId}
{"name": "Famille Jullien", "members": ["Rene", ...], "trips": ["usa-2026", ...]}

# Lister
GET /api/groups

# Supprimer
DELETE /api/groups/{groupId}
```

**Rene = admin bypass** (voit toujours tout).

---

## 💾 Assets (v1.7.0+)

Assets stockés en BLOB dans SQLite (pas filesystem) pour persistance.

```bash
# Upload
PUT /api/trips/{id}/assets/{filename}
Content-Type: image/jpeg
<binary data>

# Download
GET /api/trips/{id}/assets/{filename}

# List
GET /api/trips/{id}/assets
```

Max 5MB par asset. Survive aux redeploys.

---

## k3s
See [k3s/README.md](k3s/README.md)

## Frontend
Separate repo: [rjullien/tripkit-frontend](https://github.com/rjullien/tripkit-frontend)

## ⚠️ Ownership

**Ce repo est géré par Léa (agent IA de René).** Pas besoin de Baptiste pour le backend — Léa a accès complet au repo, peut coder, tester, pusher et releaser. Baptiste gère uniquement l'infra K3s/ArgoCD (`BaptTF/vps-infra`) qui déploie automatiquement les images Docker.

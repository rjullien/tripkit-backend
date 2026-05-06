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
| `GET/PUT/DELETE` | `/api/trips/:id/assets[/:file]` | Assets CRUD (map images) |
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
# Upload (crée ou écrase)
PUT /api/trips/{id}/assets/{filename}
Content-Type: image/jpeg
<binary data>

# Download
GET /api/trips/{id}/assets/{filename}

# List (sans data blob)
GET /api/trips/{id}/assets

# Delete
DELETE /api/trips/{id}/assets/{filename}
```

Max 5MB par asset. Survive aux redeploys.

### 🚨 Findings (mai 2026)

**Problème détecté :** Les assets Langon `day-XX-route.jpg` étaient tous le même fichier (48236 bytes, screenshot du cookie wall Google Maps en allemand). Cause : Playwright n'avait pas accepté les cookies avant de screenshoter.

**Fix :** Script `frontend/scripts/generate-route-maps.cjs` qui :
1. Lance Playwright headless
2. Accepte le cookie consent
3. Attend le rendu complet des tiles Google Maps
4. Screenshot en JPEG 85% qualité

**Assets actuels (mai 2026) :**
| Trip | Assets | Status |
|------|--------|--------|
| `langon-2026` | `map-overview.jpg` + `day-00` à `day-09` | ✅ Vraies cartes Google Maps |
| `usa-2026` | `map-overview.jpg` + `day-01` à `day-13` + `day-16` à `day-19` | ✅ Vraies cartes |

**Vérification qualité :** Toujours vérifier visuellement après upload que l'image est une vraie carte (pas un cookie wall, erreur 404, ou placeholder).

---

## 🗄️ Base de données

### Production : PostgreSQL (via CNPG)

**Le backend en prod utilise PostgreSQL, PAS SQLite !**

Le Dockerfile build avec `CGO_ENABLED=0` — le driver SQLite (qui nécessite CGO) n'est PAS inclus dans le binaire prod. Seul le driver PostgreSQL est compilé.

| Env | Driver | Fichier |
|-----|--------|--------|
| Dev local | SQLite | `./data/tripkit.db` |
| Tests CI | SQLite in-memory | `file:memdb_*` |
| **Prod K3s** | **PostgreSQL** | CloudNativePG cluster |

**Config prod (env vars dans deployment.yaml) :**
```
DB_DRIVER=postgres
DB_HOST=postgres-cluster-rw.cnpg-system.svc.cluster.local
DB_PORT=5432
DB_NAME=tripkit
DB_USER=tripkit
DB_PASSWORD=<secret>
```

### ⚠️ Piège critique : types SQL

**NE JAMAIS mettre `gorm:"type:blob"` ou autre type spécifique SQLite dans les modèles !**

GORM gère automatiquement le mapping :
- `[]byte` → `bytea` (PostgreSQL) / `blob` (SQLite)
- `string` → `text` (les deux)
- `int64` → `bigint` (les deux)

Si tu forces un type (`gorm:"type:blob"`), ça marchera en dev (SQLite) mais **crashera en prod** (PostgreSQL) car `blob` n'existe pas en PostgreSQL. Le pod fail au boot sur AutoMigrate, K8s fait un rollback silencieux, et tu restes sur l'ancienne version sans erreur visible.

**Règle :** Pour les champs `[]byte`, laisser GORM choisir :
```go
Data []byte `gorm:"" json:"-"`   // ✅ GORM choisit bytea/blob selon le driver
Data []byte `gorm:"type:blob"`    // ❌ CRASH en PostgreSQL
```

### PVC et persistance

| Quoi | Où | Persistant ? |
|------|----|--------------|
| DB PostgreSQL | CNPG cluster (PVC géré par CNPG) | ✅ Oui |
| Assets (images) | Table `assets` dans PostgreSQL (BLOB/bytea) | ✅ Oui |
| `/data/` (PVC tripkit-data) | Monté mais **NON fiable** pour les fichiers | ⚠️ Ne PAS compter dessus |

**Leçon mai 2026 :** Les assets stockés dans `/data/assets/` (filesystem) disparaissaient à chaque redeploy. Fix : tout migré en DB (table `assets`, colonne `data bytea`).

### Backup screenshots local

Les screenshots Google Maps sont aussi sauvegardés dans :
```
/home/node/projects/tripkit/route-screenshots/
├── langon-2026/       (10 fichiers)
├── canada-ontario-2026/ (8 fichiers)
├── canada-2026/       (14 fichiers)
└── usa-2026/          (11 fichiers)
```

Si la DB perd les assets, re-upload depuis ce dossier :
```bash
for f in /home/node/projects/tripkit/route-screenshots/langon-2026/*.jpg; do
  fn=$(basename "$f")
  curl -X PUT "http://tripkit-backend...:3001/api/trips/langon-2026/assets/$fn" \
    -H "Content-Type: image/jpeg" --data-binary @"$f"
done
```

---

## k3s
See [k3s/README.md](k3s/README.md)

## Frontend
Separate repo: [rjullien/tripkit-frontend](https://github.com/rjullien/tripkit-frontend)

## ⚠️ Ownership

**Ce repo est géré par Léa (agent IA de René).** Pas besoin de Baptiste pour le backend — Léa a accès complet au repo, peut coder, tester, pusher et releaser. Baptiste gère uniquement l'infra K3s/ArgoCD (`BaptTF/vps-infra`) qui déploie automatiquement les images Docker.

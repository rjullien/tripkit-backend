# TripKit Backend

API REST en Go pour TripKit — gestion de voyages, jours, hébergements, listes avec sync multi-device.

## Stack
- **Go 1.24** + chi router
- **GORM** — **PostgreSQL (CloudNativePG)** en prod (`BaptTF/vps-infra`) ; SQLite en local / tests CI
- Tests avec DB in-memory isolée
- **Docker** — image Alpine légère (~15 Mo), `CGO_ENABLED=0` (driver Postgres only)

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
| `GET` | `/api/trips/:id/days/:num/brief` | Daily Brief preview (`?skipConfig=1` admin) |
| `POST` | `/api/trips/:id/days/:num/brief/send` | Send (`force`, body `{"to":"<phone>"}` admin override) |
| `GET/PUT/DELETE` | `/api/trips/:id/assets[/:file]` | Assets CRUD (map images) |
| `GET/PUT` | `/api/trips/:id/hotels[/:num]` | Hotels CRUD (upsert) |
| `GET/PUT/DELETE` | `/api/trips/:id/lists[/:lid]` | Lists CRUD |
| `PATCH` | `/api/trips/:id/lists/:lid/sync` | Multi-device sync |

## Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `PORT` | No | `3001` | HTTP listen port |
| `BASE_PATH` | No | — (root) | Route prefix, ex. `/api` → `GET /api/trips` |
| `DB_PATH` | No | `data/tripkit.db` | SQLite database path |
| `TRIPKIT_API_TOKEN` | Prod | — | Bearer token **admin** (accès total). Unset = dev mode |
| `TRIPKIT_JWT_SECRET` | Prod | dev fallback | Clé HMAC des liens magiques. Fatal si absent et `TRIPKIT_ENV` ≠ `dev` |
| `TRIPKIT_SERVICE_TOKENS` | No | — | Tokens machine non-admin : `user1:token1,user2:token2` (token ≥ 16 car., username admin refusé) |
| `TRIPKIT_ACL_MODE` | Prod | — | `strict` = ACL fail-closed, `open` = comportement historique. Non défini → suit `TRIPKIT_ENV` |
| `TRIPKIT_ENV` | No | — | `dev` = tolérant ; toute autre valeur non vide = strict par défaut |
| `TRIPKIT_ADMIN_USERS` | No | `admin,rene` | Usernames qui bypassent le ACL trips |
| `TRIPKIT_REQUIRE_USER` | No | `false` | `true` = refuse les requêtes sans `Remote-User` |
| `TRIPKIT_CORS_ORIGINS` | No | `*` | Origines CORS autorisées, séparées par `,` |
| `TRIPKIT_NO_CACHE` | No | — | Non vide = désactive le cache météo |
| `APP_VERSION` | No | `dev` | Version renvoyée par `/health` |
| `TRIPKIT_GITHUB_TOKEN` | Prod (publish) | — | Fine-grained PAT / App token, Contents:read on **`rjullien/tripkit`** (trust file) + seed repos (`github-token` in Infisical → Secret `tripkit-secrets`) |
| `TRIPKIT_PUBLISH_SOURCES` | Dev / emergency | — | Optional JSON override. Prod = fetch `rjullien/tripkit/ops/publish-sources.json` via PAT, copy to disk; GH down → last cache → dogfood. Not trip allowlist (`publish-manifest.json`). |
| `TRIPKIT_PUBLISH_SOURCES_CACHE` | No | `$TMPDIR/tripkit-publish-sources.json` | Disk copy of last good trust JSON |
| `TRIPKIT_PUBLISH_SOURCES_TTL` | No | `2m` | How often to re-fetch trust JSON from GitHub |
| `TRIPKIT_PUBLISH_WORKER` | No | auto when `TRIPKIT_GITHUB_TOKEN` set | `1`/`0` to force worker on/off |
| `TRIPKIT_PUBLISH_ALLOW_REGISTRY_SEEDS` | Dev | off | `1` = allow `Source.Seeds` fallback when manifest fetch fails |
| `TRIPKIT_HERMES_BASE_URL` | No | `http://hermes-leo.openclaw.svc.cluster.local:8642` | Hermes-Léo API (cluster) |
| `TRIPKIT_HERMES_API_KEY` | For `/leo/*` | — | Same logical key as Hermes `API_SERVER_KEY` (Infisical → Secret `tripkit-hermes-key`) |
| `TRIPKIT_LEO_DASHBOARD_URL` | No | `https://hermes-leo.bapttf.com` | Public dashboard link for FE fallback |
| `TRIPKIT_LEO_TELEGRAM_URL` | No | — | Optional `https://t.me/…` deep-link for FE fallback |
| `TRIPKIT_BIFROST_API_KEY` | If Bifrost requires auth | — | Bearer for Daily Brief format, Plus chat **and** construction synthesis (nuisance recommendations, admin/health summaries). Not Hermes. |
| `TRIPKIT_DAILY_BRIEF_JSON` | Emergency | — | Raw override of `ops/daily-brief.json` |
| `TRIPKIT_DAILY_BRIEF_CACHE` | No | `$TMPDIR/tripkit-daily-brief.json` | Disk cache for Daily Brief ops JSON |
| `TRIPKIT_NOMINATIM_URL` | No | `https://nominatim.openstreetmap.org/search` | Géocodeur des adresses d'hôtel (nuisances). Pointer une instance dédiée évite la limite d'1 req/s du service public |
| `TRIPKIT_NOMINATIM_EMAIL` | No | — | Contact demandé par la politique d'usage de Nominatim public |

Daily Brief SoT (URLs + model + `adminPhone`): private `rjullien/tripkit/ops/daily-brief.json` via `TRIPKIT_GITHUB_TOKEN` (same pattern as Publish). GoWA only — no HA. Format via Bifrost — no Hermes. This public repo must not contain phone numbers, WhatsApp JIDs, or Tailscale MagicDNS hostnames — those stay in private ops/seeds.

### Mode Construction

Loader `ops/construction.json` (même discipline que Discovery / Daily Brief :
env JSON → GitHub → cache disque → dogfood, TTL 2 min). `Origin` est loggée au
boot. Un Completer Bifrost par check (`adminCheck` / `healthCheck` / `nuisance`)
via `ModelFor` ; si `enabled=false` ou `bifrostBaseUrl` vide, les checks
retombent sur la sortie déterministe (pas de `summary` / reco LLM).

Secret : `TRIPKIT_BIFROST_API_KEY`. Override d'urgence :
`TRIPKIT_CONSTRUCTION_JSON`. Cache : `TRIPKIT_CONSTRUCTION_CACHE` (défaut
`$TMPDIR/tripkit-construction.json`).

Toujours compilés en dur (lot 5.3 / seuils QA) : plage de phases
(`internal/construction/phase.go`), catégories nuisances et cache Overpass.

**Point de mesure des nuisances.** Un hôtel avec une adresse (`hotels[].addr`
ou `lat`/`lon`) est analysé **à ce bâtiment**, qu'il soit `candidate`,
`to_book` ou `booked` — en Construction on compare des alternatives *avant*
de réserver (SPEC §6, TASKS 5.5). Comme un hôtel de seed n'a en général
aucune coordonnée (`DATA-MODEL.md` : « no geo in days, geo only in
`locations{}` »), l'adresse est géocodée via Nominatim (`internal/geocode`,
1 req/s imposé côté client, cache 30 j). Un `lat`/`lon` posé directement
sur l'hôtel court-circuite le géocodage.

Sans adresse : recherche Nominatim sur « nom, ville », résultat affiché
comme `addressSource: guessed` (l'utilisateur peut dire que ce n'est pas
le bon bâtiment). Si rien ne sort : `addressSource: missing`, pas
d'analyse Overpass au centre-ville, le résultat **réclame**
`hotels[].addr`. Nom + adresse n'impliquent pas `booked`.

**Overpass (Discovery + nuisances).** Réglé dans `ops/discovery-themes.json`,
clé `overpass` : `baseUrl`, `mirrors` (défaut : `overpass.kumi.systems` puis
`overpass.openstreetmap.fr` — les deux vérifiés planétaires ; `["none"]` pour
désactiver), `timeoutSec` (défaut 90, plafond 180), `maxAttempts` (défaut 3,
plafond 6), `retryBackoffMs`, `concurrency` (défaut **2** : l'API publique
n'alloue que ~2 slots par IP et le backend n'a qu'une IP de sortie). Une
tentative = un endpoint, en rotation ; un `Retry-After` est respecté ; seul un
HTTP 400 (requête fautive) n'est pas réessayé. Un 200 porteur d'un `remark`
d'erreur Overpass est traité comme un échec, jamais comme « aucun élément ».

Le check nuisances interroge Overpass **séquentiellement** (`concurrency = 1`),
requêtes espacées de 800 ms à l'échelle du service, budget 150 s par catégorie
et 8 min par job (sous le `jobRunTimeout` de 10 min de `leo`). Un `progress` est
réémis toutes les 8 s pendant une requête longue (contrat
`.cursor/skills/tripkit-llm-jobs` : ≤ 10 s). Chaque lieu est
enregistré dès qu'il est analysé : le FE lit le job en asynchrone (SSE
`progress`/`delta` puis `GET .../nuisance-check`), donc un job interrompu laisse
des résultats partiels lisibles au lieu de tout perdre. Une catégorie non
récupérée reste `INDETERMINE` ⚪ avec la raison (`Overpass saturé…`, `délai
Overpass dépassé`), jamais un 🟢 rassurant.
Le gate de phase évalue la QA **pour la phase cible** ; un 🔴 refuse avec
`409 {error:"transition_blocked", blockers:[…]}` sauf `?force=1` admin.
Un verdict nuisances `ELEVE` non traité bloque Ph3 → Ph4.

### Léo / Hermes endpoints

| Method | Path | Status | Notes |
| --- | --- | --- | --- |
| `GET` | `/leo/status` | **used** | Ready flag + dashboard/telegram URLs (no secrets) |
| `POST` | `/leo/chat/stream` | **used (Plus UI)** | Creates a detached Léo job and SSE-subscribes. Live `delta` / `tool` / `done` while the app is open (same UX as before). First event is `meta` (`jobId`, `seq`). Disconnect does **not** cancel Hermes. Keepalives every 15s. |
| `GET` | `/leo/jobs/{jobId}/stream?after=N` | **used (Plus UI)** | Catch-up + live subscribe after lock-phone / dropped SSE. Same event types. |
| `POST` | `/leo/jobs/{jobId}/cancel` | **used (Plus UI)** | Explicit cancel only (Annuler). Stops Hermes. |
| `POST` | `/leo/chat` | **deprecated / unused by FE** | Sync JSON chat. **Do not remove** — useful for curl/debug. Same prompt + ACL as stream. |

Both chat paths call `leo.prepareMessages` → `leo.SystemPrompt` (Authelia user, allowlisted seed repos). Trip-related Q&A (resto, météo, idées…) is in scope; refuse only off-topic / other repos / secrets. Never trust the browser for scope.

Jobs are **in-memory** on the API pod (single replica). Reconnect must hit the same process. TTL 15 min after finish. Plus Assistant (`/plus/chat/stream`) is unchanged — still request-scoped SSE.

### Plus Assistant (Bifrost direct)

| Method | Path | Status | Notes |
| --- | --- | --- | --- |
| `GET` | `/plus/chat/status` | **used** | Ready + model from `ops/plus-chat.json` |
| `POST` | `/plus/chat/stream` | **used (Plus UI)** | SSE → Bifrost `stream:true` (no tools). Model SoT git. |

Config SoT: private `rjullien/tripkit/ops/plus-chat.json` (same Loader pattern as Daily Brief). Optional `TRIPKIT_BIFROST_API_KEY`.

### Polarsteps caption (Plus box)

Text-only journal draft. No photos, no Polarsteps API, **no GoWA**.

| Method | Path | Status | Notes |
| --- | --- | --- | --- |
| `GET` | `/trips/{tripId}/polarsteps/status` | **used (Plus UI)** | `enabled` / `ready` — seed + ops + window (trip + 5 days after `endDate`) |
| `GET` | `/trips/{tripId}/polarsteps/caption` | **used (Plus UI)** | Last saved step for the local day |
| `POST` | `/trips/{tripId}/polarsteps/caption` | **used (Plus UI)** | ExtractDay → Bifrost flash → QA. Appends a step (anti-redite vs history). `422` if QA FAILED |

History: every successful generate is a new `polarsteps_steps` row (several per day). Prompt + QA refuse repeats. Rows are **purged 5 days after `endDate`**.

Config SoT: private `rjullien/tripkit/ops/polarsteps-caption.json`. Seed gate: `trip.polarsteps.enabled`. Optional `TRIPKIT_BIFROST_API_KEY` (same as Plus chat).

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
| `push main` | test + e2e + fixtures-cross-repo | Tests seulement, pas de build Docker |
| `pull_request` | test + e2e + fixtures-cross-repo | Tests seulement |
| `release published` | test + e2e + **build-and-push** | Image Docker → ghcr.io |

Le job **`fixtures-cross-repo`** compare les fixtures de contrat de
`internal/handlers/testdata/contract/` à leur copie dans `tripkit-frontend`, qu'il clone à côté de ce
dépôt. La ref frontend comparée est résolue ainsi :

1. la ref donnée en `workflow_dispatch` (`frontend_ref`) si elle est fournie — comparaison **stricte** ;
2. sinon la branche de `tripkit-frontend` **portant le même nom** que la branche de tête ici (une
   modification de fixtures voyage en paire de branches homonymes, une par dépôt) — **stricte** ;
3. sinon la branche par défaut du frontend, en mode **non strict** : les fixtures présentes sont
   quand même comparées octet à octet, absentes le garde fait `SKIP` au lieu de faire échouer une PR
   qui n'a pas de pendant frontend.

Le cas 2 est celui pour lequel le job existe ; le cas 3 évite un job rouge en permanence. Le job a
besoin du secret `CROSS_REPO_TOKEN` si `tripkit-frontend` est privé (`GITHUB_TOKEN` ne couvre que ce
dépôt) : sans lui, la résolution retombe sur le cas 3. Il n'est **volontairement pas** dans le
`needs:` de la release.

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
5. **ArgoCD sync ?** → Check ArgoCD UI for sync status

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
| Admin token | `Authorization: Bearer <TRIPKIT_API_TOKEN>` | Full access (bypass ACL) |
| Service token | `Authorization: Bearer <token de TRIPKIT_SERVICE_TOKENS>` | user = le username configuré, role = `service` (**non-admin**, soumis au ACL groupes) |
| JWT (magic link) | `Authorization: Bearer <jwt>` | Scopé par trip_id |
| Authelia | Header `Remote-User` (sans Bearer) | role = viewer, soumis au ACL groupes |
| Dev mode (`TRIPKIT_ACL_MODE` ≠ `strict`) | Aucun de `TRIPKIT_API_TOKEN` / `TRIPKIT_JWT_SECRET` / `TRIPKIT_SERVICE_TOKENS` | Tout le monde = admin ⚠️ |
| Dev mode en `strict` | idem, mais `TRIPKIT_ACL_MODE=strict` | Plus de bypass admin : `Remote-User` → viewer, sinon **401** |

L'identité prouvée par un token gagne toujours sur le header `Remote-User` : un client
qui n'entre pas par le forwardAuth Authelia peut envoyer ce header lui-même.

### Mode strict (`TRIPKIT_ACL_MODE=strict`)

| Situation | Mode `open` (historique) | Mode `strict` |
|-----------|--------------------------|---------------|
| Table `trip_accesses` vide | tout ouvert à tous | non-admin bloqué (403) |
| Trip sans règle d'accès | ouvert à tous | 403 |
| Utilisateur sans aucun groupe | voit tous les trips | ne voit rien (`[]`) |
| `POST /trips` | libre | id explicite obligatoire **et** déjà autorisé pour l'appelant |
| Aucune variable d'auth configurée | tout le monde = admin | 401 (ou viewer si `Remote-User`) |

### Onboarding d'un contributeur externe (seed automatique)

Objectif : quelqu'un d'extérieur pousse son propre seed dans **son** dépôt et sa CI
l'importe, sans jamais pouvoir lire ni écrire les autres trips.

```bash
# 1. Créer son groupe et son (ses) trip(s) — avec une identité ADMIN
curl -X PUT https://tripkit.example.com/api/groups/nadia \
  -H "Authorization: Bearer $TRIPKIT_API_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Nadia","members":["nadia"],"trips":["nadia-2026"]}'

# 2. Générer son token de service
openssl rand -hex 32

# 3. L'ajouter à TRIPKIT_SERVICE_TOKENS (secret du backend), puis redéployer
#    TRIPKIT_SERVICE_TOKENS="nadia:<token>,autre:<token2>"

# 4. Donner le token à sa CI comme secret. Elle l'envoie tel quel :
#    Authorization: Bearer <token>   (aucun header Remote-User nécessaire)
```

Garanties côté serveur (aucune confiance requise envers sa CI) :

- le **group id** et la **liste de trips** sont fixés par l'admin ; le contributeur ne
  peut pas élargir son périmètre : `GET /api/groups` et `PUT /api/groups/{id}` → 403 ;
- un username présent dans `TRIPKIT_ADMIN_USERS` est **ignoré** dans
  `TRIPKIT_SERVICE_TOKENS` (un token de service ne peut jamais être admin), idem pour
  un token de moins de 16 caractères ;
- `Remote-User` forgé → ignoré, l'identité reste celle du token ;
- toutes les routes d'un autre trip (`GET`/`PUT` trip, days, hotels, lists, assets) → 403,
  et `POST /trips` avec un id non autorisé → 403.

Le token couvre toute la séquence d'import : `GET /trips/{id}`, `POST /trips` ou
`PUT /trips/{id}`, puis `PUT /trips/{id}/days/{n}`, `/hotels/{n}`, `/lists/{listId}`
et `/assets/{filename}`.

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


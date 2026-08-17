# Architecture — Services centralisés (anti-duplication)

> **Règle :** chaque intégration externe a UN SEUL package/client.
> Jamais d'appel HTTP direct à un service tiers depuis un handler ou un pipeline.
> Passe TOUJOURS par le service dédié.

---

## Services centralisés existants

### 🌤️ Météo — `internal/weather/`

| Besoin | Utilise |
|--------|---------|
| Prévisions (daily brief, plus chat, handler, frontend) | `weather.Service` |
| Routing pays (US→NWS, CA→MSC, default→Open-Meteo) | Automatique dans le service |

**INTERDIT :** appeler `api.open-meteo.com`, `api.weather.gov`, ou `api.weather.gc.ca` directement.
**Frontend :** doit appeler `GET /weather/forecast` (ou `GET /trips/{id}/weather`), jamais Open-Meteo en direct sauf fallback offline/hourly modal.

---

### 📱 WhatsApp (GoWA) — `internal/dailybrief/gowa.go`

| Besoin | Utilise |
|--------|---------|
| Envoyer un message WhatsApp | `GowaClient.Send(phone, message)` |

Le client GoWA est instancié dans le daily brief pipeline uniquement.
**INTERDIT :** appeler l'API GoWA depuis un autre package. Si un autre service doit envoyer un message WhatsApp, il passe par le daily brief ou on extrait `GowaClient` dans un package `internal/messaging/`.

Config : `ops/daily-brief.json` → `gowaBaseUrl`. Pas de PII dans le code (phones dans ops privé).

---

### 🤖 Bifrost (LLM) — `internal/bifrost/`

| Besoin | Utilise |
|--------|---------|
| Complétion LLM (format brief, admin check, health check, nuisance reco) | `bifrost.Completer` interface |
| Instanciation dynamique (model from ops) | `func() bifrost.Completer` (BifrostFn pattern) |

**Pattern obligatoire :** ne jamais stocker un Completer statique. Utiliser une `func() bifrost.Completer` pour que les changements de modèle dans `ops/construction.json` prennent effet dans le TTL du Loader (2 min). Le `newCompleter(feature)` dans `main.go` est le point d'entrée.

**INTERDIT :** instancier un `bifrost.NewClient` ailleurs que dans `main.go` ou un test.

---

### 🗺️ Overpass (OSM) — `internal/discovery/overpass.go`

| Besoin | Utilise |
|--------|---------|
| Recherche POI géo (discovery, nuisances) | `discovery.Client` (via `Querier` interface) |
| Config (mirrors, timeout, concurrency) | `ops/discovery-themes.json` → `overpass{}` |

Un seul `discovery.Client` instancié dans `main.go`, partagé entre discovery et nuisance via l'interface `Querier`.

**INTERDIT :** créer un deuxième client Overpass ou appeler l'API directement.
**Concurrency :** max 2 (l'API publique n'alloue que ~2 slots/IP). Ne jamais augmenter sans changer d'endpoint.

---

### 📍 Géocodage (Nominatim) — `internal/geocode/`

| Besoin | Utilise |
|--------|---------|
| Résoudre une adresse → lat/lon (hôtels pour nuisances) | `geocode.Client` |

Un seul client instancié dans `main.go`, passé au service nuisance.
Rate limit : 1 req/s (public Nominatim). Cache 30 jours en DB.

**INTERDIT :** appeler Nominatim depuis un autre package.

---

### 🐙 GitHub (Contents API) — `internal/publish/github.go`

| Besoin | Utilise |
|--------|---------|
| Lire un fichier (ops JSON, seed) | `GitHubClient.GetContents()` |
| Écrire dans un seed (writeback phase/retain/pin) | `GitHubClient.PutContents()` |
| Télécharger un zipball (publish) | `GitHubClient.FetchZip()` |

Un seul `GitHubClient` dans `main.go`, partagé entre publish, construction ops loader, seedgit, discovery loader, daily brief loader.

**INTERDIT :** créer un deuxième client GitHub ou utiliser un token différent.

---

### 🦁 Léo / Hermes — `internal/leo/`

| Besoin | Utilise |
|--------|---------|
| Chat Léo (stream SSE) | `leo.Hub` + handler `/leo/chat/stream` |
| Jobs asynchrones (nuisance, profile-edit) | `leo.Hub.StartJob()` |

**INTERDIT :** appeler Hermes directement depuis un autre package. Toujours passer par le Hub.

---

## Règles pour le frontend

| Service | Backend endpoint | Appel direct autorisé ? |
|---------|-----------------|------------------------|
| Météo | `GET /weather/forecast` | ❌ (sauf fallback offline + modal hourly) |
| NWS / MSC | — | ❌ Jamais — le backend route |
| Léo | `POST /leo/chat/stream` | ❌ |
| Plus Chat | `POST /plus/chat/stream` | ❌ |
| Discovery | `POST /trips/{id}/discovery/search` | ❌ |

Le frontend ne doit **jamais** appeler une API tierce directement sauf :
- Open-Meteo **uniquement** comme fallback quand le backend est injoignable (offline graceful degradation)
- Open-Meteo pour les données horaires du modal météo (pas encore exposé par le backend)

---

## Pattern pour ajouter un nouveau service externe

1. Créer `internal/<service>/` avec un `Client` ou `Service` struct
2. Implémenter une interface (pour testabilité)
3. Instancier dans `main.go` (un seul endroit)
4. Passer aux services consommateurs via injection (champ struct ou `func()` pour lazy)
5. Si le frontend en a besoin : exposer un endpoint handler, pas un appel direct
6. Documenter dans ce fichier

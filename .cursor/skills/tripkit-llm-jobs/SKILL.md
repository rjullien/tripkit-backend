---
name: tripkit-llm-jobs
description: "Règle dure TripKit : toute action UI qui appelle un LLM (Bifrost / Hermes / Léo) est un job leo.Hub (POST 202 + persist + SSE / GET store). Jamais un POST synchrone qui attend le modèle."
---

# TripKit — jobs LLM (iPhone / Safari)

**Ne jamais** faire attendre Safari sur Bifrost, Hermes ou n’importe quel LLM.

iPhone lock + proxy idle tuent un POST silencieux vers ~60 s (HTML 502 Traefik,
204 octets). Polarsteps 2026-08-15 : Bifrost 90–113 s, iPhone 502 à 60,7 s.
Monter les timeouts (90 / 180 / 240 / 270) est un pansement, pas le contrat.

## Pattern obligatoire (Léo Plus, Discovery, Polarsteps, Construction)

1. `POST` → **202 `{jobId}`** tout de suite (timeout FE **15 s**).
2. `leo.Hub.Start` lance le travail **hors** requête HTTP (`internal/leo/job.go` :
   in-memory, 1 réplique, TTL 15 min, run 10 min).
3. **Persister** le résultat en BE (table / cache). `GET` = store.
4. SSE `GET /leo/jobs/{id}/stream` : `progress` **toutes les ≤ 10 s** (nginx
   `/api/` 270 s ne doit pas idle-out), puis `done` / `error`.
5. FE : `API.leoJobStream`, `sessionStorage` job+seq, `resumeIfNeeded` sur
   visibilité / Plus (à côté de Léo).
6. Si le SSE tombe : relire le **GET store**. Le job continue en BE.
7. QA fail = `event: error` `code: qa_failed`, **pas** de texte copiable.

```
FE  POST /…          → 202 {jobId}     (15 s max)
BE  leo.Hub          → Bifrost/Hermes  (détaché)
BE  persist
FE  GET /leo/jobs/…/stream
FE  GET /…/result    → store si lock
```

## Interdit

- POST synchrone qui bloque sur `Completer.Complete` / Bifrost dans le handler HTTP
- Compter sur Traefik 600 s ou nginx 270 s comme contrat produit
- Un hop LLM de plus pour « raccourcir le prompt » (ça n’accélère pas la file)

## Références

- `internal/leo/job.go`
- `internal/handlers/discovery.go`
- `internal/handlers/polarsteps.go`
- Spec : `rjullien/tripkit` `DESIGN-leo-jobs.md`

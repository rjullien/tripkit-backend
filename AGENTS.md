# AGENTS.md — tripkit-backend

## Services centralisés

**Référence : [`../tripkit/SERVICES.md`](https://github.com/rjullien/tripkit/blob/main/SERVICES.md)**

Avant d'ajouter un appel HTTP vers un service externe (météo, WhatsApp, LLM,
géocodage, Overpass), vérifier qu'un service centralisé existe déjà dans le
backend. Ne jamais dupliquer — injecter via `cmd/api/main.go`.

## LLM / Safari

Toute action UI qui appelle un LLM (Léo, Discovery, Polarsteps, Construction)
suit le skill **tripkit-llm-jobs** (`.cursor/skills/tripkit-llm-jobs/SKILL.md`) :

- Handler : `POST` → **202 `{jobId}`** via `leo.Hub.Start`. Le LLM ne tourne
  **pas** sur la goroutine HTTP.
- Persister le résultat. `GET` = store que la UI relit après un lock Safari.
- SSE : `progress` toutes les ≤ 10 s, puis `done` / `error`.
- QA fail : `event: error` `qa_failed`, pas de texte copiable.

Ne pas « corriger » un 502 iPhone en montant le timeout Bifrost. Références :
`internal/handlers/discovery.go`, `internal/handlers/polarsteps.go`.

# Cross-repo contract fixtures (construction mode)

Golden response bodies for the construction check endpoints, captured from the
real HTTP handlers against a deterministic in-memory SQLite database:

| File | Endpoint | Pins |
| --- | --- | --- |
| `qa-violations.json` | `POST /trips/{tripId}/construction/qa` | `{violations, phase, count}`, one red + one yellow violation |
| `admin-check.json` | `POST /trips/{tripId}/admin-check` | `{verdict, countries, items}`, `appliesTo` camelCase, and the FR+US bi-national case producing **no** ESTA item |
| `health-check.json` | `POST /trips/{tripId}/health-check` | `{verdict, countries, items}` with items |
| `phase-transition-blocked.json` | `PUT /trips/{tripId}/construction/phase` | the 409 body `{error:"transition_blocked", blockers:[QAViolation]}` |
| `nuisance-check.json` | `GET /trips/{tripId}/nuisance-check` | `{results:[...]}`, the `INDETERMINE` level with `unavailable:true` for a category whose Overpass query failed, one scored `MODERE` category, and the self-describing `incomplete` / `failedCategories` fields |

The nuisance fixture is produced by `TestContractFixtures_Nuisance`, which runs
the analysis through the service with a stubbed Overpass querier (the trains
query fails, nightlife returns three bars) and a fixed clock, so `analyzedAt` and
the computed distances are stable. `INDETERMINE` is a deliberate extension of
`SPEC-nuisance-check.md`, which defines only `ELEVE`/`MODERE`/`FAIBLE`: verdict
precedence is `ELEVE > INDETERMINE > MODERE > FAIBLE` so a failed query can never
surface as a reassuring green verdict.

`tripkit-frontend/tests/fixtures/construction-contract/` holds a
**byte-identical copy** of every `.json` file in this directory. It is consumed
by `tripkit-frontend/tests/construction-contract.test.cjs`, which asserts that
the frontend renderers read the keys the backend actually sends. Both copies must
be updated together: that is the whole point of the fixtures, an envelope change
has to fail a test on both sides instead of silently rendering an empty state.

## Regenerating

```sh
cd tripkit-backend
go test ./internal/handlers/ -run TestContractFixtures -update
cp internal/handlers/testdata/contract/*.json \
   ../tripkit-frontend/tests/fixtures/construction-contract/
```

The `-update` flag is defined by `internal/handlers/contract_fixtures_test.go`,
so it only works when the test target is that package (not `./internal/...`).
Review the diff before committing: a change here is a public API change.

## Rules for new fields

Any field added to these payloads later (for example an LLM-generated `summary`)
must carry `omitempty` so it is absent from the fixtures whenever the optional
dependency is not configured (nil Bifrost completer in tests). Otherwise the
golden files start depending on the environment they were captured in.

Bodies are re-serialized with sorted keys and two-space indentation, so the files
stay diffable and the comparison is insensitive to handler-side key ordering.
JSON has no comment syntax and the files must stay byte-identical across the two
repos, which is why this README carries the cross-repo pointer instead of a
header inside each fixture.

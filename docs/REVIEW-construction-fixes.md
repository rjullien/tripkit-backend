# Review « Mode Construction » (#61) — statut des corrections (backend)

**Périmètre** : les 20 constats de la review croisée backend #61 / frontend #74, côté backend.
**Branche** : `feat/construction-mode` (#61).
**Pendant frontend** : `tripkit-frontend/docs/REVIEW-construction-fixes.md`.
**Verdict de départ** : NEEDS_CHANGES.

Légende : ✅ **corrigé** · 🟡 **partiellement corrigé** · ⏸️ **différé** (non implémenté, tracé).

> ⚠️ Toutes les vérifications ci-dessous sont **locales** : `go build ./...`, `go vet ./...`,
> `go test ./internal/...` (14 paquets OK, `internal/database` sans test). **Rien n'a été validé
> contre une instance qui tourne** : le DNS interne du cluster est injoignable, la prod est derrière
> Authelia, et l'API publique Overpass n'est pas accessible depuis l'environnement de travail.
> La section « Reste à faire » distingue explicitement ce qui est prouvé de ce qui ne l'est pas.

---

## 1. Constats côté backend

| # | Constat | Statut | Fichiers touchés |
|---|---|---|---|
| 1-4 | Enveloppes de réponse QA / admin-check / health-check + panneaux « tout va bien » | ✅ (côté contrat) | Les enveloppes backend (`{violations,phase,count}`, `{verdict,countries,items}`, `{results}`) ont été retenues comme **autorité** et n'ont pas changé ; c'est le frontend qui s'y aligne. Le contrat est désormais gelé par des fixtures : `internal/handlers/testdata/contract/*.json`, `internal/handlers/contract_fixtures_test.go` |
| 5 | Endpoints stub qui répondent 202 sans rien écrire | 🟡 | `internal/handlers/construction.go` : `RetainDiscoveryItem`, `PinNuisanceToSeed`, `CreateProfileRequest` répondent **501** `{"error":"not_implemented","detail":"…"}`, ne démarrent plus de job Léo et n'insèrent plus de ligne `construction_profile_requests` bloquée en `running`. Honnête, mais **la fonctionnalité n'est pas implémentée** (voir ⏸️ « write-back Léo » plus bas). Tests : `construction_test.go`, `construction_http_test.go` |
| 8 | `?force=1` non réservé aux admins | ✅ | `internal/handlers/construction.go` : 403 `{"error":"admin_required"}` si `!(config.IsAdmin(user) || isRequestAdmin(r))`, avant tout appel au service. Tests : `TestTransitionPhase_Force_NonAdminForbidden`, `TestTransitionPhase_Force_AdminSucceeds` |
| 9 | Nuisances : un échec Overpass devient un verdict vert 🟢 | ✅ | `internal/nuisance/scoring.go` (`LevelIndetermine`, `CategoryResult.Unavailable`, `UnavailableCategory`, précédence `ELEVE > INDETERMINE > MODERE > FAIBLE`, emoji ⚪), `internal/nuisance/service.go` (`CheckResult.Incomplete`, `FailedCategories`). Une catégorie en échec n'est plus scorée à zéro item |
| 10 | Aucun cache dans le chemin nuisances, concurrence 4 | 🟡 | `internal/nuisance/cache.go` (nouveau) : cache TTL 24 h réutilisant `models.DiscoveryCache` / table `construction_discovery` avec clé de scope `nuisance:<lat>,<lon>` (4 décimales) ; écriture **uniquement** en cas de succès (pas de cache négatif). Concurrence 4 → 2 (`internal/nuisance/service.go`). Prouvé par des `Querier` bouchonnés ; le comportement réel sous rate-limiting Overpass reste non vérifié |
| 12 | Blocages de transition sérialisés dans une chaîne d'erreur | ✅ | `internal/construction/errors.go` (nouveau, `TransitionBlockedError`), `internal/construction/service.go`, `internal/handlers/construction.go` : 409 `{"error":"transition_blocked","blockers":[QAViolation…]}`. Fixture : `phase-transition-blocked.json` |
| 13 | Log de phase écrit après le commit de la phase | ✅ | `internal/construction/service.go` + `internal/construction/state.go` : `WriteState` et l'insertion de `ConstructionPhaseLog` dans une seule transaction GORM. Test : `TestTransitionPhase_LogInsertFails_PhaseUnchanged` |
| 14 | Liste `allowed` de `ResolveMode` inerte | ✅ | `internal/leo/stream.go` (`PromptContext.AllowedModes`), `internal/leo/prompts.go` (`ClientSelectableModes()` remplace `AllModesList()`, `profile-edit` volontairement absent), `internal/handlers/leo.go`, `internal/handlers/leo_stream.go` |
| 15 | Prompt système contradictoire (écrire / ne pas écrire dans le seed) | ✅ | `internal/leo/client.go` (`writePolicy`, `basePromptWith`), `internal/leo/prompts.go` (`writePolicyFor`, suppression de la ligne « Ne crée pas encore de fichiers seed » de l'overlay ideation) |
| 16 | `transport_not_booked` ne se déclenche jamais sur un bloc absent | ✅ | `internal/construction/qa.go`. **Effet de bord assumé** : une journée sans bloc transport produit désormais légitimement une violation, donc les jeux de données de test « propres » doivent fournir un transport réservé (`qa_test.go` mis à jour) |
| 17 | Mots-clés d'intérêt multi-mots impossibles à matcher | ✅ | `internal/discovery/rank.go` : normalisation (minuscules + table de repli d'accents), tokenisation, formes plurielles légères, tous les tokens requis, **comparaison mot entier** et tokens d'un caractère ignorés. Test sur le vocabulaire réel de `tripkit-seeds/travel-profile.js` (`rank_test.go`). Les deux familles de pluriels en `-aux` sont candidates (`nationaux → national`, `châteaux → château`) — voir §5, constats 4 et 5. **Pas de stemmer** : les autres pluriels irréguliers ne sont pas traités |
| 18 | Synthèse Bifrost = code mort | 🟡 | `cmd/api/main.go` (`buildConstructionCompleter`) branche le completer sur `nuisance.Service.Bifrost` **et** `formalities.Service.Completer` ; `internal/formalities/service.go` appelle `FormatAdminResults`/`FormatHealthResults` et remplit `AdminCheckResult.Summary` / `HealthCheckResult.Summary` (`omitempty`) ; `internal/formalities/bifrost.go` borne l'appel à 10 s avec repli texte brut. Un WARNING de démarrage nomme les fonctionnalités désactivées si la config manque. **Réserve** : la config Bifrost est **empruntée à plus-chat** (`bifrostBaseUrl` + `chatModel` + `TRIPKIT_BIFROST_API_KEY`), faute de loader `construction.json` ; c'est pragmatique, pas architectural, et le modèle réel n'a jamais été appelé ici |
| 19 | Aucun loader `TRIPKIT_CONSTRUCTION_*` | ⏸️ | **Non implémenté, assumé.** `README.md` : section « Mode Construction: config is hardcoded in this release » (phases, seuils QA, catégories/seuils nuisances, TTL de cache, concurrence), `ops/construction.json` non consommé, endpoint/modèle Bifrost hérités de `ops/plus-chat.json`. NOTE correspondante dans `cmd/api/main.go`. Tracé comme **lot 0.3** dans `rjullien/tripkit` `construction/TASKS.md` |
| 20 | Régression d'accents dans la copie visible | ✅ | `internal/nuisance/scoring.go` (« Aucun élément détecté. », « N établissements détectés dans un rayon de 200m. », « … à N m/km. »), `internal/nuisance/service.go` (« Aucun lieu à analyser. », « Analyse terminée : N lieux analysés. »), `internal/nuisance/bifrost_synthesis.go` (prompt de synthèse), `internal/formalities/rules_health.go` (Hépatite, Typhoïde, Fièvre jaune, recommandées, préventif, séjour, régions, privilégier…), `internal/formalities/rules_admin.go` (Électronique, séjour, formalité), `internal/formalities/bifrost.go` (« Formalités administratives », « Conseils santé » et leurs replis). Fixtures régénérées, copie frontend resynchronisée |

### Constats hors backend

| # | Constat | Où |
|---|---|---|
| 6, 7, 11 | « Retenu ✓ » sur un no-op, succès sur échec SSE, phase 1 sautée | Frontend — voir `tripkit-frontend/docs/REVIEW-construction-fixes.md` |
| 1-4, 12 (rendu) | Lecture des enveloppes, erreur au lieu de l'état vide rassurant, badges des blocages | Frontend, même document |

---

## 2. Tests inter-dépôts ajoutés

La review notait que « rien dans les deux dépôts ne traverse la frontière ». C'est corrigé :

- `internal/handlers/testdata/contract/` : `qa-violations.json`, `admin-check.json`, `health-check.json`,
  `nuisance-check.json`, `phase-transition-blocked.json` + `README.md`.
- `internal/handlers/contract_fixtures_test.go` fait tourner les **vrais handlers chi** sur une base
  SQLite en mémoire et compare octet à octet.
- Régénération : `go test ./internal/handlers/ -run TestContractFixtures -update`
  (le flag `-update` est déclaré dans le paquet `handlers` : la cible doit être exactement
  `./internal/handlers/`, pas `./internal/...`).
- **Après toute régénération, recopier les fichiers dans**
  `tripkit-frontend/tests/fixtures/construction-contract/`, **`CHECKSUMS.txt` compris** : le test
  unitaire node du frontend les lit et exige l'égalité octet à octet. `diff`/`cmp` peuvent être
  absents de l'environnement, dans ce cas comparer avec `node -e` et `Buffer.equals`.
- La recopie n'est plus laissée à la mémoire de l'auteur (constat 2 de la review de suivi, §5) :
  `CHECKSUMS.txt` (manifeste sha256, format `sha256sum`) est committé des deux côtés et vérifié par
  `TestContractFixtures_Checksums` **et** par le test unitaire frontend, et
  `TestContractFixtures_FrontendCopyInSync` compare les deux répertoires octet à octet quand les deux
  dépôts sont clonés côte à côte (il `SKIP` sinon).

Déterminisme rendu nécessaire par les fixtures (et bénéfique pour l'API réelle, même si aucune spec
ne l'exigeait) : `DetectCountries` et `extractNationalities` trient désormais leurs résultats
(`internal/formalities/countries.go`, `internal/formalities/service.go`).

---

## 3. Reste à faire

### Vérifiable seulement contre une instance qui tourne

- Comportement réel d'Overpass sous rate-limiting (429/504) et taux de succès effectif du cache 24 h :
  ici, uniquement des `Querier` bouchonnés (erreur forcée, appels comptés).
- Que l'endpoint Bifrost de plus-chat réponde effectivement aux deux nouveaux prompts de synthèse
  admin/santé, et la qualité du `summary` produit.
- Comportement Postgres de la nouvelle transaction phase + log, et de l'upsert `OnConflict` sur
  `construction_discovery` (seul SQLite a été exercé).
- Rendu réel des 501/403/409 dans le navigateur contre le backend déployé.

### Genuinement non implémenté

- **Write-back Léo** pour `retain-discovery-item`, `pin-nuisance-to-seed` et
  `travel-profile/request` : les trois endpoints répondent 501 et portent un `TODO(hermes)` nommant
  précisément ce qu'il faut brancher. L'encadrement du texte utilisateur par `<user_request>` n'est
  plus un simple commentaire : `leo.WrapUserRequest` existe et est testé
  (`TestWrapUserRequest`), et le `TODO` désigne ce helper comme passage obligé.
  `models.ConstructionProfileRequest` et sa migration sont conservés pour ce branchement.
- **Loader de config ops** `TRIPKIT_CONSTRUCTION_*` / `ops/construction.json` (lot 0.3) : phases,
  seuils QA et paramètres nuisances restent compilés en dur.
- **Synthèse nuisances** : `Synthesize` est branché mais `Recommendation`/`Alternatives` restent
  vides sans config Bifrost, et ces deux champs n'ont pas d'`omitempty` (antérieur à ces correctifs).
- **Messages des violations QA en anglais** (« Day 2 is missing (gap in day numbering) ») alors que
  l'UI est en français : hors périmètre du constat 20 (qui portait sur les accents), mais visible par
  l'utilisateur et à trancher.
- `internal/discovery/service.go` garde le motif « soft-fail puis mise en cache » signalé par la
  review : il est sur `main`, hors périmètre de cette PR (le chemin nuisances, lui, ne met plus rien
  en cache en cas d'échec).
- `internal/handlers/construction.go` n'est pas `gofmt`-propre (alignement préexistant d'un littéral
  de map, un des 20 fichiers non-gofmt du dépôt) : non reformaté pour ne pas noyer le diff.

---

## 4. Specs à mettre à jour (dépôt `rjullien/tripkit`)

Les specs de référence vivent dans **`rjullien/tripkit`** et **ce travail ne les a volontairement pas
modifiées** (dépôt en lecture seule pour cette tâche, édité en parallèle). Le même statut doit y être
reporté, en particulier dans **`construction/SPEC.md` §11** (cases à cocher des critères
d'acceptation) et **`construction/TASKS.md`** :

1. **`applies_to` → `appliesTo`** : `AdminCheckItem.AppliesTo` sérialise en camelCase comme tout le
   reste. À corriger dans `SPEC-admin-check.md`, `construction/DESIGN.md`, `construction/TASKS.md`,
   `construction/ANNEX-recovery.md`.
2. **Quatrième niveau de verdict `INDETERMINE` (⚪)** : `SPEC-nuisance-check.md` §4.1 ne définit que
   `ELEVE`/`MODERE`/`FAIBLE`. `INDETERMINE` prime sur `MODERE`, et les champs `unavailable`
   (par catégorie), `incomplete` et `failedCategories` (par lieu) sont à documenter.
3. **`appliesTo` porte des nationalités, pas des voyageurs** : la « checklist par voyageur » de
   `construction/SPEC.md` §7 est donc **reconstruite côté client** par intersection. Le suivi backend
   est fait (§5, constat 1) : `AppliesTo` ne porte plus que les nationalités qui déclenchent
   réellement la règle, et `["*"]` pour une règle universelle. À documenter comme tel.
4. **Loader de config ops différé** (lot 0.3) : acter que les phases et seuils sont en dur dans cette
   livraison et que la synthèse construction emprunte la config Bifrost de plus-chat.
5. **`retain` / `pin-nuisance` / `profile-edit` répondent 501** : le write-back Léo n'est pas câblé ;
   les critères d'acceptation correspondants ne peuvent pas être cochés.
6. **Le modèle de phases est borné, pas ordonné** : `construction/SPEC.md` §5 définit les phases 1 à 4
   plus Live. Le code accepte 0 (« pas encore démarrée ») à 5 et refuse le reste en `400` ; l'ordre
   n'est **pas** contraint (retours arrière et Ph3/Ph4 parallèles assumés par la spec).

---

## 5. Deuxième passe — review de suivi (verdict APPROVED, 12 constats non bloquants)

La review de l'implémentation ci-dessus a validé le travail et laissé 12 constats non bloquants.
Côté backend :

| # | Constat de suivi | Statut | Détail |
|---|---|---|---|
| 1 | Regroupement par voyageur décoratif : `AppliesTo` portait **toutes** les nationalités du voyage | ✅ | `internal/formalities/rules_admin.go` : nouveau `matchedNationalities()` = `rule.AppliesTo ∩ nationalités du voyage`, dans l'ordre (trié) de ces dernières. Une règle universelle garde `["*"]`, seul marqueur que le frontend rend comme « tous les voyageurs » ; la liste ne peut jamais être vide (une liste vide se relit « tout le monde » en aval). Fixture `admin-check.json` régénérée : l'eTA canadien passe de `["FR","US"]` à `["FR"]`. Tests : `TestMatchAdminRules_AppliesToOnlyTriggeringNationalities`, `…_AppliesToWildcardKept`, `…_AppliesToFreeMovementNarrowed`, `…_AppliesToNeverEmpty`, plus le test de contrat frontend |
| 2 | Synchronisation des fixtures non contrôlée | ✅ | `CHECKSUMS.txt` (manifeste sha256) committé dans les deux dépôts, vérifié par `TestContractFixtures_Checksums` et par le test unitaire frontend ; `TestContractFixtures_FrontendCopyInSync` compare les deux répertoires octet à octet quand les dépôts sont côte à côte (`SKIP` sinon). Voir §2 |
| 4 | `singularize` : tout pluriel en `-aux` devenait `-al` (`châteaux → chateal`) | ✅ | `internal/discovery/rank.go` : `matchForms()` renvoie les **deux** candidats (`-al` **et** `-au`) plus le mot tel quel, et un match sur l'un suffit. `TestMatchKeyword_PluralsAndWordBoundaries` couvre `châteaux`/`bateaux`, `parcs nationaux` et le sens inverse (`château` contre « Route des Châteaux ») |
| 5 | Tokens matchés par sous-chaîne : `long` dans `Longueuil`, token `d` de « musées d'art moderne » | ✅ | `internal/discovery/rank.go` : nom et thème sont découpés en mots, un token doit égaler un **mot entier** (aux formes plurielles près) ; les tokens d'un caractère sont écartés. Tests : `TestMatchKeyword_PluralsAndWordBoundaries`, `TestRankItems_NoFalsePositiveOnSubstring`. Tous les cas du vocabulaire réel restent verts |
| 6 | README : « les 6 phases, leur ordre et les garde-fous sont compilés en dur » — faux deux fois | ✅ | `README.md` réécrit : la **plage** (0 à 5, spec §5 = 1 à 4 + Live, 0 = pas démarrée) est en dur, l'**ordre ne l'est pas** (volontaire), et le garde-fou est `CanTransition` (QA de la phase cible). La validation manquante est ajoutée : `construction.ValidPhase()` + refus `400` dans `Service.TransitionPhase`. Tests : `TestValidPhase`, `TestTransitionPhase_RejectsTargetOutOfRange`, `TestTransitionPhase_AcceptsEveryDefinedPhase` (le chemin phase 0 → 1 du frontend reste valide) |
| 7 | Transition forcée : `Blockers` toujours `"[]"` | ✅ | `internal/construction/service.go` : sur le chemin forcé, les violations rouges de la phase cible (`construction.RedViolations()`) sont persistées dans `ConstructionPhaseLog.Blockers`. La ligne d'audit dit désormais **qui** a passé outre **et quoi**. Tests : `TestTransitionPhase_Force_RecordsSkippedBlockers`, `…_Force_CleanTrip_NoBlockers` |
| 9 | Encadrement anti-injection réduit à un commentaire | ✅ | `internal/leo/prompts.go` : `WrapUserRequest()` + constantes `UserRequestOpen`/`UserRequestClose`, neutralisation d'un délimiteur glissé dans le texte (le bloc ne peut pas être fermé en avance), et l'overlay `profile-edit` explique au modèle que ce bloc est une **donnée**. `TestWrapUserRequest` (4 cas dont une tentative d'injection) et `TestProfileEditOverlay_MentionsUserRequestDelimiters`. Le `TODO(hermes)` désigne le helper comme passage obligé du write-back |
| 8 | `transport_not_booked` se déclenche sur un bloc absent | ⏸️ accepté | Conforme à `construction/DESIGN.md`. Conséquence assumée : les seeds existants ont besoin d'une passe avant de viser la phase 4 (déjà noté au constat 16 ci-dessus) |
| 11 | Nuisances et discovery partagent une table de cache | ⏸️ accepté | Compromis documenté (clé de scope `nuisance:`, pas de collision possible). À revoir si un endpoint de purge par voyage apparaît |
| 12 | Le dépôt de specs contredit encore le code | ⏸️ hors dépôt | `rjullien/tripkit` est en lecture seule pour cette tâche ; les éditions exactes sont listées en §4 |

Constats 3 et 10 : frontend, voir `tripkit-frontend/docs/REVIEW-construction-fixes.md`.

**Limite inchangée de `MatchAdminRules`, non traitée ici** : le filtre « ce voyageur a le passeport du
pays de destination » travaille sur l'**union** des nationalités du voyage, donc un seul passeport
américain dans le groupe supprime l'ESTA pour *tous* (`TestMatchAdminRules_BiNationalNoESTA` fige ce
comportement, et la fixture `admin-check.json` avec lui). C'est une sous-alerte, l'inverse du constat 1,
antérieure à ces correctifs et hors de son périmètre ; la corriger demande de produire les items **par
voyageur** côté backend, ce qui change l'enveloppe.

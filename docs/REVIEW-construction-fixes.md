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
- Ce que le manifeste ne rattrape **pas** (constat 3 de la review v2) : il est committé dans chaque
  dépôt et ne hache que son propre répertoire, donc une copie frontend périmée accompagnée de son
  propre `CHECKSUMS.txt` périmé est cohérente avec elle-même et laisse les deux suites vertes. Seule
  la comparaison des deux checkouts le voit, et elle `SKIP` en CI mono-dépôt. D'où le job
  `fixtures-cross-repo` (`.github/workflows/ci.yaml`) : il clone `tripkit-frontend` à côté de ce dépôt
  et lance le garde avec `TRIPKIT_REQUIRE_FRONTEND_FIXTURES=1`, ce qui transforme le `SKIP` en échec.
  Le job a besoin du secret `CROSS_REPO_TOKEN` si le dépôt frontend est privé et n'est
  **volontairement pas** dans le `needs:` de la release : tant que le token n'est pas configuré, il est
  indicatif et non bloquant.

Déterminisme rendu nécessaire par les fixtures (et bénéfique pour l'API réelle, même si aucune spec
ne l'exigeait) : `DetectCountries` et `extractNationalities` trient désormais leurs résultats
(`internal/formalities/countries.go`, `internal/formalities/service.go`).

---

## 3. Reste à faire

### Vérifiable seulement contre une instance qui tourne

- ~~Comportement réel d'Overpass sous rate-limiting (429/504)~~ : **vérifié** (16/08/2026, retry +
  miroirs). Un run complet des 6 catégories contre l'API publique depuis l'environnement de travail :
  59,8 s, verdict 🔴 `ELEVE` (trains à 76 m de Matabiau), `incomplete=false`. `overpass-api.de` a
  réellement répondu **429 en cours de run** ; la rotation est repartie sur `overpass.kumi.systems`
  et la catégorie a abouti — c'est exactement le cas qui produisait « Donnée indisponible ».
  `kumi.systems` a aussi été vu en 504, récupéré par `overpass.openstreetmap.fr` : les trois
  endpoints sont utiles. Taux de succès effectif du cache 24 h : toujours non mesuré en production.
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

---

## 6. Troisième passe — review v2 (verdict NEEDS_CHANGES, 8 constats dont 1 bloquant)

Le constat bloquant (1) est un rendu frontend : voir `tripkit-frontend/docs/REVIEW-construction-fixes.md`
§7. Côté backend :

| # | Constat v2 | Statut | Détail |
|---|---|---|---|
| 3 | Le garde inter-dépôts `SKIP` en CI mono-dépôt | ✅ | Le manifeste ne peut pas fermer ce trou : committé dans chaque dépôt et ne hachant que son propre répertoire, il laisse passer une copie périmée accompagnée de son propre `CHECKSUMS.txt` périmé. Nouveau job `fixtures-cross-repo` (`.github/workflows/ci.yaml`) : il clone `tripkit-frontend` **à côté** de ce dépôt et lance `go test -run TestContractFixtures ./internal/handlers/` avec `TRIPKIT_REQUIRE_FRONTEND_FIXTURES` armé, ce qui transforme le `SKIP` « frontend absent » en **échec** (nouveau chemin dans `contract_fixtures_test.go`, plus `TRIPKIT_FRONTEND_CONTRACT_DIR` pour un autre agencement). Le job a besoin du secret `CROSS_REPO_TOKEN` si le dépôt frontend est privé et n'est volontairement pas dans le `needs:` de la release : tant que le token n'est pas configuré il est **indicatif**, ce que disent aussi le README des fixtures et §2. **Corrigé en v3 (§7)** : la ref frontend comparée était figée sur la branche par défaut, ce qui rendait le job rouge sur toute paire de branches coordonnée |
| 5 | Un corps sans clé `phase` valait phase 0 | ✅ | `internal/handlers/construction.go` : le corps se décode dans `Phase *int` et une valeur absente (ou `null`) répond `400 {"error":"phase is required"}`. `{}` ou `{"target":3}` ne rembobinent plus le voyage à « pas démarrée » avec une ligne d'audit à l'appui. Un `0` explicite reste accepté (remise à zéro légitime). Tests : `TestTransitionPhase_MissingPhaseKey` (4 corps, état et journal inchangés), `TestTransitionPhase_ExplicitZeroAccepted` |
| 6 | La neutralisation de `WrapUserRequest` était en correspondance exacte | ✅ | `internal/leo/prompts.go` : `userRequestDelimiterRe` neutralise toute variante de casse et d'espacement du délimiteur, en distinguant ouverture et fermeture. Un texte qui mentionne simplement `user_request` n'est pas touché. **Corrigé en v3 (§7)** : la classe d'espaces était `[ \t]`, donc `</user_request\n>` passait encore |
| 7 | Le match par mot entier perd les mots composés | ✅ **corrigé en v3 (§7)** | La v2 avait figé les pertes comme choix assumé (`vélo` ne matchant plus `Vélodrome`, `art` plus `Artothèque`), avec une justification fausse d'axe : ce n'est pas la **longueur** qui sépare `vélo` de `long` (4 lettres chacun) mais la **direction** de la préférence. Voir §7 |

Constats 1, 2, 4 et 8 : frontend.

**Vérifications** (locales, aucune instance joignable) : `go build ./...`, `go vet ./...`,
`go test ./internal/... -count=1` (14 paquets `ok`, `internal/database` sans test), plus
`TRIPKIT_REQUIRE_FRONTEND_FIXTURES=1 go test -run TestContractFixtures ./internal/handlers/`
(le garde inter-dépôts passe avec les deux dépôts côte à côte, et échoue bien si le répertoire
frontend manque — vérifié avec `TRIPKIT_FRONTEND_CONTRACT_DIR=/tmp/nope`).

---

## 7. Quatrième passe — review v3 (verdict NEEDS_CHANGES, 6 constats dont 2 bloquants)

Les deux constats bloquants sont des rendus frontend (en-tête admin encore verte sur un check à zéro
item, silence santé indistinguable d'une destination non identifiée) : voir
`tripkit-frontend/docs/REVIEW-construction-fixes.md` §8. Côté backend, trois constats, dont deux
corrigent des affirmations de la passe précédente :

| # | Constat v3 | Statut | Détail |
|---|---|---|---|
| 3 | Le job `fixtures-cross-repo` ne pouvait pas passer sur un changement coordonné | ✅ | `.github/workflows/ci.yaml` : la ref frontend était `github.event.inputs.frontend_ref`, vide hors `workflow_dispatch`, donc `actions/checkout` prenait la **branche par défaut** du frontend, où `tests/fixtures/construction-contract/` n'existe pas encore — et `TRIPKIT_REQUIRE_FRONTEND_FIXTURES=1` transformait cette absence en échec. Le job était donc **rouge sur exactement le cas qu'il surveille**. Nouvelle étape `Resolve the frontend ref to compare against` : (a) une ref donnée en `workflow_dispatch` gagne toujours, strict ; (b) sinon, si `tripkit-frontend` a une branche du **même nom** que la branche de tête ici (une modification de fixtures voyage en paire de branches homonymes, une par dépôt), c'est celle-là, strict ; (c) sinon la branche par défaut, **non strict** — les fixtures présentes sont quand même comparées octet à octet, absentes elles font `SKIP` au lieu de faire échouer une PR sans pendant frontend. `TRIPKIT_REQUIRE_FRONTEND_FIXTURES` reçoit désormais `1`/`0` selon le cas au lieu d'un `1` constant. Un token absent (`CROSS_REPO_TOKEN`) fait échouer le `git ls-remote` et retombe sur (c), toujours sans rougir. Table de comportement dans `internal/handlers/testdata/contract/README.md` |
| 5 | La classe d'espaces du régex de délimiteurs s'arrêtait aux espaces et tabulations | ✅ | `internal/leo/prompts.go` : `userRequestDelimiterRe` passe de `[ \t]` à `\s`, donc `</user_request\n>` — qu'un modèle lit comme une fermeture de bloc, et qu'un retour à la ligne dans un textarea produit sans effort — est neutralisé comme les autres variantes. Deux variantes à retour à la ligne ajoutées à la table de `TestWrapUserRequest` ; mutation vérifiée : revenir à `[ \t]` fait tomber exactement ces deux cas. Le texte qui mentionne `user_request` sans délimiteur reste intact |
| 6 | La justification enregistrée du compromis mots composés visait le mauvais axe | ✅ corrigé **et** implémenté | La v2 concluait qu'aucun seuil de longueur ne sépare `vélo` (voulu dans `Vélodrome`) de `long` (non voulu dans `Longueuil`) — vrai, mais ce n'est pas la longueur qui les sépare, c'est la **direction** : `shopping long` est un *dislike* de `tripkit-seeds/travel-profile.js`, et le commentaire de `matchKeyword` disait déjà qu'un faux positif ne coûte quelque chose que sur un dislike (+10, il déclasse un item légitime) alors que sur un *like* il n'accorde qu'un bonus de tri. Donc `matchKeyword(name, themeID, kw, allowPrefix)` : `interestScore` passe `true` pour les likes (match par préfixe de mot, plancher `prefixMinRunes = 3`) et `false` pour les dislikes (mot entier). `vélo` retrouve `Vélodrome` et `art` `Artothèque`, tandis que `long` reste hors de `Longueuil`. `TestMatchKeyword_CompoundWordsAreKnownMisses` est remplacé par `TestMatchKeyword_PrefixMatchingIsLikeOnly` (10 cas, les deux directions, le plancher de longueur et un faux positif assumé : `parc` matche `Parcheminerie` en like) plus `TestInterestScore_PrefixOnlyHelpsLikes` qui le prouve à travers le scoreur. Mutation vérifiée : passer les likes en `false` fait tomber ce dernier |

Constats 1, 2 et 4 : frontend.

**Vérifications** (locales, aucune instance joignable) : `go build ./...`, `go vet ./...`,
`go test ./internal/... -count=1` (14 paquets `ok`, `internal/database` sans test — compte
inchangé). Garde inter-dépôts rejoué dans les trois régimes du job :
`TRIPKIT_REQUIRE_FRONTEND_FIXTURES=1` avec les deux dépôts côte à côte → `ok` ; le même avec
`TRIPKIT_FRONTEND_CONTRACT_DIR=/tmp/nope` → échec attendu ; `TRIPKIT_REQUIRE_FRONTEND_FIXTURES=0`
avec un répertoire absent → `SKIP`. L'étape de résolution de ref a été extraite du workflow et
exécutée hors CI contre un clone local (`FRONTEND_REMOTE`) pour les quatre cas : branche homonyme
présente → `ref=<branche> strict=1`, absente → `ref= strict=0`, ref dispatchée → `strict=1`, remote
injoignable → `strict=0`. **Ce qui reste non vérifiable ici** : le job lui-même sur les *runners*
GitHub (le `github.head_ref` réel, le secret `CROSS_REPO_TOKEN`), et tout ce qui demande une
instance qui tourne (Overpass, Bifrost, Postgres).

---

## 9. Rebase sur `main` v1.19.24 + leftovers #63

Ne **pas** merger #61 ni #63. Cette branche (`cursor/construction-be-rebase-6143`) rejoue
Construction sur le `main` courant (magasinage `excludeNames` + `theme@v{version}` conservés)
et reprend le lot utile de #63 :

| De #63 | Statut | Détail |
|---|---|---|
| Loader `ops/construction.json` | ✅ | Completer par check ; nil → déterministe, pas de `summary` de repli présenté comme un LLM |
| Admin par voyageur | ✅ | Fini le faux négatif d'union (René FR a l'ESTA même si Dinah est FR+US). `items[]` reste l'enveloppe FE ; `travelers[]` est **en plus** (`omitempty`) |
| `deadline` + détail coût/délai | ✅ | Fixture d'or régénérée |
| Santé silence `none` sans appel LLM | ✅ | Inchangé |
| Gate nuisances Ph3→Ph4 | ✅ | `NuisanceBlockers` |
| INDETERMINE déjà dans #61 | ✅ | Cache Overpass conservé ; `partial` alias de `incomplete` pour le gate. Précédence **ELEVE > INDETERMINE > MODERE > FAIBLE** (contrat FE), pas « yellow beats unknown » de #63 |
| Emoji INDETERMINE | ✅ | ⚪ (fixture FE), pas ❓ |

Write-back Léo (retain / pin / profile-edit) : toujours 501.

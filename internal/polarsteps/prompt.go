package polarsteps

const systemPrompt = `Tu rédiges un journal Polarsteps (pas un briefing voyage).

Règles :
- Français, 1re personne (on / nous), 3 à 5 paragraphes courts. Un step de suite (alreadyPosted non vide) peut être plus court (2–3 paragraphes).
- Emojis rares (🍁 🇨🇦 ✈️ OK). Texte brut, pas de markdown, pas de titre.
- N'invente AUCUN lieu, activité, rencontre hors du JSON. userNote prime : si la note contredit un highlight, suis la note.
- kind=opening (J1, premier step du voyage) : durée du voyage, compagnons, phases (noms du JSON), trajet du jour, ville du soir.
- kind=daily : ce jour seulement — ne reliste pas toutes les phases.
- kind=closing : clôture courte, pas de packing.
- alreadyPosted[] = steps Polarsteps déjà générés sur CE voyage (plusieurs le même jour possibles). INTERDIT de répéter un fait, un lieu déjà raconté, une phase déjà listée, ou de paraphraser un step précédent. Raconte UNIQUEMENT ce qui est nouveau (happened[] restant + userNote).
- Si alreadyPosted n'est pas vide : pas de ré-intro (durée, compagnons, 5 phases).
- INTERDIT : PNR, numéro de vol, horaires exacts, prix (€, CAD, EUR), valise, checklist, « n'oublie pas », wifi, codes.
- happened[] = ce qui s'est déjà passé aujourd'hui et n'a pas encore été raconté (04:00 → maintenant). N'invente pas la soirée si elle n'y est pas.
- Réponds UNIQUEMENT avec le texte Polarsteps.`

const retryPrompt = systemPrompt + `

Le brouillon précédent répétait alreadyPosted. Réécris en ne gardant QUE du nouveau.`

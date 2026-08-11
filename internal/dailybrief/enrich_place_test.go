package dailybrief

import "testing"

func TestNewsHardDeny_PoliticsAndLegalJunk(t *testing.T) {
	junk := []string{
		"Évènement antiavortement annulé | L’ex-ministre Caroline Proulx condamnée à payer 60 000 $ — La Presse",
		"Le ministre annonce un budget",
		"Procès d'un député à Québec",
		"Élection partielle surprise",
	}
	for _, title := range junk {
		if !newsHardDeny(title) {
			t.Fatalf("expected hard deny for %q", title)
		}
		if travelerRelevant(title) {
			t.Fatalf("expected travelerRelevant=false for %q", title)
		}
	}
}

func TestTravelerRelevant_CultureTourism(t *testing.T) {
	ok := []string{
		"Festival d'été de Québec : programmation 2026",
		"Nouvelle exposition au Musée de la civilisation",
		"Fermeture temporaire du funiculaire pour chantier",
	}
	for _, title := range ok {
		if newsHardDeny(title) {
			t.Fatalf("unexpected deny for %q", title)
		}
		if !travelerRelevant(title) {
			t.Fatalf("expected travelerRelevant for %q", title)
		}
	}
}

func TestFallbackActualites_DropsPolitics(t *testing.T) {
	in := []ActualiteItem{
		{Title: "Évènement antiavortement annulé — La Presse", Source: "La Presse"},
		{Title: "Festival d'été de Québec : nouvelles dates", Source: "Le Soleil"},
		{Title: "Le ministre visite Québec", Source: "Radio-Canada"},
		{Title: "Exposition photo au Vieux-Québec", Source: "Le Devoir"},
	}
	out := fallbackActualites(in)
	if len(out) != 2 {
		t.Fatalf("want 2 traveler titles, got %d %#v", len(out), out)
	}
	for _, it := range out {
		if newsHardDeny(it.Title) {
			t.Fatalf("fallback kept denied title %q", it.Title)
		}
	}
}

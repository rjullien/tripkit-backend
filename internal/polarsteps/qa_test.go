package polarsteps

import (
	"strings"
	"testing"
)

const golden = `Décollage depuis Nice Côte d'Azur ce matin pour une grande boucle au Québec.

18 jours, tous les 3 avec Baptiste, un itinéraire en 5 phases : Québec et Charlevoix, le Fjord du Saguenay, Tadoussac et les baleines, la Gaspésie sauvage, et le Bas-Saint-Laurent pour boucler la boucle.

Nice, Genève, Montréal. Escale courte, puis la route commence.

Premier arrêt ce soir : Montréal. La suite s'annonce belle.`

func TestRunQA_PassOpening(t *testing.T) {
	in := &Input{
		Kind:   "opening",
		From:   "Nice",
		To:     "Montréal",
		Phases: []string{"Québec & Charlevoix", "Fjord du Saguenay"},
	}
	qa := RunQA(golden, in)
	if qa.Verdict != QAPassed && qa.Verdict != QAWarning {
		t.Fatalf("verdict=%s issues=%v", qa.Verdict, qa.Issues)
	}
}

func TestRunQA_FailPNR(t *testing.T) {
	in := &Input{Kind: "daily", From: "Nice", To: "Montréal"}
	text := golden + " PNR 8WQZPY"
	qa := RunQA(text, in)
	if qa.Verdict != QAFailed {
		t.Fatalf("expected FAILED, got %s", qa.Verdict)
	}
}

func TestRunQA_FailShort(t *testing.T) {
	in := &Input{Kind: "daily", From: "Nice", To: "Montréal"}
	qa := RunQA("On est à Montréal.", in)
	if qa.Verdict != QAFailed {
		t.Fatalf("expected FAILED short, got %s", qa.Verdict)
	}
}

func TestRunQA_FailToponyme(t *testing.T) {
	in := &Input{Kind: "daily", From: "Percé", To: "Percé"}
	qa := RunQA(golden, in)
	if qa.Verdict != QAFailed {
		t.Fatalf("expected FAILED toponyme, got %s %v", qa.Verdict, qa.Issues)
	}
}

func TestRunQA_NoteWarning(t *testing.T) {
	in := &Input{
		Kind:     "daily",
		From:     "Nice",
		To:       "Montréal",
		UserNote: "crepes sucrees au chalet",
	}
	qa := RunQA(golden, in)
	if qa.Verdict == QAPassed {
		t.Fatal("expected WARNING when long note tokens missing")
	}
	found := false
	for _, i := range qa.Issues {
		if strings.Contains(i, "note") {
			found = true
		}
	}
	if !found {
		t.Fatalf("issues=%v", qa.Issues)
	}
}

package handlers

import (
	"strings"
	"testing"

	"github.com/rjullien/tripkit-backend/internal/publish"
)

func TestLeoPromptContext_NadiaScoped(t *testing.T) {
	reg := publish.DefaultDogfoodRegistry()
	ctx := leoPromptContext(reg, "nadia", false, "x")
	if ctx.Username != "nadia" {
		t.Fatalf("user=%q", ctx.Username)
	}
	if ctx.IsAdmin {
		t.Fatal("nadia must not be admin")
	}
	if len(ctx.AllowedRepos) != 1 || ctx.AllowedRepos[0] != "rjullien/tripkit-seeds-nadia" {
		t.Fatalf("repos=%v", ctx.AllowedRepos)
	}
}

func TestLeoPromptContext_AdminSeesAll(t *testing.T) {
	reg := publish.DefaultDogfoodRegistry()
	ctx := leoPromptContext(reg, "rene", true, "quebec-2026")
	if !ctx.IsAdmin {
		t.Fatal("expected admin")
	}
	joined := strings.Join(ctx.AllowedRepos, ",")
	for _, want := range []string{
		"rjullien/tripkit-seeds",
		"rjullien/tripkit-seeds-nadia",
		"rjullien/tripkit-seeds-laurine",
		"rjullien/tripkit-seeds-jihane",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in %v", want, ctx.AllowedRepos)
		}
	}
}

func TestLeoPromptContext_NilRegistryFallsBack(t *testing.T) {
	ctx := leoPromptContext(nil, "laurine", false, "")
	if len(ctx.AllowedRepos) != 1 || ctx.AllowedRepos[0] != "rjullien/tripkit-seeds-laurine" {
		t.Fatalf("repos=%v", ctx.AllowedRepos)
	}
}

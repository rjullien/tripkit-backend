package publish_test

import (
	"testing"

	"github.com/rjullien/tripkit-backend/internal/publish"
)

func TestWorkerEnabled(t *testing.T) {
	t.Setenv("TRIPKIT_GITHUB_TOKEN", "")
	t.Setenv("TRIPKIT_PUBLISH_WORKER", "")
	if publish.WorkerEnabled() {
		t.Fatal("expected off without token")
	}

	t.Setenv("TRIPKIT_GITHUB_TOKEN", "ghp_test")
	if !publish.WorkerEnabled() {
		t.Fatal("expected on when token set")
	}

	t.Setenv("TRIPKIT_PUBLISH_WORKER", "0")
	if publish.WorkerEnabled() {
		t.Fatal("expected forced off")
	}

	t.Setenv("TRIPKIT_GITHUB_TOKEN", "")
	t.Setenv("TRIPKIT_PUBLISH_WORKER", "1")
	if !publish.WorkerEnabled() {
		t.Fatal("expected forced on")
	}
}

func TestDefaultDogfoodRegistry_JullienEnabled(t *testing.T) {
	t.Setenv("TRIPKIT_PUBLISH_SOURCES", "")
	reg, err := publish.LoadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	s, ok := reg.Get("jullien")
	if !ok || !s.Enabled {
		t.Fatal("jullien should be enabled in dogfood defaults")
	}
	if !reg.CanPublish("jullien", "rene", false) {
		t.Fatal("rene should publish quebec dogfood")
	}
	nadia, ok := reg.Get("nadia")
	if !ok || nadia.Enabled {
		t.Fatal("nadia must stay disabled")
	}
}

func TestCreateJob_RequiresGitHubToken(t *testing.T) {
	t.Setenv("TRIPKIT_GITHUB_TOKEN", "")
	reg := publish.DefaultDogfoodRegistry()
	_, err := publish.CreateJob(nil, reg, publish.CreateJobRequest{
		SourceID: "jullien", TripID: "quebec-2026", ConfirmCreate: true,
	}, "rene", false)
	if err != publish.ErrNoGitHubToken {
		t.Fatalf("want ErrNoGitHubToken, got %v", err)
	}
}

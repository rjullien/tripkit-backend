// Package seedgit applies typed patches to family seed JS files on GitHub.
//
// Léo still owns prose / structural edits. Numeric and enum fields that the
// app already validates (construction phase first) go through this package:
// parse → surgical rewrite → parse + allowlist diff → Contents API PUT.
// A failed parse or a diff outside the allowlist never reaches GitHub.
package seedgit

import (
	"fmt"
	"strings"

	"github.com/rjullien/tripkit-backend/internal/construction"
	"github.com/rjullien/tripkit-backend/internal/publish"
)

// FilesClient reads and writes a single GitHub Contents file.
type FilesClient interface {
	GetFile(repo, ref, path string) (content []byte, sha string, err error)
	PutFile(repo, branch, path, message, sha string, content []byte) (newSHA string, err error)
}

// GitHubFiles adapts publish.GitHubClient to FilesClient.
type GitHubFiles struct {
	Client *publish.GitHubClient
}

// GetFile implements FilesClient.
func (g GitHubFiles) GetFile(repo, ref, path string) ([]byte, string, error) {
	if g.Client == nil {
		return nil, "", fmt.Errorf("TRIPKIT_GITHUB_TOKEN not configured")
	}
	blob, err := g.Client.GetContents(repo, ref, path)
	if err != nil {
		return nil, "", err
	}
	return blob.Content, blob.SHA, nil
}

// PutFile implements FilesClient.
func (g GitHubFiles) PutFile(repo, branch, path, message, sha string, content []byte) (string, error) {
	if g.Client == nil {
		return "", fmt.Errorf("TRIPKIT_GITHUB_TOKEN not configured")
	}
	return g.Client.PutContents(repo, branch, path, message, sha, content)
}

// Service locates a trip's seed file and pushes typed patches.
type Service struct {
	Registry *publish.Registry
	Manifest *publish.ManifestResolver
	Files    FilesClient
}

var _ construction.SeedGit = (*Service)(nil)

// Target is a resolved seed file in a trusted family repo.
type Target struct {
	Source publish.Source
	Seed   publish.SeedRef
}

// LocateTrip finds the enabled publish source that lists tripID.
func LocateTrip(reg *publish.Registry, resolver *publish.ManifestResolver, tripID string) (Target, error) {
	tripID = strings.TrimSpace(tripID)
	if tripID == "" {
		return Target{}, fmt.Errorf("trip id required")
	}
	if reg == nil {
		return Target{}, fmt.Errorf("publish registry not configured")
	}
	var hits []Target
	for _, src := range reg.Snapshot() {
		if !src.Enabled {
			continue
		}
		seeds, err := seedsOf(resolver, src)
		if err != nil {
			continue
		}
		seed, ok := publish.FindSeedRef(seeds, tripID)
		if !ok {
			continue
		}
		hits = append(hits, Target{Source: src, Seed: seed})
	}
	if len(hits) == 0 {
		return Target{}, fmt.Errorf("trip %q not in any enabled seed repo allowlist", tripID)
	}
	best := hits[0]
	for _, h := range hits[1:] {
		if h.Source.ID < best.Source.ID {
			best = h
		}
	}
	return best, nil
}

func seedsOf(resolver *publish.ManifestResolver, src publish.Source) ([]publish.SeedRef, error) {
	if resolver != nil {
		return resolver.SeedsForSource(src)
	}
	if len(src.Seeds) == 0 {
		return nil, fmt.Errorf("no seeds for source %s", src.ID)
	}
	return src.Seeds, nil
}

// PushPhase writes trip.construction.phase into the seed repo.
// The DB remains source of truth: callers must not fail the HTTP transition on error.
func (s *Service) PushPhase(tripID string, phase int, user string) (*construction.SeedPushResult, error) {
	if !construction.ValidPhase(phase) {
		return failPush("", "", "", fmt.Errorf("invalid phase %d", phase)), nil
	}
	if s == nil || s.Files == nil {
		return failPush("", "", "", fmt.Errorf("seedgit not configured")), nil
	}
	target, err := LocateTrip(s.Registry, s.Manifest, tripID)
	if err != nil {
		return failPush("", "", "", err), nil
	}
	ref := target.Source.Ref
	if ref == "" {
		ref = "main"
	}
	result := &construction.SeedPushResult{
		Repo: target.Source.Repo,
		Path: target.Seed.Path,
		Ref:  ref,
	}

	content, sha, err := s.Files.GetFile(target.Source.Repo, ref, target.Seed.Path)
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}

	patched, err := PatchPhase(string(content), phase)
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}
	if err := validatePhasePatch(string(content), patched, tripID, phase, target.Source.ExpectedFamily); err != nil {
		result.Error = err.Error()
		return result, nil
	}
	if patched == string(content) {
		result.OK = true
		result.SHA = sha
		result.Unchanged = true
		return result, nil
	}

	newSHA, err := s.Files.PutFile(
		target.Source.Repo,
		ref,
		target.Seed.Path,
		phaseCommitMessage(tripID, phase, user),
		sha,
		[]byte(patched),
	)
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}
	result.OK = true
	result.SHA = newSHA
	return result, nil
}

func failPush(repo, path, ref string, err error) *construction.SeedPushResult {
	msg := "seedgit error"
	if err != nil {
		msg = err.Error()
	}
	return &construction.SeedPushResult{Repo: repo, Path: path, Ref: ref, Error: msg}
}

func phaseCommitMessage(tripID string, phase int, user string) string {
	user = strings.TrimSpace(strings.ReplaceAll(user, "\n", " "))
	if len(user) > 64 {
		user = user[:64]
	}
	if user == "" {
		user = "tripkit"
	}
	return fmt.Sprintf("feat(construction): set %s phase to %d\n\nTriggered-by: %s\n", tripID, phase, user)
}

func validatePhasePatch(before, after, tripID string, phase int, family string) error {
	if _, err := publish.ParseSeedFile(after); err != nil {
		return fmt.Errorf("patched seed does not parse: %w", err)
	}
	afterSeed, err := publish.ParseSeedFile(after)
	if err != nil {
		return err
	}
	if errs := publish.StructuralValidate(afterSeed, tripID, family, family); len(errs) > 0 {
		return fmt.Errorf("patched seed failed canonical checks: %s", strings.Join(errs, "; "))
	}
	if _, err := publish.BuildCanonical(afterSeed, map[string]publish.Person{}, family, "seedgit", "", nil); err != nil {
		return fmt.Errorf("patched seed failed BuildCanonical: %w", err)
	}
	if got := constructionPhaseOf(afterSeed); got != phase {
		return fmt.Errorf("patched seed phase=%d want %d", got, phase)
	}
	if err := allowlistPhaseOnly(before, after); err != nil {
		return err
	}
	return nil
}

func constructionPhaseOf(seed publish.SeedFile) int {
	if seed.Trip == nil {
		return -1
	}
	c, _ := seed.Trip["construction"].(map[string]any)
	if c == nil {
		return -1
	}
	switch n := c["phase"].(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return -1
	}
}

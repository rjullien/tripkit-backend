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
	return s.applyPatch(target, phaseCommitMessage(tripID, phase, user),
		func(src string) (string, error) { return PatchPhase(src, phase) },
		func(before, after string) error {
			return validatePhasePatch(before, after, tripID, phase, target.Source.ExpectedFamily)
		},
	), nil
}

// PushActivity writes one trip.activities entry into the seed repo.
func (s *Service) PushActivity(tripID string, activity map[string]any, user string) (*construction.SeedPushResult, error) {
	if s == nil || s.Files == nil {
		return failPush("", "", "", fmt.Errorf("seedgit not configured")), nil
	}
	id, _ := activity["id"].(string)
	if strings.TrimSpace(id) == "" {
		return failPush("", "", "", fmt.Errorf("activity id required")), nil
	}
	target, err := LocateTrip(s.Registry, s.Manifest, tripID)
	if err != nil {
		return failPush("", "", "", err), nil
	}
	return s.applyPatch(target, activityCommitMessage(tripID, id, user),
		func(src string) (string, error) { return PatchActivity(src, activity) },
		func(before, after string) error {
			return validateWritePatch(before, after, tripID, target.Source.ExpectedFamily, func(p string) bool {
				return pathHasPrefix(p, "activities")
			})
		},
	), nil
}

// PushPin writes lastQa + hotels[].nuisance into the seed repo.
func (s *Service) PushPin(tripID string, lastQa map[string]any, hotelNuisance map[string]map[string]any, user string) (*construction.SeedPushResult, error) {
	if s == nil || s.Files == nil {
		return failPush("", "", "", fmt.Errorf("seedgit not configured")), nil
	}
	target, err := LocateTrip(s.Registry, s.Manifest, tripID)
	if err != nil {
		return failPush("", "", "", err), nil
	}
	return s.applyPatch(target, pinCommitMessage(tripID, user),
		func(src string) (string, error) { return PatchPin(src, lastQa, hotelNuisance) },
		func(before, after string) error {
			return validateWritePatch(before, after, tripID, target.Source.ExpectedFamily, func(p string) bool {
				if p == "trip.construction.lastQa" || pathHasPrefix(p, "trip.construction.lastQa") {
					return true
				}
				return strings.HasPrefix(p, "hotels.") && strings.Contains(p, ".nuisance")
			})
		},
	), nil
}

func (s *Service) applyPatch(
	target Target,
	message string,
	patch func(string) (string, error),
	validate func(before, after string) error,
) *construction.SeedPushResult {
	ref := target.Source.Ref
	if ref == "" {
		ref = "main"
	}
	result := &construction.SeedPushResult{
		Repo: target.Source.Repo,
		Path: target.Seed.Path,
		Ref:  ref,
	}
	if s == nil || s.Files == nil {
		result.Error = "seedgit not configured"
		return result
	}
	content, sha, err := s.Files.GetFile(target.Source.Repo, ref, target.Seed.Path)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	patched, err := patch(string(content))
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if err := validate(string(content), patched); err != nil {
		result.Error = err.Error()
		return result
	}
	if patched == string(content) {
		result.OK = true
		result.SHA = sha
		result.Unchanged = true
		return result
	}
	newSHA, err := s.Files.PutFile(target.Source.Repo, ref, target.Seed.Path, message, sha, []byte(patched))
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.OK = true
	result.SHA = newSHA
	return result
}

func failPush(repo, path, ref string, err error) *construction.SeedPushResult {
	msg := "seedgit error"
	if err != nil {
		msg = err.Error()
	}
	return &construction.SeedPushResult{Repo: repo, Path: path, Ref: ref, Error: msg}
}

func sanitizeCommitUser(user string) string {
	user = strings.TrimSpace(strings.ReplaceAll(user, "\n", " "))
	if len(user) > 64 {
		user = user[:64]
	}
	if user == "" {
		user = "tripkit"
	}
	return user
}

func phaseCommitMessage(tripID string, phase int, user string) string {
	return fmt.Sprintf("feat(construction): set %s phase to %d\n\nTriggered-by: %s\n", tripID, phase, sanitizeCommitUser(user))
}

func activityCommitMessage(tripID, activityID, user string) string {
	id := strings.TrimSpace(activityID)
	if id == "" {
		id = "activity"
	}
	return fmt.Sprintf("feat(construction): retain activity %s (%s)\n\nTriggered-by: %s\n", id, tripID, sanitizeCommitUser(user))
}

func pinCommitMessage(tripID, user string) string {
	return fmt.Sprintf("feat(construction): pin nuisance (%s)\n\nTriggered-by: %s\n", tripID, sanitizeCommitUser(user))
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

func validateWritePatch(before, after, tripID, family string, ok func(string) bool) error {
	afterSeed, err := publish.ParseSeedFile(after)
	if err != nil {
		return fmt.Errorf("patched seed does not parse: %w", err)
	}
	if errs := publish.StructuralValidate(afterSeed, tripID, family, family); len(errs) > 0 {
		return fmt.Errorf("patched seed failed canonical checks: %s", strings.Join(errs, "; "))
	}
	if _, err := publish.BuildCanonical(afterSeed, map[string]publish.Person{}, family, "seedgit", "", nil); err != nil {
		return fmt.Errorf("patched seed failed BuildCanonical: %w", err)
	}
	return allowlistPaths(before, after, ok)
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

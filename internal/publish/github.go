package publish

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

const maxArchiveBytes = 40 << 20 // 40 MiB

// GitHubClient fetches repo zipballs with a fine-grained PAT or GitHub App token.
type GitHubClient struct {
	Token  string
	Client *http.Client
}

// NewGitHubClientFromEnv uses TRIPKIT_GITHUB_TOKEN (optional).
func NewGitHubClientFromEnv() *GitHubClient {
	return &GitHubClient{
		Token: sanitizeGitHubToken(os.Getenv("TRIPKIT_GITHUB_TOKEN")),
		Client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// sanitizeGitHubToken trims whitespace/newlines and accidental surrounding quotes
// (common when pasting a PAT into Infisical).
func sanitizeGitHubToken(tok string) string {
	tok = strings.TrimSpace(tok)
	tok = strings.Trim(tok, "\"'")
	return strings.TrimSpace(tok)
}

// FetchFile downloads a single file from owner/repo@ref via the Contents API.
func (g *GitHubClient) FetchFile(repo, ref, filePath string) ([]byte, error) {
	if g == nil || g.Token == "" {
		return nil, fmt.Errorf("TRIPKIT_GITHUB_TOKEN not configured")
	}
	if ref == "" {
		ref = "main"
	}
	filePath = strings.TrimPrefix(filePath, "./")
	if filePath == "" || strings.Contains(filePath, "..") {
		return nil, fmt.Errorf("invalid path %q", filePath)
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/contents/%s?ref=%s", repo, filePath, ref)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+g.Token)
	req.Header.Set("Accept", "application/vnd.github.raw")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "tripkit-publish-worker")

	res, err := g.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return nil, formatGitHubHTTPError("contents", repo+"/"+filePath, res.StatusCode, body)
	}
	limited := io.LimitReader(res.Body, 1<<20+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(data) > 1<<20 {
		return nil, fmt.Errorf("file exceeds 1 MiB: %s", filePath)
	}
	return data, nil
}

// FetchRepoZip downloads owner/repo@ref as a zip archive.
func (g *GitHubClient) FetchRepoZip(repo, ref string) (zipBytes []byte, resolvedSHA string, err error) {
	if g == nil || g.Token == "" {
		return nil, "", fmt.Errorf("TRIPKIT_GITHUB_TOKEN not configured")
	}
	if ref == "" {
		ref = "main"
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/zipball/%s", repo, ref)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+g.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "tripkit-publish-worker")

	res, err := g.Client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return nil, "", formatGitHubHTTPError("zipball", repo, res.StatusCode, body)
	}
	// GitHub redirects; final URL often contains the commit SHA.
	resolvedSHA = extractSHAFromURL(res.Request.URL.String())
	limited := io.LimitReader(res.Body, maxArchiveBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, "", err
	}
	if len(data) > maxArchiveBytes {
		return nil, "", fmt.Errorf("archive exceeds %d bytes", maxArchiveBytes)
	}
	return data, resolvedSHA, nil
}

func formatGitHubHTTPError(op, repo string, status int, body []byte) error {
	snippet := strings.TrimSpace(string(body))
	if len(snippet) > 280 {
		snippet = snippet[:280] + "…"
	}
	switch status {
	case http.StatusUnauthorized:
		return fmt.Errorf(
			"github %s %s: 401 Unauthorized — TRIPKIT_GITHUB_TOKEN invalide/expiré "+
				"(Infisical /tripkit → github-token → secret tripkit-secrets, puis restart pod). %s",
			op, repo, snippet,
		)
	case http.StatusForbidden:
		return fmt.Errorf(
			"github %s %s: 403 Forbidden — le PAT n'a pas Contents:read sur ce repo "+
				"(fine-grained: ajouter rjullien/tripkit-seeds*). %s",
			op, repo, snippet,
		)
	case http.StatusNotFound:
		return fmt.Errorf(
			"github %s %s: 404 Not Found — repo/ref introuvable ou PAT sans accès. %s",
			op, repo, snippet,
		)
	default:
		return fmt.Errorf("github %s %s: HTTP %d (%s)", op, repo, status, snippet)
	}
}

func extractSHAFromURL(u string) string {
	// .../owner-repo-<sha>.zip or path ending with sha
	base := path.Base(u)
	base = strings.TrimSuffix(base, ".zip")
	if i := strings.LastIndex(base, "-"); i >= 0 && len(base)-i-1 >= 7 {
		return base[i+1:]
	}
	return ""
}

// ZipTree is a safe extracted view of allowlisted files.
type ZipTree map[string][]byte // path relative to repo root

// ExtractAllowlisted reads only requested paths from a GitHub zipball.
// GitHub wraps files in a top-level owner-repo-sha/ directory.
func ExtractAllowlisted(zipBytes []byte, paths []string) (ZipTree, error) {
	want := map[string]bool{}
	for _, p := range paths {
		p = strings.TrimPrefix(p, "./")
		if p == "" || strings.Contains(p, "..") {
			return nil, fmt.Errorf("invalid path %q", p)
		}
		want[p] = true
	}
	r, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, err
	}
	out := ZipTree{}
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := f.Name
		// strip first path segment
		if i := strings.IndexByte(name, '/'); i >= 0 {
			name = name[i+1:]
		}
		if !want[name] {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(rc, 8<<20))
		rc.Close()
		if err != nil {
			return nil, err
		}
		out[name] = data
	}
	for p := range want {
		if _, ok := out[p]; !ok {
			return nil, fmt.Errorf("missing file in archive: %s", p)
		}
	}
	return out, nil
}

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
	tok := strings.TrimSpace(os.Getenv("TRIPKIT_GITHUB_TOKEN"))
	return &GitHubClient{
		Token: tok,
		Client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
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
		return nil, "", fmt.Errorf("github zipball %s: %s (%s)", repo, res.Status, strings.TrimSpace(string(body)))
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

package publish

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const maxContentsBytes = 1 << 20 // 1 MiB, same as FetchFile

// FileBlob is a GitHub Contents API file (decoded) plus its blob SHA.
type FileBlob struct {
	Path    string
	SHA     string
	Content []byte
}

type contentsJSON struct {
	SHA      string `json:"sha"`
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
	Size     int    `json:"size"`
	Path     string `json:"path"`
	Message  string `json:"message"`
}

type putContentsBody struct {
	Message string `json:"message"`
	Content string `json:"content"`
	SHA     string `json:"sha"`
	Branch  string `json:"branch,omitempty"`
}

type putContentsJSON struct {
	Content *struct {
		SHA string `json:"sha"`
	} `json:"content"`
	Message string `json:"message"`
}

func (g *GitHubClient) contentsURL(repo, filePath, ref string) (string, error) {
	if g == nil || g.Token == "" {
		return "", fmt.Errorf("TRIPKIT_GITHUB_TOKEN not configured")
	}
	filePath = strings.TrimPrefix(filePath, "./")
	if filePath == "" || strings.Contains(filePath, "..") {
		return "", fmt.Errorf("invalid path %q", filePath)
	}
	base := "https://api.github.com"
	if g != nil && strings.TrimSpace(g.BaseURL) != "" {
		base = strings.TrimRight(g.BaseURL, "/")
	}
	url := fmt.Sprintf("%s/repos/%s/contents/%s", base, repo, filePath)
	if ref != "" {
		url += "?ref=" + ref
	}
	return url, nil
}

func (g *GitHubClient) newContentsRequest(method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+g.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "tripkit-seedgit")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (g *GitHubClient) httpClient() *http.Client {
	if g != nil && g.Client != nil {
		return g.Client
	}
	return http.DefaultClient
}

// GetContents fetches a file via the Contents JSON API (decoded bytes + blob SHA).
func (g *GitHubClient) GetContents(repo, ref, filePath string) (*FileBlob, error) {
	if ref == "" {
		ref = "main"
	}
	url, err := g.contentsURL(repo, filePath, ref)
	if err != nil {
		return nil, err
	}
	req, err := g.newContentsRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := g.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 2<<20+1))
	if res.StatusCode >= 300 {
		return nil, formatGitHubHTTPError("contents", repo+"/"+filePath, res.StatusCode, body)
	}
	var parsed contentsJSON
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("github contents json: %w", err)
	}
	if parsed.Size > maxContentsBytes {
		return nil, fmt.Errorf("file exceeds 1 MiB: %s", filePath)
	}
	raw := strings.ReplaceAll(parsed.Content, "\n", "")
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("github contents base64: %w", err)
	}
	if len(decoded) > maxContentsBytes {
		return nil, fmt.Errorf("file exceeds 1 MiB: %s", filePath)
	}
	return &FileBlob{Path: filePath, SHA: parsed.SHA, Content: decoded}, nil
}

// PutContents writes a file via the Contents API using optimistic concurrency on blob SHA.
// A SHA mismatch answers 409/422 — callers must not force-overwrite.
func (g *GitHubClient) PutContents(repo, branch, filePath, message, sha string, content []byte) (newSHA string, err error) {
	if branch == "" {
		branch = "main"
	}
	if strings.TrimSpace(message) == "" {
		return "", fmt.Errorf("commit message required")
	}
	if strings.TrimSpace(sha) == "" {
		return "", fmt.Errorf("blob SHA required (refusing create-without-sha)")
	}
	if len(content) > maxContentsBytes {
		return "", fmt.Errorf("file exceeds 1 MiB: %s", filePath)
	}
	url, err := g.contentsURL(repo, filePath, "")
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(putContentsBody{
		Message: message,
		Content: base64.StdEncoding.EncodeToString(content),
		SHA:     sha,
		Branch:  branch,
	})
	if err != nil {
		return "", err
	}
	req, err := g.newContentsRequest(http.MethodPut, url, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	res, err := g.httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20+1))
	if res.StatusCode == http.StatusConflict || res.StatusCode == http.StatusUnprocessableEntity {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 280 {
			snippet = snippet[:280] + "…"
		}
		return "", fmt.Errorf(
			"github contents-write %s/%s: SHA conflict (le fichier a changé sur GitHub, pas d'écrasement). %s",
			repo, filePath, snippet,
		)
	}
	if res.StatusCode >= 300 {
		return "", formatGitHubHTTPError("contents-write", repo+"/"+filePath, res.StatusCode, body)
	}
	var parsed putContentsJSON
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("github contents-write json: %w", err)
	}
	if parsed.Content == nil || parsed.Content.SHA == "" {
		return "", fmt.Errorf("github contents-write: missing blob sha in response")
	}
	return parsed.Content.SHA, nil
}

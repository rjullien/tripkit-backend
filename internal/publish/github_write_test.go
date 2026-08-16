package publish

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFormatGitHubHTTPError_403Write(t *testing.T) {
	err := formatGitHubHTTPError("contents-write", "rjullien/tripkit-seeds/quebec-2026.js", 403, []byte(`{"message":"Resource not accessible by personal access token"}`))
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, needle := range []string{"403", "Contents:write", "tripkit-seeds", "Resource not accessible"} {
		if !strings.Contains(msg, needle) {
			t.Fatalf("missing %q in %q", needle, msg)
		}
	}
}

func TestGetContents_DecodesBase64AndSHA(t *testing.T) {
	raw := []byte("var SEED_X = { trip: { id: \"x\" } };\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method=%s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/contents/quebec-2026.js") {
			t.Errorf("path=%s", r.URL.Path)
		}
		if r.URL.Query().Get("ref") != "main" {
			t.Errorf("ref=%s", r.URL.Query().Get("ref"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sha":      "blobsha1",
			"encoding": "base64",
			"content":  base64.StdEncoding.EncodeToString(raw),
			"size":     len(raw),
			"path":     "quebec-2026.js",
		})
	}))
	defer srv.Close()

	g := &GitHubClient{Token: "tok", Client: srv.Client(), BaseURL: srv.URL}
	blob, err := g.GetContents("rjullien/tripkit-seeds", "main", "quebec-2026.js")
	if err != nil {
		t.Fatal(err)
	}
	if blob.SHA != "blobsha1" {
		t.Fatalf("sha=%q", blob.SHA)
	}
	if string(blob.Content) != string(raw) {
		t.Fatalf("content=%q", blob.Content)
	}
}

func TestPutContents_OptimisticSHA(t *testing.T) {
	var gotBody putContentsBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method=%s", r.Method)
		}
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &gotBody); err != nil {
			t.Errorf("body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": map[string]any{"sha": "newblob"},
			"commit":  map[string]any{"sha": "commit1"},
		})
	}))
	defer srv.Close()

	g := &GitHubClient{Token: "tok", Client: srv.Client(), BaseURL: srv.URL}
	sha, err := g.PutContents("rjullien/tripkit-seeds", "main", "quebec-2026.js", "feat(construction): set phase to 3", "oldsha", []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if sha != "newblob" {
		t.Fatalf("sha=%q", sha)
	}
	if gotBody.SHA != "oldsha" {
		t.Fatalf("sent sha=%q", gotBody.SHA)
	}
	decoded, err := base64.StdEncoding.DecodeString(gotBody.Content)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != "hello" {
		t.Fatalf("content=%q", decoded)
	}
}

func TestPutContents_SHAConflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"message":"is at sha abc"}`))
	}))
	defer srv.Close()

	g := &GitHubClient{Token: "tok", Client: srv.Client(), BaseURL: srv.URL}
	_, err := g.PutContents("rjullien/tripkit-seeds", "main", "quebec-2026.js", "msg", "stale", []byte("x"))
	if err == nil || !strings.Contains(err.Error(), "SHA conflict") {
		t.Fatalf("err=%v", err)
	}
}

func TestPutContents_RefusesMissingSHA(t *testing.T) {
	g := &GitHubClient{Token: "tok"}
	_, err := g.PutContents("rjullien/tripkit-seeds", "main", "quebec-2026.js", "msg", "", []byte("x"))
	if err == nil || !strings.Contains(err.Error(), "blob SHA") {
		t.Fatalf("err=%v", err)
	}
}

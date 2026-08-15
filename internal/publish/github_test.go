package publish

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func zipball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, body := range files {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSanitizeGitHubToken(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"  ghp_abc  \n", "ghp_abc"},
		{"\"github_pat_x\"", "github_pat_x"},
		{"'tok'", "tok"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := sanitizeGitHubToken(tc.in); got != tc.want {
			t.Fatalf("in=%q got=%q want=%q", tc.in, got, tc.want)
		}
	}
}

func TestFormatGitHubHTTPError_401(t *testing.T) {
	err := formatGitHubHTTPError("zipball", "rjullien/tripkit-seeds", 401, []byte(`{"message":"Bad credentials"}`))
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, needle := range []string{"401", "github-token", "Infisical", "Bad credentials"} {
		if !strings.Contains(msg, needle) {
			t.Fatalf("missing %q in %q", needle, msg)
		}
	}
}

func TestExtractAllowlisted_RequiresMissing(t *testing.T) {
	raw := zipball(t, map[string]string{"owner-repo-sha/manifest.json": "{}"})
	_, err := ExtractAllowlisted(raw, []string{"manifest.json", "travel-profile.js"})
	if err == nil || !strings.Contains(err.Error(), "travel-profile.js") {
		t.Fatalf("err=%v", err)
	}
}

func TestExtractOptional_SkipsMissing(t *testing.T) {
	raw := zipball(t, map[string]string{"owner-repo-sha/manifest.json": "{}"})
	tree, err := ExtractOptional(raw, []string{"manifest.json", "travel-profile.js"})
	if err != nil {
		t.Fatal(err)
	}
	if string(tree["manifest.json"]) != "{}" {
		t.Fatalf("%q", tree["manifest.json"])
	}
	if _, ok := tree["travel-profile.js"]; ok {
		t.Fatal("optional missing file must be absent, not an error")
	}
}

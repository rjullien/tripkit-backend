package publish

import (
	"strings"
	"testing"
)

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

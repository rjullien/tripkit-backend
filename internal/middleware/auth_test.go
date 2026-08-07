package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// serviceToken is a plausible machine credential (>= 16 chars, like the output
// of `openssl rand -hex 32`).
const serviceToken = "0123456789abcdef0123456789abcdef"

// authOutcome records what Auth put in the request context.
type authOutcome struct {
	status  int
	reached bool
	user    string
	role    string
}

// clearAuthEnv resets every env var Auth depends on, so a test never inherits
// the environment of the test runner. t.Setenv restores the previous value.
func clearAuthEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"TRIPKIT_API_TOKEN",
		"TRIPKIT_JWT_SECRET",
		"TRIPKIT_SERVICE_TOKENS",
		"TRIPKIT_ACL_MODE",
		"TRIPKIT_ENV",
		"TRIPKIT_ADMIN_USERS",
	} {
		t.Setenv(key, "")
	}
}

// doAuth runs a request through Auth alone. An empty bearer or remoteUser omits
// the corresponding header.
func doAuth(t *testing.T, bearer, remoteUser string) authOutcome {
	t.Helper()
	out := authOutcome{}
	h := Auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out.reached = true
		out.user = GetAuthUser(r)
		out.role = GetAuthRole(r)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/trips", nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if remoteUser != "" {
		req.Header.Set("Remote-User", remoteUser)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	out.status = w.Code
	return out
}

func TestAuth_DevModeGrantsAdminWhenNothingConfigured(t *testing.T) {
	clearAuthEnv(t)

	got := doAuth(t, "", "")
	if !got.reached || got.status != http.StatusOK {
		t.Fatalf("expected dev mode to let the request pass, got status=%d reached=%v", got.status, got.reached)
	}
	if got.user != "dev" || got.role != RoleAdmin {
		t.Errorf("expected user=dev role=admin, got user=%q role=%q", got.user, got.role)
	}
}

func TestAuth_StrictModeRefusesDevBypass(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("TRIPKIT_ACL_MODE", "strict")

	// No credential configured and no Authelia header: refuse instead of
	// granting admin to everyone.
	got := doAuth(t, "", "")
	if got.status != http.StatusUnauthorized || got.reached {
		t.Fatalf("expected 401 without Remote-User, got status=%d reached=%v", got.status, got.reached)
	}

	// Authelia forwardAuth keeps working, as a viewer subject to the trip ACL.
	got = doAuth(t, "", "nicole")
	if got.status != http.StatusOK || !got.reached {
		t.Fatalf("expected 200 with Remote-User, got status=%d reached=%v", got.status, got.reached)
	}
	if got.user != "nicole" || got.role != RoleViewer {
		t.Errorf("expected user=nicole role=viewer, got user=%q role=%q", got.user, got.role)
	}
}

func TestAuth_ServiceTokenIsNonAdminIdentity(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("TRIPKIT_SERVICE_TOKENS", "Nadia:"+serviceToken)

	got := doAuth(t, serviceToken, "")
	if got.status != http.StatusOK || !got.reached {
		t.Fatalf("expected the service token to authenticate, got status=%d reached=%v", got.status, got.reached)
	}
	if got.user != "nadia" {
		t.Errorf("expected user=nadia (lowercased), got %q", got.user)
	}
	if got.role != RoleService {
		t.Errorf("expected role=service, got %q", got.role)
	}
	if got.role == RoleAdmin {
		t.Error("a service token must never grant role=admin")
	}
}

// TestAuth_ServiceTokenIdentityWinsOverRemoteUser guards against a CI job
// forging an admin identity with a header.
func TestAuth_ServiceTokenIdentityWinsOverRemoteUser(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("TRIPKIT_SERVICE_TOKENS", "nadia:"+serviceToken)

	got := doAuth(t, serviceToken, "rene")
	if got.user != "nadia" || got.role != RoleService {
		t.Fatalf("expected user=nadia role=service, got user=%q role=%q", got.user, got.role)
	}
}

func TestAuth_UnknownBearerTokenIsRejected(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("TRIPKIT_SERVICE_TOKENS", "nadia:"+serviceToken)

	got := doAuth(t, "not-the-configured-token", "")
	if got.status != http.StatusUnauthorized || got.reached {
		t.Fatalf("expected 401 for an unknown token, got status=%d reached=%v", got.status, got.reached)
	}

	// A forged Remote-User does not rescue an invalid token either.
	got = doAuth(t, "not-the-configured-token", "rene")
	if got.status != http.StatusUnauthorized || got.reached {
		t.Fatalf("expected 401 for an unknown token with Remote-User, got status=%d reached=%v", got.status, got.reached)
	}
}

// TestAuth_ServiceTokenForAdminUsernameIsIgnored: a service token must never be
// a way to authenticate as an admin, so such an entry is dropped entirely.
func TestAuth_ServiceTokenForAdminUsernameIsIgnored(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("TRIPKIT_ADMIN_USERS", "admin,rene")
	t.Setenv("TRIPKIT_SERVICE_TOKENS", "rene:"+serviceToken)
	// A JWT secret is configured so dev mode is not what rejects the request.
	t.Setenv("TRIPKIT_JWT_SECRET", "test-secret")

	got := doAuth(t, serviceToken, "")
	if got.status != http.StatusUnauthorized || got.reached {
		t.Fatalf("expected 401 for a service token owned by an admin, got status=%d reached=%v", got.status, got.reached)
	}
}

func TestAuth_ShortServiceTokenIsIgnored(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("TRIPKIT_SERVICE_TOKENS", "nadia:tooshort")
	t.Setenv("TRIPKIT_JWT_SECRET", "test-secret")

	got := doAuth(t, "tooshort", "")
	if got.status != http.StatusUnauthorized || got.reached {
		t.Fatalf("expected 401 for a token shorter than 16 chars, got status=%d reached=%v", got.status, got.reached)
	}
}

func TestAuth_MalformedServiceTokenEntriesAreSkipped(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("TRIPKIT_SERVICE_TOKENS", "no-colon-here, nadia:"+serviceToken+" ,:orphan-token-value")
	t.Setenv("TRIPKIT_JWT_SECRET", "test-secret")

	// The valid entry in the middle is still usable.
	if got := doAuth(t, serviceToken, ""); got.user != "nadia" || got.role != RoleService {
		t.Fatalf("expected the valid entry to survive, got user=%q role=%q status=%d", got.user, got.role, got.status)
	}
	if got := doAuth(t, "orphan-token-value", ""); got.status != http.StatusUnauthorized {
		t.Errorf("expected 401 for an entry without username, got %d", got.status)
	}
	if got := doAuth(t, "no-colon-here", ""); got.status != http.StatusUnauthorized {
		t.Errorf("expected 401 for an entry without ':', got %d", got.status)
	}
}

func TestAuth_StaticAdminTokenStillGrantsAdmin(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("TRIPKIT_API_TOKEN", "static-admin-token")
	t.Setenv("TRIPKIT_SERVICE_TOKENS", "nadia:"+serviceToken)

	got := doAuth(t, "static-admin-token", "")
	if got.status != http.StatusOK || !got.reached {
		t.Fatalf("expected the static token to authenticate, got status=%d reached=%v", got.status, got.reached)
	}
	if got.user != "admin" || got.role != RoleAdmin {
		t.Errorf("expected user=admin role=admin, got user=%q role=%q", got.user, got.role)
	}
}

// TestAuth_ServiceTokensDisableDevMode: configuring only service tokens must be
// enough to leave dev mode, otherwise every request would still be admin.
func TestAuth_ServiceTokensDisableDevMode(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("TRIPKIT_SERVICE_TOKENS", "nadia:"+serviceToken)

	got := doAuth(t, "", "")
	if got.status != http.StatusUnauthorized || got.reached {
		t.Fatalf("expected 401 without any credential, got status=%d reached=%v", got.status, got.reached)
	}
}

#!/usr/bin/env bash
# Test a token the same way the TripKit publish worker does (zipball + manifest).
# Usage:
#   TRIPKIT_GITHUB_TOKEN=ghp_xxx ./scripts/test-github-publish-token.sh
#   ./scripts/test-github-publish-token.sh ghp_xxx
set -euo pipefail

TOKEN="${1:-${TRIPKIT_GITHUB_TOKEN:-}}"
# Trim whitespace / accidental quotes (same as BE sanitizeGitHubToken).
TOKEN="$(printf '%s' "$TOKEN" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' -e 's/^"//' -e 's/"$//' -e "s/^'//" -e "s/'$//")"

if [[ -z "$TOKEN" ]]; then
  echo "FAIL: no token. Export TRIPKIT_GITHUB_TOKEN or pass it as argv[1]." >&2
  exit 2
fi

PREFIX="$(printf '%s' "$TOKEN" | head -c 7)"
LEN=${#TOKEN}
echo "== TripKit publish GitHub token check =="
echo "token_prefix=${PREFIX}…  length=${LEN}"

API="${GITHUB_API:-https://api.github.com}"
UA="tripkit-publish-token-test"
AUTH="Authorization: Bearer ${TOKEN}"
VER="X-GitHub-Api-Version: 2022-11-28"

# /user is optional — fine-grained PATs / GITHUB_TOKEN often cannot call it.
echo
echo "-- /user (optional)"
u_code=$(curl -sS -o /tmp/tk-user.json -w '%{http_code}' \
  -H "$AUTH" -H "Accept: application/vnd.github+json" -H "$VER" -H "User-Agent: $UA" \
  "${API}/user" || echo "000")
echo "HTTP ${u_code}"
if [[ "$u_code" == "200" ]]; then
  python3 - <<'PY'
import json
u=json.load(open("/tmp/tk-user.json"))
print(f"login={u.get('login')} type={u.get('type')} id={u.get('id')}")
PY
elif [[ "$u_code" == "401" ]]; then
  echo "body:"
  head -c 400 /tmp/tk-user.json; echo
  echo "FAIL: 401 Bad credentials — token invalide/expiré/révoqué (ou guillemets dans Infisical)."
  exit 1
else
  echo "(skip identity — HTTP ${u_code} is OK for some token types)"
  head -c 200 /tmp/tk-user.json 2>/dev/null; echo
fi

fail=0

check_zipball() {
  local repo="$1" ref="${2:-main}"
  local url="${API}/repos/${repo}/zipball/${ref}"
  echo
  echo "-- zipball ${repo}@${ref}"
  local code
  code=$(curl -sS -o /tmp/tk-zipball.bin -w '%{http_code}' \
    -H "$AUTH" -H "Accept: application/vnd.github+json" -H "$VER" -H "User-Agent: $UA" \
    -L --max-time 60 "$url" || echo "000")
  local ctype size
  ctype=$(file -b /tmp/tk-zipball.bin 2>/dev/null || true)
  size=$(wc -c </tmp/tk-zipball.bin | tr -d ' ')
  echo "HTTP ${code}  bytes=${size}  type=${ctype}"
  if [[ "$code" == "401" ]]; then
    head -c 400 /tmp/tk-zipball.bin; echo
    echo "FAIL zipball: Bad credentials"
    return 1
  fi
  if [[ "$code" != "200" ]]; then
    head -c 400 /tmp/tk-zipball.bin; echo
    return 1
  fi
  if ! printf '%s' "$ctype" | grep -qiE 'zip|archive'; then
    echo "WARN: not a zip"
    head -c 200 /tmp/tk-zipball.bin; echo
    return 1
  fi
  echo "OK zipball"
}

check_manifest() {
  local repo="$1" ref="${2:-main}"
  local url="${API}/repos/${repo}/contents/publish-manifest.json?ref=${ref}"
  echo
  echo "-- contents ${repo}@${ref}:publish-manifest.json"
  local code
  code=$(curl -sS -o /tmp/tk-manifest.bin -w '%{http_code}' \
    -H "$AUTH" -H "Accept: application/vnd.github.raw" -H "$VER" -H "User-Agent: $UA" \
    --max-time 30 "$url" || echo "000")
  echo "HTTP ${code}"
  if [[ "$code" != "200" ]]; then
    head -c 400 /tmp/tk-manifest.bin; echo
    return 1
  fi
  head -c 180 /tmp/tk-manifest.bin; echo
  echo "OK manifest"
}

# Required for dogfood Jullien publish
check_zipball "rjullien/tripkit-seeds" main || fail=1
check_manifest "rjullien/tripkit-seeds" main || fail=1

# Optional family repos (403/404 = PAT not granted yet — warn only)
for repo in "rjullien/tripkit-seeds-nadia" "rjullien/tripkit-seeds-laurine"; do
  if ! check_zipball "$repo" main; then
    echo "WARN: ${repo} not readable with this token (ok if not onboarded yet)"
  else
    check_manifest "$repo" main || echo "WARN: no publish-manifest.json in ${repo}"
  fi
done

echo
if [[ "$fail" -eq 0 ]]; then
  echo "ALL OK — token can fetch rjullien/tripkit-seeds zipball + publish-manifest.json"
  echo "If prod still 401: Infisical /tripkit → github-token not synced, or pod not restarted."
  exit 0
fi
echo "FAIL on tripkit-seeds — this token cannot power publish."
echo "Hints:"
echo "  - 401 → recreate PAT; Infisical key must be exactly github-token (no quotes)"
echo "  - 403/404 → fine-grained: Contents:Read on rjullien/tripkit-seeds (private)"
echo "  - After Infisical update → sync tripkit-secrets + kubectl rollout restart deploy/tripkit-backend -n tripkit"
exit 1

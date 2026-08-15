package construction

import "testing"

// TestParseConfigJSON_RealOpsFile parses the shape actually committed in
// rjullien/tripkit ops/construction.json.
func TestParseConfigJSON_RealOpsFile(t *testing.T) {
	raw := []byte(`{
  "enabled": true,
  "bifrostBaseUrl": "http://bifrost.openclaw.svc.cluster.local:8080/v1",
  "models": {
    "adminCheck": "opencode-go/deepseek-v4-flash",
    "healthCheck": "opencode-go/deepseek-v4-flash",
    "nuisance": "opencode-go/deepseek-v4-pro",
    "discoveryRank": "opencode-go/deepseek-v4-flash"
  },
  "leoModes": ["construction:ideation"],
  "qa": { "driveHardLimitMinutes": 480, "corridorSampleKm": 40 },
  "formalities": { "overrides": {} }
}`)

	cfg, err := parseConfigJSON(raw)
	if err != nil {
		t.Fatalf("parseConfigJSON: %v", err)
	}
	if !cfg.Ready() {
		t.Error("config should be ready")
	}
	if got := cfg.ModelFor("nuisance"); got != "opencode-go/deepseek-v4-pro" {
		t.Errorf("nuisance model = %q", got)
	}
	if got := cfg.ModelFor("adminCheck"); got != "opencode-go/deepseek-v4-flash" {
		t.Errorf("adminCheck model = %q", got)
	}
	if cfg.QA.DriveHardLimitMinutes != 480 {
		t.Errorf("driveHardLimitMinutes = %d", cfg.QA.DriveHardLimitMinutes)
	}
}

// TestParseConfigJSON_MissingBifrostURL: without an endpoint there is nothing to
// call, so the file is rejected and the caller falls back to dogfood.
func TestParseConfigJSON_MissingBifrostURL(t *testing.T) {
	if _, err := parseConfigJSON([]byte(`{"enabled": true}`)); err == nil {
		t.Fatal("want an error when bifrostBaseUrl is missing")
	}
}

// TestParseConfigJSON_EnabledDefaultsTrue mirrors the pluschat/dailybrief rule:
// an omitted "enabled" means on, an explicit false means off.
func TestParseConfigJSON_EnabledDefaultsTrue(t *testing.T) {
	cfg, err := parseConfigJSON([]byte(`{"bifrostBaseUrl": "http://x/v1"}`))
	if err != nil {
		t.Fatalf("parseConfigJSON: %v", err)
	}
	if !cfg.Enabled {
		t.Error("omitted enabled should default to true")
	}

	cfg, err = parseConfigJSON([]byte(`{"enabled": false, "bifrostBaseUrl": "http://x/v1"}`))
	if err != nil {
		t.Fatalf("parseConfigJSON: %v", err)
	}
	if cfg.Enabled {
		t.Error("explicit false must be honoured")
	}
	if cfg.Ready() {
		t.Error("a disabled config is not ready")
	}
}

// TestParseConfigJSON_ZeroThresholdsFallBack: a zero driveHardLimitMinutes would
// make every single day a QA violation, so it falls back to the dogfood value
// instead of being taken literally.
func TestParseConfigJSON_ZeroThresholdsFallBack(t *testing.T) {
	cfg, err := parseConfigJSON([]byte(`{"bifrostBaseUrl": "http://x/v1", "qa": {}}`))
	if err != nil {
		t.Fatalf("parseConfigJSON: %v", err)
	}
	if cfg.QA.DriveHardLimitMinutes != defaultDriveHardLimitMinutes {
		t.Errorf("driveHardLimitMinutes = %d, want fallback %d", cfg.QA.DriveHardLimitMinutes, defaultDriveHardLimitMinutes)
	}
	if cfg.QA.CorridorSampleKm != defaultCorridorSampleKm {
		t.Errorf("corridorSampleKm = %v, want fallback %v", cfg.QA.CorridorSampleKm, defaultCorridorSampleKm)
	}
	if len(cfg.LeoModes) == 0 {
		t.Error("leoModes should fall back to the dogfood allowlist")
	}
}

// TestModelFor_UnknownFeatureIsEmpty guards against a typo silently returning a
// model that would then be billed against the wrong feature.
func TestModelFor_UnknownFeatureIsEmpty(t *testing.T) {
	cfg := DefaultConfig()
	if got := cfg.ModelFor("nope"); got != "" {
		t.Errorf("ModelFor(unknown) = %q, want empty", got)
	}
}

// TestModelFor_PartialOpsFileFallsBack: a half-filled models block must not
// silence a check.
func TestModelFor_PartialOpsFileFallsBack(t *testing.T) {
	cfg, err := parseConfigJSON([]byte(`{
		"bifrostBaseUrl": "http://x/v1",
		"models": { "adminCheck": "custom/model" }
	}`))
	if err != nil {
		t.Fatalf("parseConfigJSON: %v", err)
	}
	if got := cfg.ModelFor("adminCheck"); got != "custom/model" {
		t.Errorf("adminCheck = %q, want the ops value", got)
	}
	if got := cfg.ModelFor("nuisance"); got != defaultNuisanceModel {
		t.Errorf("nuisance = %q, want dogfood fallback %q", got, defaultNuisanceModel)
	}
}

// TestNilLoaderIsSafe: Bootstrap/Get on a nil loader return dogfood rather than
// panicking, matching the other ops loaders.
func TestNilLoaderIsSafe(t *testing.T) {
	var l *Loader
	if cfg := l.Bootstrap(); cfg.BifrostBaseURL == "" {
		t.Error("nil loader Bootstrap should return dogfood config")
	}
	if cfg := l.Get(); cfg.BifrostBaseURL == "" {
		t.Error("nil loader Get should return dogfood config")
	}
}

package config

import "testing"

func TestACLStrict(t *testing.T) {
	cases := []struct {
		name    string
		aclMode string
		env     string
		want    bool
	}{
		{"nothing configured keeps legacy open mode", "", "", false},
		{"explicit strict", "strict", "", true},
		{"explicit strict is trimmed and case-insensitive", "  Strict ", "production", true},
		{"explicit open wins over env", "open", "production", false},
		{"production env implies strict", "", "production", true},
		{"dev env stays open", "", "dev", false},
		{"unknown acl mode falls back to env", "maybe", "production", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("TRIPKIT_ACL_MODE", c.aclMode)
			t.Setenv("TRIPKIT_ENV", c.env)
			if got := ACLStrict(); got != c.want {
				t.Errorf("ACLStrict() = %v, want %v (TRIPKIT_ACL_MODE=%q TRIPKIT_ENV=%q)", got, c.want, c.aclMode, c.env)
			}
		})
	}
}

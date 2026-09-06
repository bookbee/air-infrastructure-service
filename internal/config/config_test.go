package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bookbee/air-infrastructure-service/internal/constants"
)

func TestParseServiceTokens(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want map[string]string
	}{
		{"empty", "", map[string]string{}},
		{"single", "t-a:air-classifier", map[string]string{"t-a": "air-classifier"}},
		{
			"the .env.example value",
			"air-classifier-dev:air-classifier,air-platform-dev:air-platform,air-client-dev:air-client",
			map[string]string{
				"air-classifier-dev": "air-classifier",
				"air-platform-dev":   "air-platform",
				"air-client-dev":     "air-client",
			},
		},
		{"whitespace tolerated", " t-a : air-classifier , t-b : air-client ",
			map[string]string{"t-a": "air-classifier", "t-b": "air-client"}},
		{"empty entries skipped", "t-a:air-classifier,,", map[string]string{"t-a": "air-classifier"}},
		{"half a pair is skipped, not fatal", "t-a:air-classifier,broken",
			map[string]string{"t-a": "air-classifier"}},
		{"missing service is skipped", "t-a:air-classifier,t-b:",
			map[string]string{"t-a": "air-classifier"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseServiceTokens(tc.raw)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("token %q: got %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestDefaults(t *testing.T) {
	s, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Environment != constants.EnvLocal {
		t.Errorf("environment: got %q, want local", s.Environment)
	}
	if s.API.Port != 8080 {
		t.Errorf("api port: got %d, want 8080", s.API.Port)
	}
	if s.Postgres.Port != 6432 {
		t.Errorf("postgres port: got %d, want 6432 (pgbouncer)", s.Postgres.Port)
	}
	if s.Policy.DefaultRatePerMinute != 120 {
		t.Errorf("default rate: got %d, want 120", s.Policy.DefaultRatePerMinute)
	}
}

func TestEnvironmentBeatsDotenv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	write(t, path, "AIR_INFRASTRUCTURE_SERVICE__API__PORT=9999\nAIR_INFRASTRUCTURE_SERVICE__REDIS__URL=redis://from-file:6379/0\n")

	t.Setenv("AIR_INFRASTRUCTURE_SERVICE__API__PORT", "8123")

	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.API.Port != 8123 {
		t.Errorf("exported var should win: got %d, want 8123", s.API.Port)
	}
	if s.Redis.URL != "redis://from-file:6379/0" {
		t.Errorf("file value should apply when unexported: got %q", s.Redis.URL)
	}
}

func TestMissingDotenvIsNotAnError(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "absent.env")); err != nil {
		t.Fatalf("a missing .env must not fail the boot: %v", err)
	}
}

func TestTrailingCommentIsNotPartOfTheValue(t *testing.T) {
	// .env.example documents several settings with a trailing comment; treating
	// it as data would silently misconfigure the logger and the environment.
	path := filepath.Join(t.TempDir(), ".env")
	write(t, path, "AIR_INFRASTRUCTURE_SERVICE__LOG__RENDER=json        # console | json (json in prod)\n"+
		"AIR_INFRASTRUCTURE_SERVICE__ENVIRONMENT=local          # local | staging | prod\n")

	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Log.Render != "json" {
		t.Errorf("render: got %q, want json", s.Log.Render)
	}
	if s.Environment != constants.EnvLocal {
		t.Errorf("environment: got %q, want local", s.Environment)
	}
}

func TestQuotedValueKeepsItsHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	write(t, path, `AIR_INFRASTRUCTURE_SERVICE__POSTGRES__PASSWORD="p#ss word"`+"\n")

	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Postgres.Password != "p#ss word" {
		t.Errorf("password: got %q, want %q", s.Postgres.Password, "p#ss word")
	}
}

func TestInvalidValuesRefuseToBoot(t *testing.T) {
	cases := map[string]string{
		"AIR_INFRASTRUCTURE_SERVICE__ENVIRONMENT":              "production",
		"AIR_INFRASTRUCTURE_SERVICE__LOG__RENDER":              "pretty",
		"AIR_INFRASTRUCTURE_SERVICE__SECURITY__SECRET_BACKEND": "kms",
		"AIR_INFRASTRUCTURE_SERVICE__API__PORT":                "eighty-eighty",
	}
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, bad)
			if _, err := Load(""); err == nil {
				t.Fatalf("%s=%q should have failed the boot", name, bad)
			}
		})
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

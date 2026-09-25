// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

// base is the minimum that must be present for Load to succeed, so each test
// can vary one thing.
func base() map[string]string {
	return map[string]string{
		"MIGRATE_TRUSTED_PROXY": "172.18.0.0/16",
		"MIGRATE_TOKEN_KEY":     "0123456789abcdef0123456789abcdef",
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(base())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != DefaultAddr {
		t.Errorf("Addr = %q, want %q", cfg.Addr, DefaultAddr)
	}
	if cfg.DataDir != DefaultDataDir {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, DefaultDataDir)
	}
	if cfg.StagingRetention != DefaultRetentionDays*24*time.Hour {
		t.Errorf("StagingRetention = %v", cfg.StagingRetention)
	}
	if cfg.MaxConcurrent != 1 {
		t.Errorf("MaxConcurrent = %d, want 1", cfg.MaxConcurrent)
	}
}

func TestLoadRefusesPublicBindWithoutSecret(t *testing.T) {
	env := base()
	env["MIGRATE_ALLOW_PUBLIC_BIND"] = "true"

	_, err := Load(env)
	if err == nil {
		t.Fatal("expected an error for a public bind with no proxy secret")
	}
	if !strings.Contains(err.Error(), "MIGRATE_PROXY_SECRET") {
		t.Errorf("error should name the missing variable, got: %v", err)
	}
}

func TestLoadRefusesMissingTrustedProxy(t *testing.T) {
	env := base()
	delete(env, "MIGRATE_TRUSTED_PROXY")

	_, err := Load(env)
	if err == nil {
		t.Fatal("expected an error when no trusted proxy is configured")
	}
	if !strings.Contains(err.Error(), "MIGRATE_TRUSTED_PROXY") {
		t.Errorf("error should name the variable, got: %v", err)
	}
}

func TestLoadRefusesMissingTokenKey(t *testing.T) {
	env := base()
	delete(env, "MIGRATE_TOKEN_KEY")

	_, err := Load(env)
	if err == nil {
		t.Fatal("expected an error when the token key is missing")
	}
	if !strings.Contains(err.Error(), "MIGRATE_TOKEN_KEY") {
		t.Errorf("error should name the variable, got: %v", err)
	}
}

// Load must report every problem at once: fixing one variable per restart is
// the thing this behaviour exists to avoid.
func TestLoadReportsEveryProblemAtOnce(t *testing.T) {
	env := map[string]string{
		"MIGRATE_ALLOW_PUBLIC_BIND":      "true",
		"MIGRATE_MAX_CONCURRENT":         "zero",
		"MIGRATE_STAGING_RETENTION_DAYS": "-3",
	}

	_, err := Load(env)
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{
		"MIGRATE_PROXY_SECRET",
		"MIGRATE_TRUSTED_PROXY",
		"MIGRATE_TOKEN_KEY",
		"MIGRATE_MAX_CONCURRENT",
		"MIGRATE_STAGING_RETENTION_DAYS",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the combined error should mention %s; got:\n%v", want, err)
		}
	}
}

func TestLoadParsesDurationsAndLevel(t *testing.T) {
	env := base()
	env["MIGRATE_STAGING_RETENTION_DAYS"] = "30"
	env["MIGRATE_MAX_CONCURRENT"] = "2"
	env["MIGRATE_LOG_LEVEL"] = "debug"

	cfg, err := Load(env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.StagingRetention != 30*24*time.Hour {
		t.Errorf("StagingRetention = %v", cfg.StagingRetention)
	}
	if cfg.MaxConcurrent != 2 {
		t.Errorf("MaxConcurrent = %d", cfg.MaxConcurrent)
	}
}

func TestWarningsNameWhatWillNotWork(t *testing.T) {
	cfg, err := Load(base())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	warnings := strings.Join(cfg.Warnings(), "\n")
	for _, want := range []string{
		"Google OAuth",
		"TAKEOUT_SHARE_ACCOUNT",
		"Nextcloud",
		"Immich",
	} {
		if !strings.Contains(warnings, want) {
			t.Errorf("warnings should mention %q; got:\n%s", want, warnings)
		}
	}
}

func TestResolveRealEnvironmentWinsOverFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/env"
	content := "MIGRATE_ADDR=1.2.3.4:9999\nMIGRATE_DATA_DIR=/from-file\n"
	if err := writeFile(path, content); err != nil {
		t.Fatalf("write: %v", err)
	}

	t.Setenv("MIGRATE_CONFIG", path)
	t.Setenv("MIGRATE_ADDR", "127.0.0.1:1111")

	env, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if env["MIGRATE_ADDR"] != "127.0.0.1:1111" {
		t.Errorf("the real environment should win, got %q", env["MIGRATE_ADDR"])
	}
	if env["MIGRATE_DATA_DIR"] != "/from-file" {
		t.Errorf("the file value should survive when not overridden, got %q", env["MIGRATE_DATA_DIR"])
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

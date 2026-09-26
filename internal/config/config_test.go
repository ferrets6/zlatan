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
		"ZLATAN_TRUSTED_PROXY": "172.18.0.0/16",
		"ZLATAN_TOKEN_KEY":     "0123456789abcdef0123456789abcdef",
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
	env["ZLATAN_ALLOW_PUBLIC_BIND"] = "true"

	_, err := Load(env)
	if err == nil {
		t.Fatal("expected an error for a public bind with no proxy secret")
	}
	if !strings.Contains(err.Error(), "ZLATAN_PROXY_SECRET") {
		t.Errorf("error should name the missing variable, got: %v", err)
	}
}

func TestLoadRefusesMissingTrustedProxy(t *testing.T) {
	env := base()
	delete(env, "ZLATAN_TRUSTED_PROXY")

	_, err := Load(env)
	if err == nil {
		t.Fatal("expected an error when no trusted proxy is configured")
	}
	if !strings.Contains(err.Error(), "ZLATAN_TRUSTED_PROXY") {
		t.Errorf("error should name the variable, got: %v", err)
	}
}

func TestLoadRefusesMissingTokenKey(t *testing.T) {
	env := base()
	delete(env, "ZLATAN_TOKEN_KEY")

	_, err := Load(env)
	if err == nil {
		t.Fatal("expected an error when the token key is missing")
	}
	if !strings.Contains(err.Error(), "ZLATAN_TOKEN_KEY") {
		t.Errorf("error should name the variable, got: %v", err)
	}
}

// Load must report every problem at once: fixing one variable per restart is
// the thing this behaviour exists to avoid.
func TestLoadReportsEveryProblemAtOnce(t *testing.T) {
	env := map[string]string{
		"ZLATAN_ALLOW_PUBLIC_BIND":      "true",
		"ZLATAN_MAX_CONCURRENT":         "zero",
		"ZLATAN_STAGING_RETENTION_DAYS": "-3",
	}

	_, err := Load(env)
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{
		"ZLATAN_PROXY_SECRET",
		"ZLATAN_TRUSTED_PROXY",
		"ZLATAN_TOKEN_KEY",
		"ZLATAN_MAX_CONCURRENT",
		"ZLATAN_STAGING_RETENTION_DAYS",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the combined error should mention %s; got:\n%v", want, err)
		}
	}
}

func TestLoadParsesDurationsAndLevel(t *testing.T) {
	env := base()
	env["ZLATAN_STAGING_RETENTION_DAYS"] = "30"
	env["ZLATAN_MAX_CONCURRENT"] = "2"
	env["ZLATAN_LOG_LEVEL"] = "debug"

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
		"ZLATAN_NEXTCLOUD_URL",
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
	content := "ZLATAN_ADDR=1.2.3.4:9999\nZLATAN_DATA_DIR=/from-file\n"
	if err := writeFile(path, content); err != nil {
		t.Fatalf("write: %v", err)
	}

	t.Setenv("ZLATAN_CONFIG", path)
	t.Setenv("ZLATAN_ADDR", "127.0.0.1:1111")

	env, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if env["ZLATAN_ADDR"] != "127.0.0.1:1111" {
		t.Errorf("the real environment should win, got %q", env["ZLATAN_ADDR"])
	}
	if env["ZLATAN_DATA_DIR"] != "/from-file" {
		t.Errorf("the file value should survive when not overridden, got %q", env["ZLATAN_DATA_DIR"])
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

func TestQuotaBudgetAndOverrides(t *testing.T) {
	base := map[string]string{
		"ZLATAN_TRUSTED_PROXY": "172.18.0.0/16",
		"ZLATAN_TOKEN_KEY":     "a-key",
	}

	t.Run("default budget", func(t *testing.T) {
		cfg, err := Load(base)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := cfg.Quota.BudgetGiBFor("anyone"); got != DefaultBudgetGiB {
			t.Errorf("budget = %d, want %d", got, DefaultBudgetGiB)
		}
	})

	t.Run("override wins for one person only", func(t *testing.T) {
		env := map[string]string{}
		for k, v := range base {
			env[k] = v
		}
		env["ZLATAN_QUOTA_BUDGET_GIB"] = "100"
		env["ZLATAN_QUOTA_OVERRIDES"] = "marco=200, federico=200"
		cfg, err := Load(env)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := cfg.Quota.BudgetGiBFor("marco"); got != 200 {
			t.Errorf("marco budget = %d, want 200", got)
		}
		if got := cfg.Quota.BudgetGiBFor("federico"); got != 200 {
			t.Errorf("federico budget = %d, want 200", got)
		}
		if got := cfg.Quota.BudgetGiBFor("someone-else"); got != 100 {
			t.Errorf("default budget = %d, want 100", got)
		}
	})

	t.Run("a malformed override is skipped, not fatal", func(t *testing.T) {
		env := map[string]string{}
		for k, v := range base {
			env[k] = v
		}
		env["ZLATAN_QUOTA_OVERRIDES"] = "marco=200,nonsense,,x=zero"
		cfg, err := Load(env)
		if err != nil {
			t.Fatalf("Load should not fail on a malformed override: %v", err)
		}
		if got := cfg.Quota.BudgetGiBFor("marco"); got != 200 {
			t.Errorf("marco budget = %d, want 200", got)
		}
		if got := cfg.Quota.BudgetGiBFor("x"); got != DefaultBudgetGiB {
			t.Errorf("x budget = %d, want the default", got)
		}
	})

	t.Run("a non-positive budget is a problem", func(t *testing.T) {
		env := map[string]string{}
		for k, v := range base {
			env[k] = v
		}
		env["ZLATAN_QUOTA_BUDGET_GIB"] = "0"
		if _, err := Load(env); err == nil {
			t.Error("Load should reject a zero budget")
		}
	})
}

func TestWebURLIsThePublicAddress(t *testing.T) {
	base := map[string]string{
		"ZLATAN_TRUSTED_PROXY": "172.18.0.0/16",
		"ZLATAN_TOKEN_KEY":     "a-key",
		"ZLATAN_NEXTCLOUD_URL": "http://nextcloud",
		"ZLATAN_IMMICH_URL":    "http://immich_server:2283",
	}

	t.Run("empty public URL means no link", func(t *testing.T) {
		cfg, err := Load(base)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := cfg.Nextcloud.WebURL(); got != "" {
			t.Errorf("WebURL = %q, want empty when no public URL is set", got)
		}
		if got := cfg.Immich.WebURL(); got != "" {
			t.Errorf("WebURL = %q, want empty when no public URL is set", got)
		}
	})

	t.Run("public URL wins and the internal one is never returned", func(t *testing.T) {
		env := map[string]string{}
		for k, v := range base {
			env[k] = v
		}
		env["ZLATAN_NEXTCLOUD_PUBLIC_URL"] = "https://cloud.example.org/"
		env["ZLATAN_IMMICH_PUBLIC_URL"] = "https://immich.example.org"
		cfg, err := Load(env)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		// The trailing slash is trimmed, so the link builder can append a path.
		if got := cfg.Nextcloud.WebURL(); got != "https://cloud.example.org" {
			t.Errorf("WebURL = %q, want the public URL without a trailing slash", got)
		}
		if got := cfg.Immich.WebURL(); got != "https://immich.example.org" {
			t.Errorf("WebURL = %q", got)
		}
	})

	t.Run("the public URL is warned about when missing", func(t *testing.T) {
		cfg, err := Load(base)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		joined := strings.Join(cfg.Warnings(), "\n")
		if !strings.Contains(joined, "ZLATAN_NEXTCLOUD_PUBLIC_URL") {
			t.Error("a missing Nextcloud public URL should be warned about")
		}
		if !strings.Contains(joined, "ZLATAN_IMMICH_PUBLIC_URL") {
			t.Error("a missing Immich public URL should be warned about")
		}
	})
}

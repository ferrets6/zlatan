// SPDX-License-Identifier: AGPL-3.0-or-later

// Package config loads Zlatan's configuration from the environment and from
// an optional env-style file. There is no interactive setup.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/marcodellemarche/zlatan/internal/core"
)

const (
	DefaultAddr    = "127.0.0.1:8080"
	DefaultDataDir = "/data"
	DefaultStaging = "/staging"

	// DefaultRetentionDays is how long a finished migration's staging is kept
	// before the purge. Long enough to answer "wait, did my video arrive?",
	// short enough not to hoard a hundred gigabytes.
	DefaultRetentionDays = 14

	// DefaultTakeoutFolder is the folder Google Takeout creates in Drive when
	// the person chooses "Add to Drive".
	DefaultTakeoutFolder = "Takeout"

	// DefaultTakeoutPoll is how often the Takeout watcher looks for the
	// folder. Takeout takes hours to days, so a slow poll costs nothing and a
	// fast one would only spend quota.
	DefaultTakeoutPoll = 10 * time.Minute

	// DefaultTakeoutMaxWait is how long the watcher keeps looking before it
	// gives up and tells the person to use the upload route instead. Google's
	// own email says the archive link is valid for about 7 days.
	DefaultTakeoutMaxWait = 7 * 24 * time.Hour
)

// Config is the whole of Zlatan's configuration. Every secret is a
// core.Secret, so printing the struct cannot leak one.
type Config struct {
	Addr            string
	AllowPublicBind bool
	DataDir         string
	StagingDir      string
	LogLevel        slog.Level

	// ProxySecret is a header Caddy injects on the way in. The service shares
	// a Docker network with every other container, so without this any of them
	// could reach it directly and skip the SSO in front of the public name.
	// Empty means no gate.
	ProxySecret core.Secret

	// TrustedProxy is the network the forward-auth header (Remote-User) may
	// come from. A Remote-User header is believed only when the request
	// arrives from it, so a container on the same network cannot claim to be
	// somebody else. Empty means no header is trusted, and every request is
	// refused — the safe default.
	TrustedProxy string

	// TokenKey seals OAuth refresh tokens at rest. Losing it makes every
	// stored token unreadable, so the running migrations must be re-authorised.
	TokenKey core.Secret

	Google    Google
	Nextcloud Nextcloud
	Immich    Immich

	// StagingRetention is how long a completed migration's staging is kept.
	StagingRetention time.Duration

	// MaxConcurrent is how many heavy migrations may run at once. One on this
	// single-node homelab: immich-go and Immich's own jobs already saturate
	// the CPU, and the memory limits are deliberate.
	MaxConcurrent int

	// TakeoutPoll is how often the watcher looks for the Takeout folder, and
	// TakeoutMaxWait how long it keeps looking before giving up.
	TakeoutPoll    time.Duration
	TakeoutMaxWait time.Duration
}

// Google is the OAuth client dedicated to this service, plus the family
// account people share their Takeout with. It is deliberately separate from
// Nextcloud's integration_google client: different scopes, different blast
// radius.
type Google struct {
	ClientID     core.Secret
	ClientSecret core.Secret
	RedirectURL  string

	// TakeoutFolder is the name of the folder Google Takeout creates in the
	// person's Drive when they pick "Add to Drive". Zlatan watches for a folder
	// with this name under the account it already has read access to, so no
	// separate share step and no central account are involved.
	TakeoutFolder string
}

// Configured reports whether the OAuth client is usable.
func (g Google) Configured() bool {
	return !g.ClientID.Empty() && !g.ClientSecret.Empty() && g.RedirectURL != ""
}

// Nextcloud is the import destination for the Drive half. Only the internal
// URL is configured: the write credential is a per-user app password obtained
// through Nextcloud's Login Flow v2, so no admin account is stored here.
type Nextcloud struct {
	URL string
}

// Configured reports whether Nextcloud can be reached.
func (n Nextcloud) Configured() bool {
	return n.URL != ""
}

// Immich is the import destination for the Photos half.
type Immich struct {
	URL    string
	APIKey core.Secret
}

// Configured reports whether Immich can be reached and written to.
func (i Immich) Configured() bool {
	return i.URL != "" && !i.APIKey.Empty()
}

// Resolve reads an optional env-style file and overlays the real environment
// on top of it. The real environment always wins.
func Resolve() (map[string]string, error) {
	env := map[string]string{}
	if path := os.Getenv("ZLATAN_CONFIG"); path != "" {
		fileEnv, err := parseEnvFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading ZLATAN_CONFIG: %w", err)
		}
		for k, v := range fileEnv {
			env[k] = v
		}
	}
	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		env[k] = v
	}
	return env, nil
}

func parseEnvFile(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `"'`)
		env[k] = v
	}
	return env, nil
}

// Load builds the configuration, reporting every problem at once so one pass
// fixes all of them.
func Load(env map[string]string) (*Config, error) {
	var problems []string
	get := func(key string) string { return strings.TrimSpace(env[key]) }

	cfg := &Config{
		Addr:            or(get("ZLATAN_ADDR"), DefaultAddr),
		DataDir:         or(get("ZLATAN_DATA_DIR"), DefaultDataDir),
		StagingDir:      or(get("ZLATAN_STAGING_DIR"), DefaultStaging),
		AllowPublicBind: get("ZLATAN_ALLOW_PUBLIC_BIND") == "true",
		ProxySecret:     core.Secret(get("ZLATAN_PROXY_SECRET")),
		TrustedProxy:    get("ZLATAN_TRUSTED_PROXY"),
		TokenKey:        core.Secret(get("ZLATAN_TOKEN_KEY")),
		Google: Google{
			ClientID:      core.Secret(get("ZLATAN_GOOGLE_CLIENT_ID")),
			ClientSecret:  core.Secret(get("ZLATAN_GOOGLE_CLIENT_SECRET")),
			RedirectURL:   get("ZLATAN_GOOGLE_REDIRECT_URL"),
			TakeoutFolder: or(get("ZLATAN_TAKEOUT_FOLDER"), DefaultTakeoutFolder),
		},
		Nextcloud: Nextcloud{
			URL: get("ZLATAN_NEXTCLOUD_URL"),
		},
		Immich: Immich{
			URL:    get("ZLATAN_IMMICH_URL"),
			APIKey: core.Secret(get("ZLATAN_IMMICH_API_KEY")),
		},
		StagingRetention: DefaultRetentionDays * 24 * time.Hour,
		MaxConcurrent:    1,
		TakeoutPoll:      DefaultTakeoutPoll,
		TakeoutMaxWait:   DefaultTakeoutMaxWait,
	}

	level, err := parseLevel(get("ZLATAN_LOG_LEVEL"))
	if err != nil {
		problems = append(problems, err.Error())
	}
	cfg.LogLevel = level

	if days := get("ZLATAN_STAGING_RETENTION_DAYS"); days != "" {
		n, err := strconv.Atoi(days)
		if err != nil || n < 0 {
			problems = append(problems, "ZLATAN_STAGING_RETENTION_DAYS must be a non-negative integer")
		} else {
			cfg.StagingRetention = time.Duration(n) * 24 * time.Hour
		}
	}

	if v := get("ZLATAN_TAKEOUT_POLL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Minute {
			problems = append(problems, "ZLATAN_TAKEOUT_POLL must be a duration of at least 1m")
		} else {
			cfg.TakeoutPoll = d
		}
	}
	if v := get("ZLATAN_TAKEOUT_MAX_WAIT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Hour {
			problems = append(problems, "ZLATAN_TAKEOUT_MAX_WAIT must be a duration of at least 1h")
		} else {
			cfg.TakeoutMaxWait = d
		}
	}

	if conc := get("ZLATAN_MAX_CONCURRENT"); conc != "" {
		n, err := strconv.Atoi(conc)
		if err != nil || n < 1 {
			problems = append(problems, "ZLATAN_MAX_CONCURRENT must be a positive integer")
		} else {
			cfg.MaxConcurrent = n
		}
	}

	// Binding a public interface without a proxy secret would expose the
	// service to anything that can reach the port, with no gate at all.
	if cfg.AllowPublicBind && cfg.ProxySecret.Empty() {
		problems = append(problems,
			"ZLATAN_ALLOW_PUBLIC_BIND=true requires ZLATAN_PROXY_SECRET: a public bind with no gate exposes every user's migration")
	}
	if cfg.TrustedProxy == "" {
		problems = append(problems,
			"ZLATAN_TRUSTED_PROXY is not set: no forward-auth header will be believed, so every request is refused")
	}
	if cfg.TokenKey.Empty() {
		problems = append(problems,
			"ZLATAN_TOKEN_KEY is not set: OAuth tokens cannot be sealed at rest")
	}

	if len(problems) > 0 {
		return nil, errors.New(strings.Join(problems, "\n"))
	}
	return cfg, nil
}

// Warnings returns things that are not fatal but mean a route will not work.
// They are logged rather than refused: the service starts and says what is
// missing, instead of crashing on a partial setup.
func (c *Config) Warnings() []string {
	var w []string
	if !c.Google.Configured() {
		w = append(w, "the Google OAuth client is not configured, so the Drive route is unavailable and the Photos wizard falls back to upload")
	}
	if !c.Nextcloud.Configured() {
		w = append(w, "ZLATAN_NEXTCLOUD_URL is not set, so the Drive route cannot import anything")
	}
	if !c.Immich.Configured() {
		w = append(w, "Immich is not configured, so the Photos route cannot import anything")
	}
	return w
}

func or(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func parseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("ZLATAN_LOG_LEVEL %q is not one of debug, info, warn, error", s)
	}
}

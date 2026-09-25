// SPDX-License-Identifier: AGPL-3.0-or-later

// Command zlatan is the whole of Zlatan: the wizard server and the small
// operator CLI in one binary.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/marcodellemarche/zlatan/internal/config"
	"github.com/marcodellemarche/zlatan/internal/core"
	"github.com/marcodellemarche/zlatan/internal/oauth"
	"github.com/marcodellemarche/zlatan/internal/runner"
	"github.com/marcodellemarche/zlatan/internal/store"
	"github.com/marcodellemarche/zlatan/internal/upload"
	"github.com/marcodellemarche/zlatan/internal/web"
)

// version is stamped at build time with -ldflags.
var version = "dev"

const (
	exitClean  = 0
	exitError  = 1
	exitConfig = 2
)

const usage = `zlatan is a self-guided migration service: Google Drive to
Nextcloud, Google Photos to Immich.

Usage:
  zlatan <command>

Commands:
  serve               run the wizard server: migrate the schema, then listen
  migrate             apply pending database migrations and exit
  version             print the version

Configuration is read from the environment, and from the env-style file named
by ZLATAN_CONFIG when it is set. See .env.example.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitConfig
	}

	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, version)
		return exitClean
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return exitClean
	case "serve":
		return withApp(stderr, true, serve)
	case "migrate":
		return withApp(stderr, true, migrateOnly)
	default:
		fmt.Fprintf(stderr, "zlatan: unknown command %q\n\n%s", args[0], usage)
		return exitConfig
	}
}

// app is what a command that touches state needs.
type app struct {
	cfg    *config.Config
	log    *slog.Logger
	db     *store.DB
	sealer *core.Sealer
}

func withApp(stderr io.Writer, migrates bool, fn func(context.Context, *app) int) int {
	cfg, log, code := loadConfig(stderr)
	if cfg == nil {
		return code
	}

	db, err := store.Open(filepath.Join(cfg.DataDir, "zlatan.db"))
	if err != nil {
		log.Error("open database", "error", err)
		return exitConfig
	}
	defer db.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if !migrates {
		if err := store.CheckSchema(ctx, db); err != nil {
			log.Error("database schema mismatch", "error", err)
			return exitConfig
		}
	}

	sealer, err := core.NewSealer(cfg.TokenKey)
	if err != nil {
		log.Error("build the token sealer", "error", err)
		return exitConfig
	}

	return fn(ctx, &app{cfg: cfg, log: log, db: db, sealer: sealer})
}

func loadConfig(stderr io.Writer) (*config.Config, *slog.Logger, int) {
	env, err := config.Resolve()
	if err != nil {
		fmt.Fprintf(stderr, "zlatan: %v\n", err)
		return nil, nil, exitConfig
	}
	cfg, err := config.Load(env)
	if err != nil {
		for _, line := range strings.Split(err.Error(), "\n") {
			fmt.Fprintf(stderr, "zlatan: config: %s\n", line)
		}
		return nil, nil, exitConfig
	}

	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	log.Info("zlatan starting",
		"version", version,
		"addr", cfg.Addr,
		"data_dir", cfg.DataDir,
		"staging_dir", cfg.StagingDir,
	)
	// Warnings, not refusals: the service starts and says what will not work,
	// rather than crashing on a partial setup.
	for _, w := range cfg.Warnings() {
		log.Warn(w)
	}
	return cfg, log, exitClean
}

func migrateOnly(ctx context.Context, a *app) int {
	if err := store.Migrate(ctx, a.db, a.log); err != nil {
		a.log.Error("migrate", "error", err)
		return exitConfig
	}
	version, err := store.DBVersion(ctx, a.db.R)
	if err != nil {
		a.log.Error("read schema version", "error", err)
		return exitError
	}
	a.log.Info("schema is current", "version", version)
	return exitClean
}

const shutdownGrace = 10 * time.Second

func serve(ctx context.Context, a *app) int {
	if err := store.Migrate(ctx, a.db, a.log); err != nil {
		a.log.Error("migrate", "error", err)
		return exitConfig
	}

	engine := runner.New(a.cfg, a.db, a.sealer, a.log)
	uploads := upload.New(a.cfg.StagingDir, 0, 0)

	// The OAuth provider is optional: with no client configured the wizard
	// simply does not offer the Drive route.
	var googleFlow web.GoogleFlow
	if a.cfg.Google.Configured() {
		provider, err := oauth.New(a.cfg.Google.ClientID, a.cfg.Google.ClientSecret, a.cfg.Google.RedirectURL)
		if err != nil {
			a.log.Error("build the Google OAuth provider", "error", err)
			return exitConfig
		}
		googleFlow = provider
	}

	srv := &http.Server{
		Addr: a.cfg.Addr,
		Handler: web.Routes(web.Options{
			Version:    version,
			Config:     a.cfg,
			State:      a.db,
			DB:         a.db.W,
			Log:        a.log,
			Runner:     engine,
			Google:     googleFlow,
			Sealer:     a.sealer,
			TokenStore: a.db,
			Uploads:    uploads,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		a.log.Info("listening", "addr", srv.Addr)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.log.Error("listen", "addr", srv.Addr, "error", err)
			return exitConfig
		}
		return exitClean
	case <-ctx.Done():
		a.log.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		a.log.Error("shutdown", "error", err)
		return exitError
	}
	return exitClean
}

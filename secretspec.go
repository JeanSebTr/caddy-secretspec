// Package secretspec is a Caddy app that resolves secrets declared in a
// SecretSpec manifest (https://secretspec.dev) and exposes them as
// {secretspec.NAME} placeholders, everywhere in the config: storage,
// TLS (e.g. DNS provider tokens), and HTTP handlers.
//
// Secrets never go through the process environment, and they are not
// readable from templates. They are resolved again on every config
// reload; if that fails, the reload is rejected and the running config
// keeps its secrets.
package secretspec

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	secretspec "github.com/cachix/secretspec/secretspec-go"
	"github.com/caddyserver/caddy/v2"
	"go.uber.org/zap"
)

func init() {
	caddy.RegisterModule(App{})
	caddy.RegisterReplacementProvider(caddy.ReplacementProvider{
		Namespace: "secretspec",
		Func:      lookup,
		Sensitive: true,
		App:       "secretspec",
	})
}

// App resolves the secrets of a SecretSpec manifest and makes them
// available as {secretspec.NAME} placeholders. For secrets declared
// with as_path, the placeholder is the path of a file holding the
// secret, which is removed once the config is unloaded.
//
// The app is provisioned before the config's storage and other apps,
// so its placeholders can be used while provisioning them.
type App struct {
	// Path to the secretspec.toml manifest. Defaults to SecretSpec's
	// own discovery, from the working directory.
	Path string `json:"path,omitempty"`

	// Provider URI to resolve secrets from, e.g. "keyring://" or
	// "env://". Defaults to SECRETSPEC_PROVIDER, then to the
	// manifest's configuration.
	Provider string `json:"provider,omitempty"`

	// Profile to resolve. Defaults to SECRETSPEC_PROFILE, then to
	// the manifest's default profile.
	Profile string `json:"profile,omitempty"`

	// Scope limits resolution to a named [scopes.*] subset of the
	// manifest.
	Scope string `json:"scope,omitempty"`

	// Reason for accessing the secrets, recorded by providers
	// that audit access.
	Reason string `json:"reason,omitempty"`

	state  *state
	prev   *state
	logger *zap.Logger
}

// CaddyModule returns the Caddy module information.
func (App) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "secretspec",
		New: func() caddy.Module { return new(App) },
	}
}

// Provision resolves the secrets, and makes them the ones served by
// the {secretspec.*} placeholders.
func (a *App) Provision(ctx caddy.Context) error {
	a.logger = ctx.Logger()

	b := secretspec.New()
	if a.Path != "" {
		b = b.WithPath(a.Path)
	}
	if a.Provider != "" {
		b = b.WithProvider(a.Provider)
	}
	if a.Profile != "" {
		b = b.WithProfile(a.Profile)
	}
	if a.Scope != "" {
		b = b.WithScope(a.Scope)
	}
	if a.Reason != "" {
		b = b.WithReason(a.Reason)
	}
	_, version := caddy.Version()
	b = b.WithCaller(secretspec.CallerContext{
		Name:      "caddy-secretspec",
		Version:   version,
		Operation: "provision",
	})

	resolved, err := load(b)
	if err != nil {
		var missing *secretspec.MissingRequiredError
		if errors.As(err, &missing) {
			return fmt.Errorf("missing required secrets: %v", missing.Missing)
		}
		return fmt.Errorf("resolving secrets: %w", err)
	}

	a.state = newState(resolved, a.logger)
	a.prev = current.Swap(a.state)

	a.logger.Info("resolved secrets",
		zap.String("provider", resolved.Provider),
		zap.String("profile", resolved.Profile),
		zap.Int("count", len(resolved.Secrets)),
		zap.Strings("missing_optional", resolved.MissingOptional))
	return nil
}

// Start implements caddy.App.
func (*App) Start() error { return nil }

// Stop implements caddy.App.
func (*App) Stop() error { return nil }

// Cleanup releases the secrets of this config. If they are still
// the ones being served, the config failed to load (or Caddy is
// shutting down), so the previous secrets are served again, unless
// they were released already.
func (a *App) Cleanup() error {
	if a.state == nil {
		return nil
	}
	restore := a.prev
	if restore != nil && restore.closed.Load() {
		restore = nil
	}
	current.CompareAndSwap(a.state, restore)
	return a.state.close()
}

// state is a set of resolved secrets, served by lookup while current.
type state struct {
	resolved *secretspec.Resolved
	missing  map[string]struct{}
	closed   atomic.Bool
	warned   sync.Map
	logger   *zap.Logger
}

func newState(resolved *secretspec.Resolved, logger *zap.Logger) *state {
	s := &state{
		resolved: resolved,
		missing:  make(map[string]struct{}, len(resolved.MissingOptional)),
		logger:   logger,
	}
	for _, name := range resolved.MissingOptional {
		s.missing[name] = struct{}{}
	}
	return s
}

func (s *state) get(name string) (any, bool) {
	if secret, ok := s.resolved.Secrets[name]; ok {
		v, _ := secret.Usable()
		return v, true
	}
	if _, ok := s.missing[name]; ok {
		return "", true
	}
	if _, warned := s.warned.LoadOrStore(name, struct{}{}); !warned {
		s.logger.Warn("unknown secret in placeholder; is it declared in the manifest?",
			zap.String("placeholder", "{secretspec."+name+"}"))
	}
	return nil, false
}

func (s *state) close() error {
	s.closed.Store(true)
	return s.resolved.Close()
}

// current is the state served by lookup. It is swapped by each config
// load, since replacement providers are global to the process.
var current atomic.Pointer[state]

func lookup(name string) (any, bool) {
	s := current.Load()
	if s == nil {
		return nil, false
	}
	return s.get(name)
}

// load resolves the secrets; a variable so that tests don't need the
// native SecretSpec library.
var load = func(b *secretspec.Builder) (*secretspec.Resolved, error) {
	return b.Load()
}

// Interface guards
var (
	_ caddy.App          = (*App)(nil)
	_ caddy.Provisioner  = (*App)(nil)
	_ caddy.CleanerUpper = (*App)(nil)
)

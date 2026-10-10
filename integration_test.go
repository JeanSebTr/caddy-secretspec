package secretspec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/caddyserver/caddy/v2"
)

// TestNativeResolver resolves a real manifest with the native SecretSpec
// library. It runs only when SECRETSPEC_FFI_LIB points to libsecretspec,
// e.g. one downloaded from https://github.com/cachix/secretspec/releases.
func TestNativeResolver(t *testing.T) {
	if os.Getenv("SECRETSPEC_FFI_LIB") == "" {
		t.Skip("SECRETSPEC_FFI_LIB not set")
	}

	dir := t.TempDir()
	manifest := filepath.Join(dir, "secretspec.toml")
	err := os.WriteFile(manifest, []byte(`
[project]
name = "caddy-secretspec-test"
revision = "1.0"

[profiles.default]
CADDY_SECRETSPEC_TOKEN = { description = "a token" }
CADDY_SECRETSPEC_OPTIONAL = { description = "optional", required = false }
`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	provision := func() (*App, error) {
		app := &App{Path: manifest, Provider: "env://", Reason: "caddy-secretspec tests"}
		ctx, cancel := caddy.NewContext(caddy.Context{Context: t.Context()})
		t.Cleanup(cancel)
		return app, app.Provision(ctx)
	}

	t.Run("missing required", func(t *testing.T) {
		_, err := provision()
		if err == nil || !strings.Contains(err.Error(), "CADDY_SECRETSPEC_TOKEN") {
			t.Fatalf("expected a missing required secret error, got %v", err)
		}
	})

	t.Run("resolved", func(t *testing.T) {
		t.Setenv("CADDY_SECRETSPEC_TOKEN", "from-env")
		app, err := provision()
		if err != nil {
			t.Fatal(err)
		}
		defer app.Cleanup()

		if v, _ := lookup("CADDY_SECRETSPEC_TOKEN"); v != "from-env" {
			t.Errorf("expected 'from-env', got '%v'", v)
		}
		if v, ok := lookup("CADDY_SECRETSPEC_OPTIONAL"); !ok || v != "" {
			t.Errorf("expected a known, empty optional secret, got '%v', %v", v, ok)
		}
	})
}

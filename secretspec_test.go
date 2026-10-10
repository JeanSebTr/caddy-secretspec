package secretspec

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	secretspec "github.com/cachix/secretspec/secretspec-go"
	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"

	_ "github.com/caddyserver/caddy/v2/modules/standard"
)

// provisionProbe is an app recording what {secretspec.TOKEN} resolves
// to while it is provisioned, like TLS or storage modules would see it.
type provisionProbe struct{}

var probeSeen atomic.Pointer[string]

func (provisionProbe) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: "secretspec_test_probe", New: func() caddy.Module { return new(provisionProbe) }}
}

func (*provisionProbe) Provision(caddy.Context) error {
	v := caddy.NewReplacer().ReplaceKnown("{secretspec.TOKEN}", "")
	probeSeen.Store(&v)
	return nil
}
func (*provisionProbe) Start() error { return nil }
func (*provisionProbe) Stop() error  { return nil }

func init() {
	caddy.RegisterModule(provisionProbe{})
}

// fakeLoad replaces the native resolver for the duration of a test;
// each Provision gets the result of next().
func fakeLoad(t *testing.T, next func() (*secretspec.Resolved, error)) {
	t.Helper()
	orig := load
	load = func(*secretspec.Builder) (*secretspec.Resolved, error) { return next() }
	t.Cleanup(func() { load = orig })
}

func resolvedWith(secrets map[string]string, missingOptional ...string) *secretspec.Resolved {
	r := &secretspec.Resolved{
		Provider:        "fake",
		Profile:         "test",
		Secrets:         make(map[string]secretspec.ResolvedSecret, len(secrets)),
		MissingOptional: missingOptional,
	}
	for k, v := range secrets {
		r.Secrets[k] = secretspec.ResolvedSecret{Value: &v}
	}
	return r
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// testConfig serves body at / and the templates-rendered tpl at /tpl.
func testConfig(port int, body, tpl string) []byte {
	cfg := map[string]any{
		"admin": map[string]any{"disabled": true},
		"apps": map[string]any{
			"secretspec":            map[string]any{"profile": "test"},
			"secretspec_test_probe": map[string]any{},
			"http": map[string]any{"servers": map[string]any{"srv": map[string]any{
				"listen":          []string{fmt.Sprintf("127.0.0.1:%d", port)},
				"automatic_https": map[string]any{"disable": true},
				"routes": []any{
					map[string]any{
						"match": []any{map[string]any{"path": []string{"/tpl"}}},
						"handle": []any{
							map[string]any{"handler": "templates"},
							map[string]any{"handler": "static_response", "body": tpl},
						},
						"terminal": true,
					},
					map[string]any{"handle": []any{
						map[string]any{"handler": "static_response", "body": body},
					}},
				},
			}}},
		},
	}
	b, _ := json.Marshal(cfg)
	return b
}

func get(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func TestLifecycle(t *testing.T) {
	values := make(chan *secretspec.Resolved, 1)
	failNext := false
	fakeLoad(t, func() (*secretspec.Resolved, error) {
		if failNext {
			return nil, errors.New("provider unavailable")
		}
		return <-values, nil
	})

	port := freePort(t)
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	cfg := testConfig(port,
		"token={secretspec.TOKEN} opt={secretspec.OPTIONAL} typo={secretspec.TYPO}",
		`tpl={{placeholder "secretspec.TOKEN"}}`)

	// initial load: secrets are available at provision and request time
	values <- resolvedWith(map[string]string{"TOKEN": "v1"}, "OPTIONAL")
	if err := caddy.Load(cfg, true); err != nil {
		t.Fatalf("loading config: %v", err)
	}
	t.Cleanup(func() { _ = caddy.Stop() })

	if seen := *probeSeen.Load(); seen != "v1" {
		t.Errorf("provision time: expected 'v1', got '%s'", seen)
	}
	if body := get(t, url); body != "token=v1 opt= typo={secretspec.TYPO}" {
		t.Errorf("request time: unexpected body '%s'", body)
	}
	if body := get(t, url+"/tpl"); body != "tpl=" {
		t.Errorf("templates must not read secrets, got '%s'", body)
	}

	// reload: secrets are resolved again
	values <- resolvedWith(map[string]string{"TOKEN": "v2"}, "OPTIONAL")
	if err := caddy.Load(cfg, true); err != nil {
		t.Fatalf("reloading config: %v", err)
	}
	if seen := *probeSeen.Load(); seen != "v2" {
		t.Errorf("provision time after reload: expected 'v2', got '%s'", seen)
	}
	if body := get(t, url); !strings.HasPrefix(body, "token=v2 ") {
		t.Errorf("request time after reload: unexpected body '%s'", body)
	}

	// failed reload: rejected, and the running config keeps its secrets
	failNext = true
	if err := caddy.Load(cfg, true); err == nil || !strings.Contains(err.Error(), "provider unavailable") {
		t.Fatalf("expected the reload to fail with the provider error, got %v", err)
	}
	if body := get(t, url); !strings.HasPrefix(body, "token=v2 ") {
		t.Errorf("after failed reload: unexpected body '%s'", body)
	}

	// shutdown: nothing is served anymore
	if err := caddy.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, ok := lookup("TOKEN"); ok {
		t.Error("expected no secrets to be served after shutdown")
	}
}

func TestAsPathFilesRemovedWhenUnloaded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("s3cr3t"), 0o400); err != nil {
		t.Fatal(err)
	}
	fakeLoad(t, func() (*secretspec.Resolved, error) {
		r := resolvedWith(nil)
		r.Secrets["KEY_FILE"] = secretspec.ResolvedSecret{Path: &path, AsPath: true}
		return r, nil
	})

	app := new(App)
	ctx, cancel := caddy.NewContext(caddy.Context{Context: t.Context()})
	defer cancel()
	if err := app.Provision(ctx); err != nil {
		t.Fatal(err)
	}
	if v, _ := lookup("KEY_FILE"); v != path {
		t.Errorf("expected the as_path placeholder to be the file path, got '%v'", v)
	}
	if err := app.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected the secret file to be removed, got %v", err)
	}
}

func TestMissingRequired(t *testing.T) {
	fakeLoad(t, func() (*secretspec.Resolved, error) {
		return nil, &secretspec.MissingRequiredError{Missing: []string{"A", "B"}}
	})
	ctx, cancel := caddy.NewContext(caddy.Context{Context: t.Context()})
	defer cancel()
	err := new(App).Provision(ctx)
	if err == nil || err.Error() != "missing required secrets: [A B]" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestUnmarshalCaddyfile(t *testing.T) {
	for _, tc := range []struct {
		input  string
		expect App
		err    string
	}{
		{input: `secretspec`},
		{input: `secretspec /etc/caddy/secretspec.toml`, expect: App{Path: "/etc/caddy/secretspec.toml"}},
		{
			input: `secretspec {
				path     /etc/caddy/secretspec.toml
				provider "sops://secrets.yaml"
				profile  production
				scope    edge
				reason   "caddy edge proxy"
			}`,
			expect: App{
				Path:     "/etc/caddy/secretspec.toml",
				Provider: "sops://secrets.yaml",
				Profile:  "production",
				Scope:    "edge",
				Reason:   "caddy edge proxy",
			},
		},
		{input: `secretspec a b`, err: "wrong argument count"},
		{input: "secretspec a {\npath b\n}", err: "path already specified"},
		{input: "secretspec {\nprofile\n}", err: "wrong argument count"},
		{input: "secretspec {\nprofile a b\n}", err: "wrong argument count"},
		{input: "secretspec {\nnope a\n}", err: "unrecognized subdirective 'nope'"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			var app App
			err := app.UnmarshalCaddyfile(caddyfile.NewTestDispenser(tc.input))
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("expected error containing '%s', got %v", tc.err, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if app.Path != tc.expect.Path || app.Provider != tc.expect.Provider ||
				app.Profile != tc.expect.Profile || app.Scope != tc.expect.Scope ||
				app.Reason != tc.expect.Reason {
				t.Errorf("expected %+v, got %+v", tc.expect, app)
			}
		})
	}
}

func TestCaddyfileAdapt(t *testing.T) {
	adapter := caddyfile.Adapter{ServerType: httpcaddyfile.ServerType{}}
	out, _, err := adapter.Adapt([]byte(`{
		secretspec {
			profile production
		}
	}
	:8080 {
		respond {secretspec.TOKEN}
	}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Apps map[string]json.RawMessage `json:"apps"`
	}
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatal(err)
	}
	if got := string(cfg.Apps["secretspec"]); got != `{"profile":"production"}` {
		t.Errorf("unexpected secretspec app config: %s", got)
	}
}

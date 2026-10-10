# caddy-secretspec

A [Caddy](https://caddyserver.com) app that resolves secrets declared in a
[SecretSpec](https://secretspec.dev) manifest and exposes them as
`{secretspec.NAME}` placeholders.

```caddyfile
{
	secretspec /etc/caddy/secretspec.toml {
		profile  production
		provider sops://secrets.yaml
		reason   "caddy edge proxy"
	}
}

example.com {
	tls {
		dns cloudflare {secretspec.CF_API_TOKEN}  # needs the caddy-dns/cloudflare plugin
	}
	reverse_proxy localhost:8080 {
		header_up Authorization "Bearer {secretspec.UPSTREAM_TOKEN}"
	}
}
```

- **Everywhere in the config.** Placeholders work while provisioning storage
  and TLS (e.g. DNS provider tokens), not only in HTTP handlers.
- **Not in the environment.** Secrets are never exported to the process
  environment, so child processes, `caddy environ` and `{{env}}` in templates
  can't see them. They are not readable with `{{placeholder}}` in templates
  either.
- **Not in the config.** Only the manifest's coordinates are in the config, so
  the admin API's `GET /config/` exposes no secret.
- **Rotation on reload.** Secrets are resolved again on every config load.
  Caddy skips reloading an unchanged config, so use `caddy reload --force`
  after changing only secrets. If resolving fails, the reload is rejected and
  the running config keeps its secrets.
- **`as_path` secrets.** For secrets declared with `as_path = true`, the
  placeholder is the path of a file holding the secret (e.g. for `tls <cert>
  <key>`). The file is removed once the config is unloaded.

> [!IMPORTANT]
> This plugin needs global replacement providers, which upstream Caddy does
> not have. Build it against the
> [`replacement-provider-registry`](https://github.com/JeanSebTr/caddy/tree/replacement-provider-registry)
> branch of the `JeanSebTr/caddy` fork.

## Options

| Option | Description |
| --- | --- |
| `path` (or the first argument) | Path to `secretspec.toml`. Defaults to SecretSpec's discovery from the working directory. |
| `provider` | Provider URI, e.g. `keyring://`, `env://`, `sops://…`. Defaults to `SECRETSPEC_PROVIDER`, then the manifest. |
| `profile` | Profile to resolve. Defaults to `SECRETSPEC_PROFILE`, then the manifest's default. |
| `scope` | Only resolve a named `[scopes.*]` subset of the manifest. |
| `reason` | Access reason, written to SecretSpec's local audit log (on by default, configured per user). Required if the manifest sets `require_reason`. |

The equivalent JSON config is the `secretspec` app:

```json
{"apps": {"secretspec": {"path": "/etc/caddy/secretspec.toml", "profile": "production"}}}
```

A secret that is not declared in the manifest is left as is
(`{secretspec.TYPO}`) and logged as a warning; a declared optional secret
that is missing is an empty string. Placeholders can't be used in logging
config, which is set up before the secrets are resolved.

Headless servers need non-interactive providers, such as `sops`, `age`,
`systemd-credential`, or a cloud secrets manager.

## Building

```sh
xcaddy build \
	--with github.com/JeanSebTr/caddy-secretspec \
	--replace github.com/caddyserver/caddy/v2=github.com/JeanSebTr/caddy/v2@v2.0.0-20261010150625-a7f257b64e73  # fork with replacement providers
```

For development, `go build ./cmd/caddy` builds Caddy with this plugin.

The SecretSpec Go SDK loads the native `libsecretspec` library at runtime
(without cgo): point `SECRETSPEC_FFI_LIB` at a library downloaded from the
[SecretSpec releases](https://github.com/cachix/secretspec/releases). The
SDK's `embed_lib` and `static` build tags don't work with the module as
downloaded from the Go proxy, which doesn't include the library; see the
[SDK's documentation](https://github.com/cachix/secretspec/tree/main/secretspec-go)
to stage it.

## Tests

```sh
go test ./...
# with the native library, to also resolve a real manifest
SECRETSPEC_FFI_LIB=/path/to/libsecretspec.dylib go test ./...
```

package secretspec

import (
	"github.com/caddyserver/caddy/v2/caddyconfig"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
)

func init() {
	httpcaddyfile.RegisterGlobalOption("secretspec", parseGlobalOption)
}

// parseGlobalOption sets up the secretspec app from the Caddyfile.
// Syntax:
//
//	secretspec [<path>] {
//	    path     <path>
//	    provider <uri>
//	    profile  <name>
//	    scope    <name>
//	    reason   <text>
//	}
func parseGlobalOption(d *caddyfile.Dispenser, existingVal any) (any, error) {
	if existingVal != nil {
		return nil, d.Err("secretspec global option already specified")
	}
	app := new(App)
	if err := app.UnmarshalCaddyfile(d); err != nil {
		return nil, err
	}
	return httpcaddyfile.App{
		Name:  "secretspec",
		Value: caddyconfig.JSON(app, nil),
	}, nil
}

// UnmarshalCaddyfile sets up the app from Caddyfile tokens.
func (a *App) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	d.Next() // consume option name

	if d.NextArg() {
		a.Path = d.Val()
	}
	if d.NextArg() {
		return d.ArgErr()
	}

	for d.NextBlock(0) {
		var field *string
		switch d.Val() {
		case "path":
			field = &a.Path
		case "provider":
			field = &a.Provider
		case "profile":
			field = &a.Profile
		case "scope":
			field = &a.Scope
		case "reason":
			field = &a.Reason
		default:
			return d.Errf("unrecognized subdirective '%s'", d.Val())
		}
		if *field != "" {
			return d.Errf("%s already specified", d.Val())
		}
		if !d.NextArg() {
			return d.ArgErr()
		}
		*field = d.Val()
		if d.NextArg() {
			return d.ArgErr()
		}
	}
	return nil
}

// Interface guard
var _ caddyfile.Unmarshaler = (*App)(nil)

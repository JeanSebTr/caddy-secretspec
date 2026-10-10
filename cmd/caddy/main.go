// Command caddy is Caddy with the secretspec plugin, for development.
// Release builds should use xcaddy instead.
package main

import (
	caddycmd "github.com/caddyserver/caddy/v2/cmd"

	_ "github.com/caddyserver/caddy/v2/modules/standard"
	_ "github.com/JeanSebTr/caddy-secretspec"
)

func main() {
	caddycmd.Main()
}

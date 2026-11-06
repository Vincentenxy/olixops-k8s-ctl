// pat is a local administrator bootstrap tool; it never exposes a network mint endpoint.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	app "olixops-k8s-ctl/internal/application/auth"
	d "olixops-k8s-ctl/internal/domain/auth"
	infra "olixops-k8s-ctl/internal/infrastructure/auth"
	"olixops-k8s-ctl/internal/infrastructure/config"
)

func main() {
	dir := flag.String("config-dir", "./configs", "configuration directory")
	user := flag.String("user-id", "", "verified owner ID")
	name := flag.String("name", "bootstrap", "PAT label")
	days := flag.Int("days", 30, "expiry in days (1-365)")
	flag.Parse()
	if *user == "" || *days < 1 || *days > 365 {
		fail("user-id and days 1-365 required")
	}
	cfg, e := config.Load(*dir)
	if e != nil {
		fail("load configuration failed")
	}
	if !cfg.Auth.Enabled || !cfg.Auth.PAT.Enabled {
		fail("auth and PAT must be enabled")
	}
	jwt, e := infra.NewJWT(cfg.Auth)
	if e != nil {
		fail(e.Error())
	}
	repo, e := infra.Open(cfg.Auth.PAT.DatabasePath)
	if e != nil {
		fail("open PAT database failed")
	}
	defer repo.Close()
	s := app.New(jwt, jwt, repo)
	ctx := d.WithPrincipal(context.Background(), d.Principal{UserID: *user, AuthSource: "local-admin-bootstrap"})
	out, e := s.Create(ctx, app.CreateInput{Name: *name, ExpiresAt: time.Now().UTC().Add(time.Duration(*days) * 24 * time.Hour)})
	if e != nil {
		fail("create PAT failed")
	}
	// The token is printed exactly once to the administrator's terminal, never logged.
	if e = json.NewEncoder(os.Stdout).Encode(out); e != nil {
		fail("write output failed")
	}
}
func fail(message string) { fmt.Fprintln(os.Stderr, message); os.Exit(1) }

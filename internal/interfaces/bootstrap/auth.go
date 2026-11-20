// Package bootstrap shares platform authentication wiring across server entry points.
package bootstrap

import (
	"fmt"
	app "olixops-k8s-ctl/internal/application/auth"
	infra "olixops-k8s-ctl/internal/infrastructure/auth"
	"strings"
)

func Authentication(cfg infra.Config) (*app.Service, func(), error) {
	cleanup := func() {}
	if !cfg.Enabled {
		return nil, cleanup, nil
	}
	if !cfg.JWT.Enabled && !cfg.PAT.Enabled {
		return nil, cleanup, fmt.Errorf("enable JWT or platform PAT authentication")
	}
	if cfg.JWT.Enabled && (strings.TrimSpace(cfg.JWT.Issuer) == "" || strings.TrimSpace(cfg.JWT.Audience) == "") {
		return nil, cleanup, fmt.Errorf("platform JWT issuer and audience required")
	}
	verifier, err := infra.NewJWT(cfg)
	if err != nil {
		return nil, cleanup, err
	}
	service, err := app.New(verifier, cfg.JWT.Enabled, cfg.PAT.Enabled)
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}
	return service, cleanup, nil
}

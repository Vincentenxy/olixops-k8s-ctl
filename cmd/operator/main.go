// The platform operator accepts encrypted-at-rest credentials after platform-side decryption.
// Each registered cluster receives its own application service and authenticated router.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"go.uber.org/zap"
	"olixops-k8s-ctl/internal/application"
	authapp "olixops-k8s-ctl/internal/application/auth"
	sessions "olixops-k8s-ctl/internal/application/shell"
	"olixops-k8s-ctl/internal/application/shell/interceptor"
	authinfra "olixops-k8s-ctl/internal/infrastructure/auth"
	"olixops-k8s-ctl/internal/infrastructure/config"
	"olixops-k8s-ctl/internal/infrastructure/execctl"
	"olixops-k8s-ctl/internal/infrastructure/k8smcp"
	"olixops-k8s-ctl/internal/infrastructure/kubeconfig"
	clusterk8s "olixops-k8s-ctl/internal/infrastructure/kubernetes"
	"olixops-k8s-ctl/internal/interfaces/operator"
	"olixops-k8s-ctl/internal/interfaces/router"
	"olixops-k8s-ctl/pkg/logger"
)

func main() {
	directory := flag.String("config-dir", "./configs", "configuration directory")
	maximum := flag.Int("max-clusters", 16, "maximum registered clusters")
	flag.Parse()
	logger.Init("debug")
	defer logger.Sync()
	cfg, err := config.Load(*directory)
	if err != nil {
		logger.L().Fatal("operator configuration failed", zap.Error(err))
	}
	// In authenticated routers the legacy API key is intentionally ignored by config.Load.
	// The operator uses a separate platform bootstrap credential on its private entry.
	if cfg.Control.APIKeyFile != "" {
		if cfg.Control.APIKey != "" {
			logger.L().Fatal("configure only control.api-key-file for operator bootstrap")
		}
		key, e := os.ReadFile(cfg.Control.APIKeyFile)
		if e != nil {
			logger.L().Fatal("operator bootstrap credential unavailable")
		}
		cfg.Control.APIKey = strings.TrimSpace(string(key))
	}
	if cfg.Control.APIKey == "" || !cfg.Auth.Enabled || !cfg.Auth.Company.Enabled || cfg.Auth.PAT.Enabled || cfg.Catalog.Enabled || cfg.MCP.DiagnosticsOnly || *maximum < 1 {
		logger.L().Fatal("operator requires a control key, company authentication, no PAT/catalog/diagnostics-only and a positive cluster limit")
	}
	if cfg.PodExec.Enabled && cfg.PodExec.Mode != "managed" {
		logger.L().Fatal("operator requires managed pod execution to isolate cluster credentials")
	}
	factory := func(ctx context.Context, input operator.Input) (http.Handler, func(), error) {
		local := *cfg
		local.Cluster.ID = input.ClusterID
		local.Catalog.Cluster = input.ClusterID
		local.Auth.Company.Audience = "k8s-ctl:" + input.ClusterID
		local.Kubernetes.InCluster = false
		local.Kubernetes.Kubeconfig.Context = ""
		local.Server.PublicURL = ""
		if e := local.ValidatePodExec(); e != nil {
			return nil, nil, e
		}
		provider, e := kubeconfig.NewMemoryProvider([]byte(input.Kubeconfig))
		if e != nil {
			return nil, nil, e
		}
		reference := kubeconfig.Reference{}
		snapshot, e := kubeconfig.Snapshot(ctx, provider, reference)
		if e != nil {
			return nil, nil, e
		}
		client, e := clusterk8s.New(local.Kubernetes, snapshot, clusterk8s.WithPodExecAuthorization(local.PodExec.AuthorizeTargets()))
		if e != nil {
			return nil, nil, e
		}
		runner := execctl.New(execctl.Config{Provider: snapshot, Reference: reference, Kubectl: local.Kubernetes.Kubectl, Helm: local.Kubernetes.Helm, TimeoutSeconds: local.Kubernetes.CommandTimeoutSecond})
		options := []application.ServiceOption{application.WithClusterID(input.ClusterID), application.WithShell(local.Shell.Enabled, local.Shell.Interceptor.Enabled, interceptor.AllowAll{})}
		if local.PodExec.Enabled {
			backend, e := k8smcp.NewManaged(local.PodExec.Binary, snapshot)
			if e != nil {
				return nil, nil, e
			}
			options = append(options, application.WithPodExecutor(backend))
		}
		if local.PodExec.AuthorizeTargets() && len(local.PodExec.Grants) > 0 {
			options = append(options, application.WithDiagnosticGrants(local.PodExec.Grants))
		}
		cleanup := func() {}
		if local.Shell.Enabled {
			manager := sessions.NewManager(time.Duration(local.Shell.Terminal.IdleSeconds)*time.Second, time.Duration(local.Shell.Terminal.LifetimeSeconds)*time.Second, local.Shell.Terminal.MaxSessions, local.Shell.Terminal.MaxPerUser)
			cleanup = manager.Close
			options = append(options, application.WithTerminals(client, manager))
		}
		service := application.NewClusterService(client, runner, local.Kubernetes, options...)
		jwt, e := authinfra.NewJWT(local.Auth)
		if e != nil {
			cleanup()
			return nil, nil, e
		}
		handler, e := router.NewWithAuth(&local, service, nil, authapp.New(jwt, jwt, nil))
		if e != nil {
			cleanup()
			return nil, nil, e
		}
		return handler, cleanup, nil
	}
	registry := operator.New(cfg.Control.APIKey, *maximum, factory)
	defer registry.Close()
	server := &http.Server{Addr: fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port), Handler: registry, ReadHeaderTimeout: 5 * time.Second}
	listenerError := make(chan error, 1)
	go func() {
		logger.L().Info("platform operator started", zap.String("addr", server.Addr))
		if e := server.ListenAndServe(); e != nil && !errors.Is(e, http.ErrServerClosed) {
			listenerError <- e
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-stop:
	case <-listenerError:
		logger.L().Error("operator listener failed")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"olixops-k8s-ctl/internal/application"
	catalogapp "olixops-k8s-ctl/internal/application/catalog"
	sessions "olixops-k8s-ctl/internal/application/shell"
	"olixops-k8s-ctl/internal/application/shell/interceptor"
	catalogdomain "olixops-k8s-ctl/internal/domain/catalog"
	"olixops-k8s-ctl/internal/infrastructure/catalogdb"
	"olixops-k8s-ctl/internal/infrastructure/config"
	"olixops-k8s-ctl/internal/infrastructure/execctl"
	"olixops-k8s-ctl/internal/infrastructure/k8smcp"
	"olixops-k8s-ctl/internal/infrastructure/kubeconfig"
	clusterk8s "olixops-k8s-ctl/internal/infrastructure/kubernetes"
	"olixops-k8s-ctl/internal/interfaces/bootstrap"
	"olixops-k8s-ctl/internal/interfaces/middleware"
	"olixops-k8s-ctl/internal/interfaces/operator"
	"olixops-k8s-ctl/internal/interfaces/router"
	"olixops-k8s-ctl/pkg/logger"
)

func main() {
	configDir := flag.String("config-dir", "./configs", "directory containing the single config.yaml")
	flag.Parse()

	cfg, err := config.Load(*configDir)
	if err != nil {
		logger.Init("debug")
		logger.L().Fatal("load config failed", zap.Error(err))
	}

	logger.Init(cfg.Server.Mode)
	defer logger.Sync()

	if cfg.App.Mode == config.ModeOperator {
		runOperator(cfg)
		return
	}
	runStandalone(cfg)
}

func runStandalone(cfg *config.Config) {
	authentication, closeAuthentication, authErr := bootstrap.Authentication(cfg.Auth)
	if authErr != nil {
		logger.L().Fatal("init platform authentication failed", zap.Error(authErr))
	}
	defer closeAuthentication()

	kubeconfigReference := kubeconfig.Reference{
		Source:    cfg.Kubernetes.Kubeconfig.Source,
		Path:      cfg.Kubernetes.Kubeconfig.Path,
		Reference: cfg.Kubernetes.Kubeconfig.Reference,
		Context:   cfg.Kubernetes.Kubeconfig.Context,
	}
	if cfg.Kubernetes.InCluster {
		kubeconfigReference = kubeconfig.Reference{Source: kubeconfig.SourceInCluster}
		cfg.Kubernetes.Kubeconfig.Source = kubeconfig.SourceInCluster
		cfg.Kubernetes.Kubeconfig.Context = ""
	}
	kubeconfigProvider, err := kubeconfig.NewProvider(kubeconfigReference, nil)
	if err != nil {
		logger.L().Fatal("init kubeconfig provider failed", zap.Error(err))
	}
	var managedSnapshot *kubeconfig.SnapshotProvider
	if cfg.PodExec.Enabled && cfg.PodExec.Mode == "managed" {
		managedSnapshot, err = kubeconfig.Snapshot(context.Background(), kubeconfigProvider, kubeconfigReference)
		if err != nil {
			logger.L().Fatal("pin managed MCP cluster failed")
		}
		kubeconfigProvider = managedSnapshot
	}
	clusterClient, err := clusterk8s.New(cfg.Kubernetes, kubeconfigProvider, clusterk8s.WithPodExecAuthorization(cfg.PodExec.AuthorizeTargets()))
	if err != nil {
		logger.L().Fatal("init kubernetes client failed", zap.Error(err))
	}
	runner := execctl.New(execctl.Config{
		Provider:       kubeconfigProvider,
		Reference:      kubeconfigReference,
		Kubectl:        cfg.Kubernetes.Kubectl,
		Helm:           cfg.Kubernetes.Helm,
		TimeoutSeconds: cfg.Kubernetes.CommandTimeoutSecond,
	})
	serviceOptions := []application.ServiceOption{application.WithClusterID(cfg.ClusterID())}
	if cfg.PodExec.Enabled {
		var backend *k8smcp.Client
		var backendErr error
		if cfg.PodExec.Mode == "managed" {
			backend, backendErr = k8smcp.NewManaged(cfg.PodExec.Binary, managedSnapshot)
		} else {
			backend, backendErr = k8smcp.New(cfg.PodExec.Endpoint)
		}
		if backendErr != nil {
			logger.L().Fatal("init Kubernetes MCP bridge failed", zap.Error(backendErr))
		}
		serviceOptions = append(serviceOptions, application.WithPodExecutor(backend))
	}
	serviceOptions = append(serviceOptions, application.WithShell(cfg.Shell.Enabled, cfg.Shell.Interceptor.Enabled, interceptor.AllowAll{}))
	if cfg.PodExec.AuthorizeTargets() && (cfg.MCP.DiagnosticsOnly || len(cfg.PodExec.Grants) > 0) {
		serviceOptions = append(serviceOptions, application.WithDiagnosticGrants(cfg.PodExec.Grants))
	}
	if cfg.Shell.Enabled {
		manager := sessions.NewManager(
			time.Duration(cfg.Shell.Terminal.IdleSeconds)*time.Second,
			time.Duration(cfg.Shell.Terminal.LifetimeSeconds)*time.Second,
			cfg.Shell.Terminal.MaxSessions,
			cfg.Shell.Terminal.MaxPerUser,
		)
		defer manager.Close()
		serviceOptions = append(serviceOptions, application.WithTerminals(clusterClient, manager))
	}
	downloadBase := cfg.Server.PublicURL
	if downloadBase == "" && (cfg.Server.Host == "127.0.0.1" || cfg.Server.Host == "localhost") {
		downloadBase = fmt.Sprintf("http://%s:%d", cfg.Server.Host, cfg.Server.Port)
	}
	serviceOptions = append(serviceOptions, application.WithDownloadBaseURL(downloadBase))
	clusterService := application.NewClusterService(clusterClient, runner, cfg.Kubernetes, serviceOptions...)
	catalogService, closeCatalog := standaloneCatalog(cfg, clusterService)
	defer closeCatalog()
	handler, err := router.NewWithAuth(cfg, clusterService, catalogService, authentication)
	if err != nil {
		logger.L().Fatal("init router failed", zap.Error(err))
	}
	serveUntilSignal(cfg, handler, "server started", "server exited")
}

func standaloneCatalog(cfg *config.Config, clusterService *application.ClusterService) (*catalogapp.Service, func()) {
	if !cfg.Catalog.Enabled || cfg.MCP.DiagnosticsOnly {
		return nil, func() {}
	}
	store, err := catalogdb.Open(cfg.Catalog.DatabasePath)
	if err != nil {
		logger.L().Fatal("init catalog database failed", zap.Error(err))
	}
	c := cfg.Catalog
	catalogService, err := catalogapp.New(
		context.Background(),
		store,
		application.CatalogCluster{Service: clusterService},
		catalogdomain.Gateway{
			ID:        c.GatewayID,
			Cluster:   c.Cluster,
			Namespace: c.GatewayNamespace,
			Service:   c.GatewayService,
			Name:      c.GatewayName,
			PortStart: c.PortStart,
			PortEnd:   c.PortEnd,
		},
		nil,
		catalogapp.WithDefaults(catalogapp.Defaults{
			OwnerID:          c.DefaultOwner,
			StorageClass:     c.DefaultStorageClass,
			RuntimeClassName: c.SandboxRuntimeClass,
		}),
	)
	if err != nil {
		logger.L().Fatal("init catalog failed", zap.Error(err))
	}

	expiryCtx, stopExpiry := context.WithCancel(context.Background())
	expiryDone := make(chan struct{})
	go func() {
		defer close(expiryDone)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			runCtx, cancelRun := context.WithTimeout(expiryCtx, 120*time.Second)
			expiryErr := catalogService.ExpireSandboxes(runCtx, time.Now().UTC())
			cancelRun()
			if expiryErr != nil && expiryCtx.Err() == nil {
				logger.L().Warn("sandbox expiry cleanup pending")
			}
			select {
			case <-expiryCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	cleanup := func() {
		stopExpiry()
		<-expiryDone
		_ = store.Close()
	}
	return catalogService, cleanup
}

func runOperator(cfg *config.Config) {
	if !cfg.Auth.Enabled || cfg.Catalog.Enabled || cfg.MCP.DiagnosticsOnly {
		logger.L().Fatal("operator requires platform authentication, no catalog/diagnostics-only mode")
	}
	if cfg.PodExec.Enabled && cfg.PodExec.Mode != "managed" {
		logger.L().Fatal("operator requires managed pod execution to isolate cluster credentials")
	}

	authentication, closeAuthentication, authErr := bootstrap.Authentication(cfg.Auth)
	if authErr != nil {
		logger.L().Fatal("platform authentication configuration failed", zap.Error(authErr))
	}
	defer closeAuthentication()

	factory := func(ctx context.Context, input operator.Input) (http.Handler, func(), error) {
		fail := func(stage string, err error) (http.Handler, func(), error) {
			logger.Error(
				ctx,
				"[operator.factory] runtime initialization failed",
				zap.String("clusterId", input.ClusterID),
				zap.String("stage", stage),
				zap.String("errorType", fmt.Sprintf("%T", err)),
				zap.Error(err),
			)
			return nil, nil, err
		}

		local := *cfg
		local.Cluster.ID = input.ClusterID
		local.Catalog.Cluster = input.ClusterID
		local.Kubernetes.InCluster = false
		local.Kubernetes.Kubeconfig.Context = ""
		local.Server.PublicURL = ""
		if err := local.ValidatePodExec(); err != nil {
			return fail("validate-pod-exec", err)
		}
		provider, err := kubeconfig.NewMemoryProvider([]byte(input.Kubeconfig))
		if err != nil {
			return fail("load-kubeconfig", err)
		}
		reference := kubeconfig.Reference{}
		snapshot, err := kubeconfig.Snapshot(ctx, provider, reference)
		if err != nil {
			return fail("snapshot-kubeconfig", err)
		}
		client, err := clusterk8s.New(local.Kubernetes, snapshot, clusterk8s.WithPodExecAuthorization(local.PodExec.AuthorizeTargets()))
		if err != nil {
			return fail("create-kubernetes-client", err)
		}
		runner := execctl.New(execctl.Config{
			Provider:       snapshot,
			Reference:      reference,
			Kubectl:        local.Kubernetes.Kubectl,
			Helm:           local.Kubernetes.Helm,
			TimeoutSeconds: local.Kubernetes.CommandTimeoutSecond,
		})
		serviceOptions := []application.ServiceOption{
			application.WithClusterID(input.ClusterID),
			application.WithShell(local.Shell.Enabled, local.Shell.Interceptor.Enabled, interceptor.AllowAll{}),
		}
		if local.PodExec.Enabled {
			backend, backendErr := k8smcp.NewManaged(local.PodExec.Binary, snapshot)
			if backendErr != nil {
				return fail("create-managed-mcp", backendErr)
			}
			serviceOptions = append(serviceOptions, application.WithPodExecutor(backend))
		}
		if local.PodExec.AuthorizeTargets() && len(local.PodExec.Grants) > 0 {
			serviceOptions = append(serviceOptions, application.WithDiagnosticGrants(local.PodExec.Grants))
		}
		cleanup := func() {}
		if local.Shell.Enabled {
			manager := sessions.NewManager(
				time.Duration(local.Shell.Terminal.IdleSeconds)*time.Second,
				time.Duration(local.Shell.Terminal.LifetimeSeconds)*time.Second,
				local.Shell.Terminal.MaxSessions,
				local.Shell.Terminal.MaxPerUser,
			)
			cleanup = manager.Close
			serviceOptions = append(serviceOptions, application.WithTerminals(client, manager))
		}
		service := application.NewClusterService(client, runner, local.Kubernetes, serviceOptions...)
		handler, err := router.NewWithAuth(&local, service, nil, authentication)
		if err != nil {
			cleanup()
			return fail("create-router", err)
		}
		return handler, cleanup, nil
	}

	registry := operator.New(cfg.Operator.MaxClusters, factory)
	defer registry.Close()
	handler := middleware.AuthenticateHTTP(authentication, registry)
	serveUntilSignal(cfg, handler, "platform operator started", "platform operator exited")
}

func serveUntilSignal(cfg *config.Config, handler http.Handler, startMessage, exitMessage string) {
	server := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	listenerError := make(chan error, 1)
	go func() {
		logger.L().Info(startMessage,
			zap.String("name", cfg.App.Name),
			zap.String("version", cfg.App.Version),
			zap.String("mode", cfg.App.Mode),
			zap.String("addr", server.Addr),
		)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			listenerError <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-quit:
	case err := <-listenerError:
		logger.L().Fatal("server listener failed", zap.Error(err))
	}

	logger.L().Info("shutting down", zap.String("mode", cfg.App.Mode))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.L().Fatal("server shutdown failed", zap.Error(err))
	}
	logger.L().Info(exitMessage)
}

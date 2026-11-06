package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	sessions "olixops-k8s-ctl/internal/application/shell"
	"olixops-k8s-ctl/internal/application/shell/interceptor"
	"os"
	"os/signal"
	"syscall"
	"time"

	"olixops-k8s-ctl/internal/application"
	authapp "olixops-k8s-ctl/internal/application/auth"
	catalogapp "olixops-k8s-ctl/internal/application/catalog"
	authdomain "olixops-k8s-ctl/internal/domain/auth"
	catalogdomain "olixops-k8s-ctl/internal/domain/catalog"
	authinfra "olixops-k8s-ctl/internal/infrastructure/auth"
	"olixops-k8s-ctl/internal/infrastructure/catalogdb"
	"olixops-k8s-ctl/internal/infrastructure/config"
	"olixops-k8s-ctl/internal/infrastructure/execctl"
	"olixops-k8s-ctl/internal/infrastructure/k8smcp"
	"olixops-k8s-ctl/internal/infrastructure/kubeconfig"
	clusterk8s "olixops-k8s-ctl/internal/infrastructure/kubernetes"
	"olixops-k8s-ctl/internal/interfaces/router"
	"olixops-k8s-ctl/pkg/logger"

	"go.uber.org/zap"
)

func main() {
	configDir := flag.String("config-dir", "./configs", "directory containing config.yaml")
	flag.Parse()
	cfg, err := config.Load(*configDir)
	if err != nil {
		logger.Init("debug")
		logger.L().Fatal("load config failed", zap.Error(err))
	}

	logger.Init(cfg.Server.Mode)
	defer logger.Sync()

	var authentication *authapp.Service
	if cfg.Auth.Enabled {
		jwtAdapter, e := authinfra.NewJWT(cfg.Auth)
		if e != nil {
			logger.L().Fatal("init authentication failed", zap.Error(e))
		}
		var repository authdomain.Repository
		if cfg.Auth.PAT.Enabled {
			store, openErr := authinfra.Open(cfg.Auth.PAT.DatabasePath)
			if openErr != nil {
				logger.L().Fatal("init PAT database failed")
			}
			defer store.Close()
			repository = store
		}
		authentication = authapp.New(jwtAdapter, jwtAdapter, repository)
	}
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
	clusterClient, err := clusterk8s.New(cfg.Kubernetes, kubeconfigProvider)
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
		var e error
		if cfg.PodExec.Mode == "managed" {
			backend, e = k8smcp.NewManaged(cfg.PodExec.Binary, managedSnapshot)
		} else {
			backend, e = k8smcp.New(cfg.PodExec.Endpoint)
		}
		if e != nil {
			logger.L().Fatal("init Kubernetes MCP bridge failed", zap.Error(e))
		}
		serviceOptions = append(serviceOptions, application.WithPodExecutor(backend, cfg.PodExec.AllowedNamespaces))
	}
	serviceOptions = append(serviceOptions, application.WithShell(cfg.Shell.Enabled, cfg.Shell.Interceptor.Enabled, interceptor.AllowAll{}))
	if cfg.MCP.DiagnosticsOnly || len(cfg.PodExec.Grants) > 0 {
		serviceOptions = append(serviceOptions, application.WithDiagnosticGrants(cfg.PodExec.Grants))
	}
	if cfg.Shell.Enabled {
		manager := sessions.NewManager(time.Duration(cfg.Shell.Terminal.IdleSeconds)*time.Second, time.Duration(cfg.Shell.Terminal.LifetimeSeconds)*time.Second, cfg.Shell.Terminal.MaxSessions, cfg.Shell.Terminal.MaxPerUser)
		defer manager.Close()
		serviceOptions = append(serviceOptions, application.WithTerminals(clusterClient, manager))
	}
	downloadBase := cfg.Server.PublicURL
	if downloadBase == "" && (cfg.Server.Host == "127.0.0.1" || cfg.Server.Host == "localhost") {
		downloadBase = fmt.Sprintf("http://%s:%d", cfg.Server.Host, cfg.Server.Port)
	}
	serviceOptions = append(serviceOptions, application.WithDownloadBaseURL(downloadBase))
	clusterService := application.NewClusterService(clusterClient, runner, cfg.Kubernetes, serviceOptions...)
	var catalogService *catalogapp.Service
	if cfg.Catalog.Enabled && !cfg.MCP.DiagnosticsOnly {
		store, openErr := catalogdb.Open(cfg.Catalog.DatabasePath)
		if openErr != nil {
			logger.L().Fatal("init catalog database failed", zap.Error(openErr))
		}
		defer store.Close()
		c := cfg.Catalog
		catalogService, err = catalogapp.New(context.Background(), store, application.CatalogCluster{Service: clusterService}, catalogdomain.Gateway{ID: c.GatewayID, Cluster: c.Cluster, Namespace: c.GatewayNamespace, Service: c.GatewayService, Name: c.GatewayName, PortStart: c.PortStart, PortEnd: c.PortEnd}, nil, catalogapp.WithDefaults(catalogapp.Defaults{OwnerID: c.DefaultOwner, StorageClass: c.DefaultStorageClass, RuntimeClassName: c.SandboxRuntimeClass}))
		if err != nil {
			logger.L().Fatal("init catalog failed", zap.Error(err))
		}
	}
	if catalogService != nil {
		expiryCtx, stopExpiry := context.WithCancel(context.Background())
		expiryDone := make(chan struct{})
		go func() {
			defer close(expiryDone)
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for {
				runCtx, cancelRun := context.WithTimeout(expiryCtx, 120*time.Second)
				err := catalogService.ExpireSandboxes(runCtx, time.Now().UTC())
				cancelRun()
				if err != nil && expiryCtx.Err() == nil {
					logger.L().Warn("sandbox expiry cleanup pending")
				}
				select {
				case <-expiryCtx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
		defer func() { stopExpiry(); <-expiryDone }()
	}
	r, err := router.NewWithAuth(cfg, clusterService, catalogService, authentication)
	if err != nil {
		logger.L().Fatal("init router failed", zap.Error(err))
	}

	srv := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.L().Info("server started",
			zap.String("name", cfg.App.Name),
			zap.String("version", cfg.App.Version),
			zap.String("addr", srv.Addr),
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.L().Fatal("server listen failed", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.L().Info("shutting down server")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.L().Fatal("server shutdown failed", zap.Error(err))
	}
	logger.L().Info("server exited")
}

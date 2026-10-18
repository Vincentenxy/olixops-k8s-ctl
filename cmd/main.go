package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"olixops-k8s-ctl/internal/application"
	"olixops-k8s-ctl/internal/infrastructure/config"
	"olixops-k8s-ctl/internal/infrastructure/execctl"
	"olixops-k8s-ctl/internal/infrastructure/kubeconfig"
	clusterk8s "olixops-k8s-ctl/internal/infrastructure/kubernetes"
	"olixops-k8s-ctl/internal/interfaces/router"
	"olixops-k8s-ctl/pkg/logger"

	"go.uber.org/zap"
)

func main() {
	cfg, err := config.Load("./configs")
	if err != nil {
		logger.Init("debug")
		logger.L().Fatal("load config failed", zap.Error(err))
	}

	logger.Init(cfg.Server.Mode)
	defer logger.Sync()

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
	clusterService := application.NewClusterService(clusterClient, runner, cfg.Kubernetes)
	r, err := router.New(cfg, clusterService)
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

package handler

import (
	"context"
	"github.com/gin-gonic/gin"
	"olixops-k8s-ctl/internal/application/api"
	"olixops-k8s-ctl/internal/domain"
	k8s "olixops-k8s-ctl/internal/infrastructure/kubernetes"
)

type PodDiagnostics interface {
	GetPodLogs(context.Context, k8s.LogOptions) (k8s.PodLogs, error)
	ExecPodCommand(context.Context, domain.PodExecInput) (domain.PodExecOutput, error)
}

func RegisterPodDiagnostics(v1 *gin.RouterGroup, s PodDiagnostics, exec bool) {
	pods := v1.Group("/cluster/pods")
	{
		pods.POST("/logs", operation(api.ReadTimeout, s.GetPodLogs))
		if exec {
			pods.POST("/exec", operation(api.ShellTimeout, func(ctx context.Context, in domain.PodExecInput) (domain.PodExecOutput, error) {
				out, err := s.ExecPodCommand(ctx, in)
				return out, api.ExecFailure(out, err)
			}))
		}
	}
}

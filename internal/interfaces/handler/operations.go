package handler

import (
	"bytes"
	"context"
	"github.com/gin-gonic/gin"
	"olixops-k8s-ctl/internal/application/api"
	"olixops-k8s-ctl/internal/interfaces/httpio"
	"olixops-k8s-ctl/pkg/response"
	"time"
)

// operation binds HTTP's protocol envelope only. Typed use cases are also used by MCP.
func operation[In, Out any](timeout time.Duration, call func(context.Context, In) (Out, error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		in, err := decodeJSON[In](c)
		if err != nil {
			operationResult(c, nil, err)
			return
		}
		out, err := call(ctx, in)
		operationResult(c, out, err)
	}
}
func operationResult(c *gin.Context, out any, err error) {
	if err == nil {
		response.Success(c, out)
		return
	}
	e := api.Classify(err)
	c.AbortWithStatusJSON(e.Status, gin.H{"code": e.Status, "msg": e.Message, "data": gin.H{"reason": e.Reason, "result": out}})
}

func RegisterOperations(v1 *gin.RouterGroup, service api.Service, writeTimeout time.Duration) {
	if writeTimeout <= 0 {
		writeTimeout = 120 * time.Second
	}
	o := api.Operations{Service: service}
	cluster := v1.Group("/cluster")
	{
		namespaces := cluster.Group("/namespaces")
		{
			namespaces.POST("/create", operation(writeTimeout, o.NamespaceCreate))
			namespaces.POST("/delete", operation(writeTimeout, o.NamespaceDelete))
			namespaces.POST("/metadata", operation(writeTimeout, o.NamespaceMetadata))
		}
		resources := cluster.Group("/resources")
		{
			resources.POST("/list", operation(api.ReadTimeout, o.Objects))
			resources.POST("/summary", operation(api.ReadTimeout, o.Resources))
			resources.POST("/get", operation(api.ReadTimeout, o.Resource))
			resources.POST("/yaml", operation(api.ReadTimeout, o.YAML))
			resources.POST("/delete", operation(writeTimeout, o.Delete))
		}
		cluster.POST("/events/list", operation(api.ReadTimeout, o.Events))
		cluster.POST("/gateways/list", operation(api.ReadTimeout, o.GatewayResources))
		workloads := cluster.Group("/workloads")
		{
			workloads.POST("/rollout", operation(api.ReadTimeout, o.Rollout))
			workloads.POST("/history", operation(api.ReadTimeout, o.History))
			workloads.POST("/scale", operation(writeTimeout, o.Scale))
			workloads.POST("/restart", operation(writeTimeout, o.Restart))
			workloads.POST("/rollback", operation(writeTimeout, o.Rollback))
		}
		kubectl := cluster.Group("/kubectl")
		{
			kubectl.POST("/validate", operation(api.ReadTimeout, o.Validate))
			kubectl.POST("/dry-run", operation(writeTimeout, o.DryRun))
		}
		helm := cluster.Group("/helm")
		{
			helm.POST("/list", operation(api.ReadTimeout, o.HelmList))
			helm.POST("/status", operation(api.ReadTimeout, o.HelmStatus))
			helm.POST("/history", operation(api.ReadTimeout, o.HelmHistory))
			helm.POST("/rollback", operation(writeTimeout, o.HelmRollback))
			helm.POST("/uninstall", operation(writeTimeout, o.HelmUninstall))
		}
		cluster.POST("/configmaps/update", operation(writeTimeout, o.ConfigMap))
	}
}

type TerminalOperations = api.TerminalService

func RegisterTerminals(v1 *gin.RouterGroup, s TerminalOperations) {
	terminals := v1.Group("/cluster/pods/terminals")
	{
		terminals.POST("/open", operation(api.ShellTimeout, s.OpenPodTerminal))
		terminals.POST("/write", operation(api.ShellTimeout, s.WritePodTerminal))
		terminals.POST("/read", operation(api.ShellTimeout, s.ReadPodTerminal))
		terminals.POST("/close", operation(api.ShellTimeout, s.ClosePodTerminal))
	}
	files := v1.Group("/cluster/pods/files")
	{
		files.POST("/prepare-download", operation(api.ShellTimeout, s.DownloadPodFile))
		files.POST("/prepare-upload", operation(api.ShellTimeout, s.UploadPodFile))
	}
}

func decodeJSON[In any](c *gin.Context) (In, error) {
	raw, err := httpio.ReadJSONBody(c.Writer, c.Request, api.MaxJSONBytes)
	if err != nil {
		var zero In
		return zero, err
	}
	return api.Decode[In](bytes.NewReader(raw))
}

package application

import (
	"context"
	"olixops-k8s-ctl/internal/application/shell/interceptor"
	"olixops-k8s-ctl/internal/domain/fault"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/util/validation"
	"olixops-k8s-ctl/internal/domain"
	authdomain "olixops-k8s-ctl/internal/domain/auth"
	"olixops-k8s-ctl/pkg/logger"
)

func WithPodExecutor(backend domain.PodExecutor, namespaces []string) ServiceOption {
	return func(s *ClusterService) {
		s.podExecutor = backend
		s.execNamespaces = map[string]bool{}
		for _, ns := range namespaces {
			s.execNamespaces[ns] = true
		}
	}
}

// WithShell configures single-command execution; nil policies fail closed when enabled.
func WithShell(enabled, intercept bool, policy interceptor.Interceptor) ServiceOption {
	return func(s *ClusterService) {
		s.shellEnabled = enabled
		s.shellIntercept = intercept
		s.shellInterceptor = policy
	}
}

func (s *ClusterService) ExecPodCommand(ctx context.Context, in domain.PodExecInput) (out domain.PodExecOutput, err error) {
	out = domain.PodExecOutput{Outcome: "not_executed"}
	principal, ok := authdomain.FromContext(ctx)
	if !ok {
		return out, fault.Unauthenticated("authenticated identity required")
	}
	start := time.Now()
	defer func() {
		out.DurationMS = time.Since(start).Milliseconds()
		logger.Info(ctx, "pod exec", zap.String("userId", principal.UserID), zap.String("tokenId", principal.TokenID), zap.String("namespace", in.Namespace), zap.String("pod", in.Pod), zap.String("container", in.Container), zap.String("action", in.Action), zap.String("outcome", out.Outcome), zap.Bool("success", err == nil && out.Outcome == "completed" && !out.UpstreamError && !out.TimedOut))
	}()
	if s.podExecutor == nil {
		return out, fault.Disabled("Pod exec disabled")
	}
	if !s.execNamespaces[in.Namespace] || in.Namespace == "default" || in.Namespace == "olixops" || strings.HasPrefix(in.Namespace, "kube-") {
		return out, fault.Forbidden("exec namespace not allowed")
	}
	target, err := s.namedTarget(in.Namespace, "Pod", in.Pod)
	if err != nil {
		return out, err
	}
	if in.Container == "" || len(validation.IsDNS1123Label(in.Container)) > 0 {
		return out, fault.Invalid("explicit valid container required")
	}
	if in.Action == "shell" {
		if !s.shellEnabled {
			return out, fault.Disabled("Shell execution disabled")
		}
		if strings.TrimSpace(in.Command) == "" || len(in.Command) > 65536 || strings.ContainsRune(in.Command, 0) {
			return out, fault.Invalid("command must be nonempty, at most 64 KiB and contain no NUL")
		}
	} else if in.Command != "" {
		return out, fault.Invalid("command is only accepted for shell")
	}
	if in.Action != "read_file" && in.Path != "" {
		return out, fault.Invalid("path is only accepted for read_file")
	}
	if in.Action != "process_status" && in.PID != 0 {
		return out, fault.Invalid("pid is only accepted for process_status")
	}
	grant, err := s.diagnosticGrant(ctx, in.Namespace, in.Pod, in.Container, in.Action, in.Path)
	if err != nil {
		return out, err
	}
	commands := map[string][]string{"redis_ping": {"redis-cli", "PING"}, "nginx_check": {"nginx", "-t"}, "disk_usage": {"df", "-h", "/"}, "process_list": {"ls", "-1", "/proc"}, "environment": {"env"}, "memory_usage": {"cat", "/sys/fs/cgroup/memory.current", "/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory.stat"}}
	if in.Action == "process_status" {
		if in.PID == 0 {
			in.PID = 1
		}
		if in.PID < 1 || in.PID > 4194304 {
			return out, fault.Invalid("invalid process PID")
		}
		commands[in.Action] = []string{"cat", "/proc/" + strconv.Itoa(in.PID) + "/status"}
	}
	if in.Action == "read_file" {
		if grant == nil || !domain.ValidDiagnosticFilePath(in.Path) {
			return out, fault.Forbidden("explicit file grant required")
		}
		commands[in.Action] = []string{"perl", "-e", diagnosticFileReader, "--", in.Path}
	}
	if in.Action == "shell" {
		commands[in.Action] = []string{"/bin/sh", "-c", in.Command}
	}
	argv, ok := commands[in.Action]
	if !ok {
		return out, fault.Invalid("unsupported exec action")
	}
	if in.TimeoutSeconds == 0 {
		in.TimeoutSeconds = 10
	}
	if in.TimeoutSeconds < 1 || in.TimeoutSeconds > 30 {
		return out, fault.Invalid("exec timeout must be 1-30 seconds")
	}
	// One budget covers policy, target inspection, MCP initialization and execution.
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(in.TimeoutSeconds)*time.Second)
	defer cancel()
	if err = s.authorize(runCtx, "pod.exec", true, []domain.Target{target}, in); err != nil {
		if runCtx.Err() == context.DeadlineExceeded {
			out.TimedOut = true
			return out, nil
		}
		return out, err
	}
	var approvedUIDs []string
	if grant != nil && grant.PodUID != "" {
		approvedUIDs = []string{grant.PodUID}
	}
	uid, err := s.client.CheckPodExecTarget(runCtx, in.Namespace, in.Pod, in.Container, approvedUIDs...)
	if err != nil {
		if runCtx.Err() == context.DeadlineExceeded {
			out.TimedOut = true
			return out, nil
		}
		return out, err
	}
	if grant != nil && grant.PodUID != "" && uid != grant.PodUID {
		return out, fault.Conflict("diagnostic Pod UID changed")
	}
	// Do not submit work if inspection consumed the remaining budget.
	if err = runCtx.Err(); err != nil {
		if err == context.DeadlineExceeded {
			out.TimedOut = true
			return out, nil
		}
		return out, err
	}

	if in.Action == "shell" && s.shellIntercept {
		if err = interceptor.Evaluate(runCtx, s.shellInterceptor, interceptor.Request{UserID: principal.UserID, Namespace: in.Namespace, Pod: in.Pod, PodUID: uid, Container: in.Container, Command: in.Command, Operation: "shell.exec"}); err != nil {
			return out, err
		}
	}
	if runCtx.Err() != nil {
		out.TimedOut = true
		return out, nil
	}
	out, err = s.podExecutor.Execute(runCtx, in.Namespace, in.Pod, in.Container, argv)
	if in.Action == "memory_usage" && err == nil && out.UpstreamError && runCtx.Err() == nil {
		out, err = s.podExecutor.Execute(runCtx, in.Namespace, in.Pod, in.Container, []string{"cat", "/sys/fs/cgroup/memory/memory.usage_in_bytes", "/sys/fs/cgroup/memory/memory.limit_in_bytes", "/sys/fs/cgroup/memory/memory.stat"})
	}
	if in.Action == "read_file" && len(out.Output) > 65536 {
		out.Output = out.Output[:65536]
		for !utf8.ValidString(out.Output) {
			out.Output = out.Output[:len(out.Output)-1]
		}
		out.Truncated = true
	}
	out.PodUID = uid
	if runCtx.Err() == context.DeadlineExceeded {
		if out.Outcome != "not_executed" {
			out.Outcome = "unknown"
		}
		out.TimedOut = true
		err = nil
	}
	return out, err
}

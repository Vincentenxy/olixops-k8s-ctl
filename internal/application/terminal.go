package application

import (
	"context"
	"olixops-k8s-ctl/internal/domain/fault"
	"time"

	sessions "olixops-k8s-ctl/internal/application/shell"
	"olixops-k8s-ctl/internal/application/shell/interceptor"
	"olixops-k8s-ctl/internal/domain"
	authdomain "olixops-k8s-ctl/internal/domain/auth"
)

func WithTerminals(backend domain.PodStreamBackend, manager *sessions.Manager) ServiceOption {
	return func(s *ClusterService) { s.streamBackend = backend; s.terminals = manager }
}
func (s *ClusterService) terminalOwner(ctx context.Context) (string, error) {
	p, ok := authdomain.FromContext(ctx)
	if !ok || !s.shellEnabled || s.terminals == nil || s.streamBackend == nil {
		return "", fault.Disabled("authenticated shell sessions unavailable")
	}
	return p.UserID, nil
}
func (s *ClusterService) shellTarget(ctx context.Context, t domain.TerminalTarget, action string) (sessions.Target, error) {
	if !s.execNamespaces[t.Namespace] || t.Container == "" {
		return sessions.Target{}, fault.Forbidden("shell target not allowed")
	}
	target, err := s.namedTarget(t.Namespace, "Pod", t.Pod)
	if err != nil {
		return sessions.Target{}, err
	}
	grant, err := s.diagnosticGrant(ctx, t.Namespace, t.Pod, t.Container, action, "")
	if err != nil {
		return sessions.Target{}, err
	}
	if err = s.authorize(ctx, "pod.exec", true, []domain.Target{target}, t); err != nil {
		return sessions.Target{}, err
	}
	var uids []string
	if grant != nil && grant.PodUID != "" {
		uids = []string{grant.PodUID}
	}
	uid, err := s.client.CheckPodExecTarget(ctx, t.Namespace, t.Pod, t.Container, uids...)
	if err != nil {
		return sessions.Target{}, err
	}
	if grant != nil && grant.PodUID != "" && grant.PodUID != uid {
		return sessions.Target{}, fault.Conflict("shell target UID changed")
	}
	return sessions.Target{TerminalTarget: t, UID: uid}, nil
}
func (s *ClusterService) shellCheck(ctx context.Context, owner string, t sessions.Target, op, id, command string) error {
	if !s.shellIntercept {
		return nil
	}
	return interceptor.Evaluate(ctx, s.shellInterceptor, interceptor.Request{UserID: owner, Namespace: t.Namespace, Pod: t.Pod, PodUID: t.UID, Container: t.Container, Command: command, Operation: op, SessionID: id})
}
func (s *ClusterService) OpenPodTerminal(ctx context.Context, in domain.TerminalOpenInput) (domain.TerminalResult, error) {
	owner, err := s.terminalOwner(ctx)
	if err != nil {
		return domain.TerminalResult{}, err
	}
	t, err := s.shellTarget(ctx, in.TerminalTarget, "shell")
	if err != nil {
		return domain.TerminalResult{}, err
	}
	if err = s.shellCheck(ctx, owner, t, "terminal.open", "", "/bin/sh"); err != nil {
		return domain.TerminalResult{}, err
	}
	if err = ctx.Err(); err != nil {
		return domain.TerminalResult{}, err
	}
	return s.terminals.Open(owner, t, in.TTY, s.streamBackend)
}
func (s *ClusterService) ownedTerminal(ctx context.Context, id, op, input string, checkTarget bool) (string, *sessions.Session, error) {
	owner, err := s.terminalOwner(ctx)
	if err != nil {
		return "", nil, err
	}
	session, err := s.terminals.Inspect(owner, id)
	if err != nil {
		return "", nil, err
	}
	if checkTarget {
		target, e := s.shellTarget(ctx, session.Target.TerminalTarget, "shell")
		if e != nil {
			s.terminals.End(owner, id)
			return "", nil, e
		}
		if target.UID != session.Target.UID {
			s.terminals.End(owner, id)
			return "", nil, fault.Conflict("terminal Pod replaced; session closed")
		}
	}
	if err = s.shellCheck(ctx, owner, session.Target, op, id, input); err != nil {
		return "", nil, err
	}
	return owner, session, nil
}
func (s *ClusterService) WritePodTerminal(ctx context.Context, in domain.TerminalWriteInput) (domain.TerminalResult, error) {
	if len(in.Input) == 0 || len(in.Input) > 16384 {
		return domain.TerminalResult{}, fault.Invalid("terminal input must be 1-16384 bytes")
	}
	_, session, err := s.ownedTerminal(ctx, in.SessionID, "terminal.write", in.Input, true)
	if err != nil {
		return domain.TerminalResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.terminals.Send(ctx, session, in.Input)
}
func (s *ClusterService) ReadPodTerminal(ctx context.Context, in domain.TerminalReadInput) (domain.TerminalResult, error) {
	if in.WaitSeconds < 0 || in.WaitSeconds > 5 {
		return domain.TerminalResult{}, fault.Invalid("waitSeconds must be 0-5")
	}
	_, session, err := s.ownedTerminal(ctx, in.SessionID, "terminal.read", "", true)
	if err != nil {
		return domain.TerminalResult{}, err
	}
	return s.terminals.Read(ctx, session, in.Cursor, time.Duration(in.WaitSeconds)*time.Second)
}
func (s *ClusterService) ClosePodTerminal(ctx context.Context, in domain.TerminalSessionInput) (domain.TerminalResult, error) {
	owner, _, err := s.ownedTerminal(ctx, in.SessionID, "terminal.close", "", false)
	if err != nil {
		return domain.TerminalResult{}, err
	}
	return s.terminals.End(owner, in.SessionID)
}

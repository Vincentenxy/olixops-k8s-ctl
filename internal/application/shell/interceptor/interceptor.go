// Package interceptor centralizes container shell policy. It does not execute commands.
package interceptor

import "context"

type Request struct {
	UserID, Namespace, Pod, PodUID, Container, Command string
	Operation, SessionID                               string
}
type Decision struct {
	Allowed bool
	Code    string
	Message string
}
type Interceptor interface {
	Check(context.Context, Request) (Decision, error)
}

// AllowAll reserves the policy seam; it provides no dangerous-command protection.
type AllowAll struct{}

func (AllowAll) Check(context.Context, Request) (Decision, error) {
	return Decision{Allowed: true}, nil
}

// Rejection is safe to expose to clients. Policy implementations must not put secrets in Message.
type Rejection struct{ Code, Message string }

func (e *Rejection) Error() string { return e.Code + ": " + e.Message }

// Evaluate fails closed on missing implementations and policy errors.
func Evaluate(ctx context.Context, policy Interceptor, request Request) error {
	if policy == nil {
		return &Rejection{"SHELL_POLICY_ERROR", "Shell policy is not configured"}
	}
	decision, err := policy.Check(ctx, request)
	if err != nil {
		return &Rejection{"SHELL_POLICY_ERROR", "Shell policy check failed; command was not executed"}
	}
	if !decision.Allowed {
		if decision.Code == "" {
			decision.Code = "SHELL_COMMAND_BLOCKED"
		}
		if decision.Message == "" {
			decision.Message = "Command rejected by Shell policy"
		}
		return &Rejection{decision.Code, decision.Message}
	}
	return nil
}

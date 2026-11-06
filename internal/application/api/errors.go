package api

import (
	"context"
	"errors"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"olixops-k8s-ctl/internal/application/shell/interceptor"
	"olixops-k8s-ctl/internal/domain"
	auth "olixops-k8s-ctl/internal/domain/auth"
	catalog "olixops-k8s-ctl/internal/domain/catalog"
	"olixops-k8s-ctl/internal/domain/fault"
)

// Failure is the shared public meaning of an error, never an upstream error dump.
type Failure struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
	Status  int    `json:"-"`
}

func (e Failure) Error() string { return e.Reason + ": " + e.Message }
func Classify(err error) Failure {
	var f *fault.Error
	var rejected *interceptor.Rejection
	var business *catalog.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return Failure{"deadline_exceeded", "Operation deadline exceeded; a write may already have taken effect", 504}
	case errors.Is(err, context.Canceled):
		return Failure{"canceled", "Operation canceled; a write may already have taken effect", 408}
	case errors.Is(err, auth.ErrUnauthenticated):
		return Failure{"unauthenticated", "Authentication required", 401}
	case errors.As(err, &rejected):
		return Failure{rejected.Code, rejected.Message, 403}
	case errors.As(err, &f):
		status := map[string]int{"invalid_argument": 400, "unauthenticated": 401, "forbidden": 403, "not_found": 404, "conflict": 409, "feature_disabled": 403, "upstream_unavailable": 503, "execution_unknown": 502, "deadline_exceeded": 504}[f.Reason]
		if status == 0 {
			status = 500
		}
		return Failure{f.Reason, f.Message, status}
	case kerrors.IsUnauthorized(err):
		return Failure{"upstream_unauthorized", "Kubernetes credentials were rejected", 503}
	case kerrors.IsForbidden(err):
		return Failure{"forbidden", "Kubernetes RBAC denied the operation", 403}
	case kerrors.IsNotFound(err):
		return Failure{"not_found", "Resource not found", 404}
	case kerrors.IsAlreadyExists(err), kerrors.IsConflict(err):
		return Failure{"conflict", "Resource already exists or changed; read current state before retrying", 409}
	case kerrors.IsInvalid(err), kerrors.IsBadRequest(err):
		return Failure{"invalid_argument", "Kubernetes rejected the resource parameters", 400}
	case kerrors.IsTimeout(err), kerrors.IsServerTimeout(err):
		return Failure{"deadline_exceeded", "Kubernetes operation timed out; result may be unknown", 504}
	case errors.As(err, &business):
		return Failure{business.Code, business.Message, 400}
	default:
		return Failure{"upstream_unavailable", "Operation failed; check backend availability and request ID", 503}
	}
}

func ExecFailure(out domain.PodExecOutput, err error) error {
	if out.Outcome == "unknown" {
		return fault.Unknown("Command result is unknown; do not retry automatically")
	}
	if err != nil {
		return err
	}
	if out.TimedOut {
		if out.Outcome == "not_executed" {
			return fault.New("deadline_exceeded", "Command deadline exceeded before execution")
		}
		return fault.Unknown("Command timed out; remote execution may continue")
	}
	if out.UpstreamError {
		return fault.Unknown("Command returned an upstream error; inspect the execution result")
	}
	return nil
}

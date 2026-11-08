package api

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"olixops-k8s-ctl/internal/domain"
	catalog "olixops-k8s-ctl/internal/domain/catalog"
	"olixops-k8s-ctl/internal/domain/fault"
	"testing"
)

func TestStableErrorReasons(t *testing.T) {
	for _, tc := range []struct {
		err    error
		reason string
		status int
	}{
		{catalog.Fail("NOT_FOUND", "not found"), "NOT_FOUND", 404},
		{catalog.Fail("REVISION_CONFLICT", "revision conflict"), "REVISION_CONFLICT", 409},
		{catalog.Fail("DEPLOYMENT_RESULT_UNKNOWN", "unknown"), "DEPLOYMENT_RESULT_UNKNOWN", 502},
		{fault.Invalid("bad name"), "invalid_argument", 400},
		{fault.Forbidden("blocked"), "forbidden", 403},
		{context.DeadlineExceeded, "deadline_exceeded", 504},
		{context.Canceled, "canceled", 408},
		{kerrors.NewNotFound(schema.GroupResource{Resource: "pods"}, "app"), "not_found", 404},
		{kerrors.NewConflict(schema.GroupResource{Resource: "pods"}, "app", errors.New("private token")), "conflict", 409},
		{errors.New("private token and kubeconfig"), "upstream_unavailable", 503},
	} {
		e := Classify(tc.err)
		require.Equal(t, tc.status, e.Status)
		require.Equal(t, tc.reason, e.Reason)
		require.NotContains(t, e.Message, "private")
	}
	require.Equal(t, "execution_unknown", Classify(ExecFailure(domain.PodExecOutput{Outcome: "unknown"}, nil)).Reason)
	require.Equal(t, 504, Classify(ExecFailure(domain.PodExecOutput{Outcome: "not_executed", TimedOut: true}, nil)).Status)
}

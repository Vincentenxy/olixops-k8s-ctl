package tests

import (
	"context"
	"github.com/stretchr/testify/require"
	"olixops-k8s-ctl/internal/application"
	contract "olixops-k8s-ctl/internal/application/api"
	"testing"
	"time"
)

func TestCommandCancellationPreservesMutationUncertainty(t *testing.T) {
	// Helpers run this test binary as a CLI. Race's default one-second exit
	// sleep must not consume the deadline during Helm's preflight queries.
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	for _, tc := range []struct {
		name, command, reason string
		run                   func(context.Context, *application.ClusterService) error
	}{
		{"read", "status", "deadline_exceeded", func(ctx context.Context, s *application.ClusterService) error {
			_, err := s.GetHelmStatus(ctx, "demo", "web")
			return err
		}},
		{"apply", "apply", "execution_unknown", func(ctx context.Context, s *application.ClusterService) error {
			_, _, err := s.ApplyManifest(ctx, demoManifest, "demo", false, false)
			return err
		}},
		{"dry_run", "apply", "deadline_exceeded", func(ctx context.Context, s *application.ClusterService) error {
			_, _, err := s.ApplyManifest(ctx, demoManifest, "demo", false, true)
			return err
		}},
		{"rollback", "rollout", "execution_unknown", func(ctx context.Context, s *application.ClusterService) error {
			_, err := s.RollbackWorkload(ctx, "demo", "Deployment", "web", 1)
			return err
		}},
		{"helm_rollback", "rollback", "execution_unknown", func(ctx context.Context, s *application.ClusterService) error {
			_, err := s.RollbackHelm(ctx, "demo", "web", 1, false)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newOperationFixture(t)
			t.Setenv("OLIXOPS_TEST_DELAY_COMMAND", tc.command)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := tc.run(ctx, f.service)
			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.Equal(t, tc.reason, contract.Classify(err).Reason)
			count := 0
			for _, record := range readCommands(t, f.commandLog) {
				for _, arg := range record.Args {
					if arg == tc.command {
						count++
						break
					}
				}
			}
			require.Equal(t, 1, count, "must not automatically retry an interrupted command")
		})
	}
	t.Run("canceled_before_execution", func(t *testing.T) {
		f := newOperationFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, err := f.service.ApplyManifest(ctx, demoManifest, "demo", false, false)
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, "canceled", contract.Classify(err).Reason)
		require.Empty(t, readCommands(t, f.commandLog))
	})
}

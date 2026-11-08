package shell

import (
	"context"
	"github.com/stretchr/testify/require"
	"io"
	"testing"
	"time"
)

type partiallyReadingStream struct{ received chan struct{} }

func (b partiallyReadingStream) Stream(ctx context.Context, _, _, _ string, _ []string, _ bool, in io.Reader, _, _ io.Writer) error {
	var buf [1]byte
	if _, err := io.ReadFull(in, buf[:]); err != nil {
		return err
	}
	close(b.received)
	<-ctx.Done()
	return ctx.Err()
}
func TestQueuedWriteHonorsCancellation(t *testing.T) {
	m := NewManager(time.Minute, time.Hour, 2, 1)
	defer m.Close()
	received := make(chan struct{})
	opened, err := m.Open("alice", Target{}, true, partiallyReadingStream{received})
	require.NoError(t, err)
	s, err := m.Inspect("alice", opened.SessionID)
	require.NoError(t, err)
	firstCtx, stop := context.WithCancel(context.Background())
	defer stop()
	firstDone := make(chan struct{})
	go func() { defer close(firstDone); _, _ = m.Send(firstCtx, s, "first write") }()
	select {
	case <-received:
	case <-time.After(time.Second):
		t.Fatal("first write did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := m.Send(ctx, s, "second"); done <- err }()
	blocked := false
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(250 * time.Millisecond):
		blocked = true
	}
	// A canceled queued request must not disconnect the active writer's session.
	state, err := m.Read(context.Background(), s, 0, 0)
	require.NoError(t, err)
	require.Equal(t, "open", state.State)
	stop()
	<-firstDone
	if blocked {
		<-done
	}
	require.False(t, blocked, "queued write ignored its own cancellation")
}

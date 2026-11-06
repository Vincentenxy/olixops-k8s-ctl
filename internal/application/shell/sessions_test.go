package shell

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type echoStream struct{}

func (echoStream) Stream(ctx context.Context, ns, pod, container string, argv []string, tty bool, in io.Reader, out, errout io.Writer) error {
	if fmt.Sprint(argv) != "[/bin/sh]" {
		return fmt.Errorf("wrong command")
	}
	// A fake persistent shell: every input is echoed on the same stream.
	_, err := io.Copy(out, in)
	return err
}
func TestSessionOwnershipCursorAndLimits(t *testing.T) {
	m := NewManager(time.Minute, time.Hour, 2, 1)
	defer m.Close()
	opened, err := m.Open("alice", Target{}, true, echoStream{})
	require.NoError(t, err)
	_, err = m.Inspect("bob", opened.SessionID)
	require.Error(t, err)
	_, err = m.End("bob", opened.SessionID)
	require.Error(t, err)
	_, err = m.Open("alice", Target{}, false, echoStream{})
	require.Error(t, err)
	s, err := m.Inspect("alice", opened.SessionID)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = m.Send(ctx, s, "cd /tmp\n")
	require.NoError(t, err)
	out, err := m.Read(ctx, s, 0, time.Second)
	require.NoError(t, err)
	b, err := base64.StdEncoding.DecodeString(out.OutputBase64)
	require.NoError(t, err)
	require.Equal(t, "cd /tmp\n", string(b))
	_, err = m.Send(ctx, s, "pwd\n")
	require.NoError(t, err)
	next, err := m.Read(ctx, s, out.NextCursor, time.Second)
	require.NoError(t, err)
	b, err = base64.StdEncoding.DecodeString(next.OutputBase64)
	require.NoError(t, err)
	require.Equal(t, "pwd\n", string(b))
	_, err = m.Read(ctx, s, next.NextCursor+1, 0)
	require.Error(t, err)
	s.Write([]byte(strings.Repeat("x", bufferLimit+3)))
	overflow, err := m.Read(ctx, s, 0, 0)
	require.NoError(t, err)
	require.Positive(t, overflow.LostBytes)
	require.Equal(t, int64(readLimit)+overflow.LostBytes, overflow.NextCursor)
	_, err = m.End("alice", s.ID)
	require.NoError(t, err)
	_, err = m.Inspect("alice", s.ID)
	require.Error(t, err)
}
func TestExpirationAndShutdown(t *testing.T) {
	m := NewManager(time.Second, time.Hour, 2, 2)
	opened, err := m.Open("alice", Target{}, false, echoStream{})
	require.NoError(t, err)
	s, err := m.Inspect("alice", opened.SessionID)
	require.NoError(t, err)
	s.mu.Lock()
	s.touched = time.Now().Add(-2 * time.Second)
	s.mu.Unlock()
	m.expire(time.Now())
	_, err = m.Inspect("alice", opened.SessionID)
	require.Error(t, err)
	m.Close()
	_, err = m.Open("alice", Target{}, false, echoStream{})
	require.Error(t, err)
}

type blockedStream struct{}

func (blockedStream) Stream(ctx context.Context, _ string, _ string, _ string, _ []string, _ bool, _ io.Reader, _ io.Writer, _ io.Writer) error {
	<-ctx.Done()
	return ctx.Err()
}
func TestInputTimeoutClosesSessionWithoutClaimingRemoteExit(t *testing.T) {
	m := NewManager(time.Minute, time.Hour, 2, 1)
	defer m.Close()
	opened, err := m.Open("alice", Target{}, true, blockedStream{})
	require.NoError(t, err)
	s, err := m.Inspect("alice", opened.SessionID)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = m.Send(ctx, s, "sleep 100\n")
	require.Error(t, err)
	out, err := m.Read(context.Background(), s, 0, 0)
	require.NoError(t, err)
	require.Equal(t, "disconnected", out.State)
	require.False(t, out.RemoteExitConfirmed)
}
func TestControlCharactersRemainRawInput(t *testing.T) {
	m := NewManager(time.Minute, time.Hour, 2, 1)
	defer m.Close()
	opened, err := m.Open("alice", Target{}, true, echoStream{})
	require.NoError(t, err)
	s, err := m.Inspect("alice", opened.SessionID)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = m.Send(ctx, s, "\x03")
	require.NoError(t, err)
	out, err := m.Read(ctx, s, 0, time.Second)
	require.NoError(t, err)
	decoded, err := base64.StdEncoding.DecodeString(out.OutputBase64)
	require.NoError(t, err)
	require.Equal(t, []byte{3}, decoded)
	require.False(t, out.RemoteExitConfirmed)
}

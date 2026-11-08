package application

import (
	"github.com/stretchr/testify/require"
	sessions "olixops-k8s-ctl/internal/application/shell"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTransferTicketsOwnershipExpiryAndSingleUse(t *testing.T) {
	var store ticketStore
	id, _, err := store.prepare("alice", sessions.Target{}, "/tmp/file")
	require.NoError(t, err)
	_, err = store.inspect("bob", id)
	require.Error(t, err)
	require.Error(t, store.consume("bob", id))
	_, err = store.inspect("alice", id)
	require.NoError(t, err)
	var successes atomic.Int32
	var workers sync.WaitGroup
	for range 32 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if store.consume("alice", id) == nil {
				successes.Add(1)
			}
		}()
	}
	workers.Wait()
	require.Equal(t, int32(1), successes.Load())
	_, err = store.inspect("alice", id)
	require.Error(t, err)
	id, _, err = store.prepare("alice", sessions.Target{}, "/tmp/expired")
	require.NoError(t, err)
	// Expiration may occur while the caller is checking Pod UID/policy after inspect.
	_, err = store.inspect("alice", id)
	require.NoError(t, err)
	ticket := store.tickets[id]
	ticket.Expires = time.Now().Add(-time.Second)
	store.tickets[id] = ticket
	require.Error(t, store.consume("alice", id))
	_, err = store.inspect("alice", id)
	require.Error(t, err)
	_, _, err = store.prepare("alice", sessions.Target{}, "/tmp/new")
	require.NoError(t, err)
	require.NotContains(t, store.tickets, id)
	var uploads ticketStore
	_, err = uploads.inspect("alice", id)
	require.Error(t, err)
}

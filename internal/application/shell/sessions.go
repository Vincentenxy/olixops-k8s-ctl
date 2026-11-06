// Package shell manages bounded, user-owned container terminal sessions.
package shell

import (
	"context"
	"encoding/base64"
	"io"
	"olixops-k8s-ctl/internal/domain/fault"
	"sync"
	"time"

	"github.com/google/uuid"
	"olixops-k8s-ctl/internal/domain"
)

const bufferLimit = 256 << 10
const readLimit = 64 << 10

type Target struct {
	domain.TerminalTarget
	UID string
}
type Session struct {
	mu               sync.Mutex
	writeMu          sync.Mutex
	ID, Owner        string
	Target           Target
	created, touched time.Time
	cancel           context.CancelFunc
	input            *io.PipeWriter
	reader           *io.PipeReader
	state            string
	data             []byte
	base             int64
	changed          chan struct{}
}
type Manager struct {
	mu             sync.Mutex
	sessions       map[string]*Session
	root           context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
	idle, lifetime time.Duration
	total, perUser int
	closed         bool
}

func NewManager(idle, lifetime time.Duration, total, perUser int) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{sessions: map[string]*Session{}, root: ctx, cancel: cancel, idle: idle, lifetime: lifetime, total: total, perUser: perUser}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				m.expire(now)
			}
		}
	}()
	return m
}
func (m *Manager) expire(now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, s := range m.sessions {
		s.mu.Lock()
		expired := now.Sub(s.touched) >= m.idle || now.Sub(s.created) >= m.lifetime
		s.mu.Unlock()
		if expired {
			s.stop("expired")
			delete(m.sessions, id)
		}
	}
}
func (s *Session) signal() { close(s.changed); s.changed = make(chan struct{}) }
func (s *Session) stop(state string) {
	s.cancel()
	s.input.Close()
	s.reader.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
	s.signal()
}
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	m.cancel()
	for id, s := range m.sessions {
		s.stop("closed")
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	m.wg.Wait()
}

// Inspect deliberately gives the same error for missing sessions and another user's sessions.
func (m *Manager) Inspect(owner, id string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil || s.Owner != owner {
		return nil, fault.NotFound("terminal session unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.touched) >= m.idle || time.Since(s.created) >= m.lifetime {
		return nil, fault.NotFound("terminal session expired")
	}
	return s, nil
}
func (m *Manager) Open(owner string, target Target, tty bool, backend domain.PodStreamBackend) (domain.TerminalResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return domain.TerminalResult{}, fault.Disabled("terminal manager closed")
	}
	n := 0
	for _, s := range m.sessions {
		if s.Owner == owner {
			n++
		}
	}
	if len(m.sessions) >= m.total || n >= m.perUser {
		return domain.TerminalResult{}, fault.Conflict("terminal session limit reached")
	}
	ctx, cancel := context.WithTimeout(m.root, m.lifetime)
	r, w := io.Pipe()
	now := time.Now()
	s := &Session{ID: uuid.NewString(), Owner: owner, Target: target, created: now, touched: now, cancel: cancel, input: w, reader: r, state: "open", changed: make(chan struct{})}
	m.sessions[s.ID] = s
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		err := backend.Stream(ctx, target.Namespace, target.Pod, target.Container, []string{"/bin/sh"}, tty, r, s, s)
		r.Close()
		w.Close()
		s.mu.Lock()
		if s.state == "open" {
			if ctx.Err() != nil {
				s.state = "disconnected"
			} else if err != nil {
				s.state = "failed"
			} else {
				s.state = "exited"
			}
			s.signal()
		}
		s.mu.Unlock()
		cancel()
	}()
	return domain.TerminalResult{SessionID: s.ID, State: "open"}, nil
}
func (s *Session) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(p)
	s.data = append(s.data, p...)
	if len(s.data) > bufferLimit {
		drop := len(s.data) - bufferLimit
		s.data = append([]byte{}, s.data[drop:]...)
		s.base += int64(drop)
	}
	s.signal()
	return n, nil
}
func (m *Manager) Send(ctx context.Context, s *Session, input string) (domain.TerminalResult, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if ctx.Err() != nil {
		return domain.TerminalResult{}, ctx.Err()
	}
	s.mu.Lock()
	state := s.state
	s.mu.Unlock()
	if state != "open" {
		return domain.TerminalResult{}, fault.Conflict("terminal is not open")
	}
	done := make(chan error, 1)
	go func() { _, err := io.WriteString(s.input, input); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			return domain.TerminalResult{}, fault.Unknown("terminal input failed")
		}
	case <-ctx.Done():
		s.stop("disconnected")
		return domain.TerminalResult{}, fault.Unknown("terminal input timed out; remote process state unknown")
	}
	s.mu.Lock()
	s.touched = time.Now()
	state = s.state
	s.mu.Unlock()
	return domain.TerminalResult{SessionID: s.ID, State: state}, nil
}
func (m *Manager) Read(ctx context.Context, s *Session, cursor int64, wait time.Duration) (domain.TerminalResult, error) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		s.mu.Lock()
		end := s.base + int64(len(s.data))
		if cursor < 0 || cursor > end {
			s.mu.Unlock()
			return domain.TerminalResult{}, fault.Invalid("invalid output cursor")
		}
		lost := int64(0)
		if cursor < s.base {
			lost = s.base - cursor
			cursor = s.base
		}
		if cursor < end || s.state != "open" || wait == 0 {
			n := end - cursor
			if n > readLimit {
				n = readLimit
			}
			data := append([]byte{}, s.data[cursor-s.base:cursor-s.base+n]...)
			s.touched = time.Now()
			out := domain.TerminalResult{SessionID: s.ID, State: s.state, OutputBase64: base64.StdEncoding.EncodeToString(data), NextCursor: cursor + n, LostBytes: lost, RemoteExitConfirmed: s.state == "exited"}
			s.mu.Unlock()
			return out, nil
		}
		ch := s.changed
		s.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return domain.TerminalResult{}, ctx.Err()
		case <-timer.C:
			wait = 0
		}
	}
}
func (m *Manager) End(owner, id string) (domain.TerminalResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil || s.Owner != owner {
		return domain.TerminalResult{}, fault.NotFound("terminal session unavailable")
	}
	s.stop("closed")
	delete(m.sessions, id)
	return domain.TerminalResult{SessionID: id, State: "closed", RemoteExitConfirmed: false}, nil
}

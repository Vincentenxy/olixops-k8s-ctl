package application

import (
	"sync"
	"time"

	"github.com/google/uuid"
	sessions "olixops-k8s-ctl/internal/application/shell"
	"olixops-k8s-ctl/internal/domain/fault"
)

const transferTicketTTL = 5 * time.Minute
const transferTicketLimit = 4096

type transferTicket struct {
	Owner   string
	Target  sessions.Target
	Path    string
	Expires time.Time
}

// Upload and download each have their own store, with the same ownership,
// expiration and atomic single-consumption rules. No policy check holds this lock.
type ticketStore struct {
	mu      sync.Mutex
	tickets map[string]transferTicket
}

func (s *ticketStore) prepare(owner string, target sessions.Target, path string) (string, transferTicket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.tickets == nil {
		s.tickets = map[string]transferTicket{}
	}
	for id, ticket := range s.tickets {
		if !now.Before(ticket.Expires) {
			delete(s.tickets, id)
		}
	}
	if len(s.tickets) >= transferTicketLimit {
		return "", transferTicket{}, fault.Conflict("too many prepared file transfer references")
	}
	id := uuid.NewString()
	ticket := transferTicket{Owner: owner, Target: target, Path: path, Expires: now.Add(transferTicketTTL)}
	s.tickets[id] = ticket
	return id, ticket, nil
}
func (s *ticketStore) inspect(owner, id string) (transferTicket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ticket, ok := s.tickets[id]
	if !ok || ticket.Owner != owner || !time.Now().Before(ticket.Expires) {
		return transferTicket{}, fault.NotFound("file transfer reference unavailable or expired")
	}
	return ticket, nil
}
func (s *ticketStore) consume(owner, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ticket, ok := s.tickets[id]
	if !ok {
		return fault.Conflict("file transfer reference already consumed")
	}
	if ticket.Owner != owner || !time.Now().Before(ticket.Expires) {
		return fault.NotFound("file transfer reference unavailable or expired")
	}
	delete(s.tickets, id)
	return nil
}

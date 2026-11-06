package application

import (
	"archive/tar"
	"context"
	"io"
	"olixops-k8s-ctl/internal/domain/fault"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	sessions "olixops-k8s-ctl/internal/application/shell"
	"olixops-k8s-ctl/internal/domain"
)

const DownloadRoute = "/api/v1/cluster/pods/files/download"

type downloadTicket struct {
	Owner   string
	Target  sessions.Target
	Path    string
	Expires time.Time
}
type downloads struct {
	sync.Mutex
	tickets map[string]downloadTicket
	baseURL string
}

func WithDownloadBaseURL(base string) ServiceOption {
	return func(s *ClusterService) { s.downloads.baseURL = strings.TrimRight(base, "/") }
}

// DownloadPodFile prepares a user-bound, single-use reference, never file contents.
func (s *ClusterService) DownloadPodFile(ctx context.Context, in domain.DownloadInput) (domain.DownloadOutput, error) {
	owner, err := s.terminalOwner(ctx)
	if err != nil {
		return domain.DownloadOutput{}, err
	}
	if !domain.ValidDiagnosticFilePath(in.Path) {
		return domain.DownloadOutput{}, fault.Invalid("absolute clean file path outside reserved system paths required")
	}
	t, err := s.shellTarget(ctx, in.TerminalTarget, "download_file")
	if err != nil {
		return domain.DownloadOutput{}, err
	}
	if err = s.shellCheck(ctx, owner, t, "file.prepare", "", in.Path); err != nil {
		return domain.DownloadOutput{}, err
	}
	s.downloads.Lock()
	defer s.downloads.Unlock()
	if s.downloads.tickets == nil {
		s.downloads.tickets = map[string]downloadTicket{}
	}
	now := time.Now()
	for id, ticket := range s.downloads.tickets {
		if !now.Before(ticket.Expires) {
			delete(s.downloads.tickets, id)
		}
	}
	if len(s.downloads.tickets) >= 4096 {
		return domain.DownloadOutput{}, fault.Conflict("too many prepared download references")
	}
	id := uuid.NewString()
	expires := now.Add(5 * time.Minute)
	s.downloads.tickets[id] = downloadTicket{Owner: owner, Target: t, Path: in.Path, Expires: expires}
	return domain.DownloadOutput{DownloadID: id, DownloadURL: s.downloads.baseURL + DownloadRoute, Method: "POST", Name: path.Base(in.Path), ExpiresAt: expires.UTC().Format(time.RFC3339), PodUID: t.UID}, nil
}

// FileStream owns the remote exec lifetime. Only a single regular tar entry is exposed.
// No archive entry names are used as local filesystem paths.
type FileStream struct {
	Name    string
	Size    int64
	Reader  io.Reader
	archive *tar.Reader
	pipe    *io.PipeReader
	cancel  context.CancelFunc
	done    <-chan error
}

func (f *FileStream) Close() error { f.cancel(); return f.pipe.Close() }
func (f *FileStream) Finish() error {
	_, err := f.archive.Next()
	if err != io.EOF {
		return fault.Unavailable("incomplete or unexpected file archive")
	}
	if _, err = io.Copy(io.Discard, f.pipe); err != nil {
		return fault.Unavailable("incomplete archive transport")
	}
	if err = <-f.done; err != nil {
		return fault.Unavailable("container transfer failed")
	}
	return nil
}

// OpenPodFile rechecks identity, scope, UID and policy at the actual HTTP download.
func (s *ClusterService) OpenPodFile(ctx context.Context, id string) (*FileStream, error) {
	owner, err := s.terminalOwner(ctx)
	if err != nil {
		return nil, err
	}
	s.downloads.Lock()
	ticket, ok := s.downloads.tickets[id]
	s.downloads.Unlock()
	if !ok || ticket.Owner != owner || !time.Now().Before(ticket.Expires) {
		return nil, fault.NotFound("download reference unavailable or expired")
	}
	t, err := s.shellTarget(ctx, ticket.Target.TerminalTarget, "download_file")
	if err != nil {
		return nil, err
	}
	if t.UID != ticket.Target.UID {
		return nil, fault.Conflict("download Pod replaced")
	}
	if err = s.shellCheck(ctx, owner, t, "file.download", id, ticket.Path); err != nil {
		return nil, err
	}
	s.downloads.Lock()
	_, ok = s.downloads.tickets[id]
	if ok {
		delete(s.downloads.tickets, id)
	}
	s.downloads.Unlock()
	if !ok {
		return nil, fault.Conflict("download reference already consumed")
	}
	ctx, cancel := context.WithCancel(ctx)
	r, w := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := s.streamBackend.Stream(ctx, t.Namespace, t.Pod, t.Container, []string{"tar", "-chf", "-", "-C", path.Dir(ticket.Path), "./" + path.Base(ticket.Path)}, false, nil, w, io.Discard)
		if err != nil {
			w.CloseWithError(fault.Unavailable("container transfer failed"))
		} else {
			w.Close()
		}
		done <- err
	}()
	archive := tar.NewReader(r)
	header, err := archive.Next()
	if err != nil || header.Size < 0 || (header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA) {
		cancel()
		r.Close()
		return nil, fault.Unavailable("download requires one readable regular file and tar in container")
	}
	return &FileStream{Name: path.Base(ticket.Path), Size: header.Size, Reader: archive, archive: archive, pipe: r, cancel: cancel, done: done}, nil
}

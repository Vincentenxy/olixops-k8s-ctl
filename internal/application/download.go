package application

import (
	"archive/tar"
	"context"
	"io"
	"olixops-k8s-ctl/internal/domain/fault"
	"path"
	"strings"
	"time"

	"olixops-k8s-ctl/internal/domain"
)

const DownloadRoute = "/api/v1/cluster/pods/files/download"

func WithDownloadBaseURL(base string) ServiceOption {
	return func(s *ClusterService) { s.transferBaseURL = strings.TrimRight(base, "/") }
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
	id, ticket, err := s.downloads.prepare(owner, t, in.Path)
	if err != nil {
		return domain.DownloadOutput{}, err
	}
	return domain.DownloadOutput{DownloadID: id, DownloadURL: s.transferBaseURL + DownloadRoute, Method: "POST", Name: path.Base(in.Path), ExpiresAt: ticket.Expires.UTC().Format(time.RFC3339), PodUID: t.UID}, nil
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
	ticket, err := s.downloads.inspect(owner, id)
	if err != nil {
		return nil, err
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
	if err = s.downloads.consume(owner, id); err != nil {
		return nil, err
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

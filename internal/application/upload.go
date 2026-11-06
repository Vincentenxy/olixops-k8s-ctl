package application

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"olixops-k8s-ctl/internal/domain/fault"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"olixops-k8s-ctl/internal/domain"
)

const UploadRoute = "/api/v1/cluster/pods/files/upload"

type uploads struct {
	sync.Mutex
	tickets map[string]downloadTicket
}

// UploadPodFile prepares metadata only. Raw bytes arrive on the authenticated HTTP route.
func (s *ClusterService) UploadPodFile(ctx context.Context, in domain.UploadInput) (domain.UploadOutput, error) {
	owner, err := s.terminalOwner(ctx)
	if err != nil {
		return domain.UploadOutput{}, err
	}
	if !domain.ValidDiagnosticFilePath(in.Path) {
		return domain.UploadOutput{}, fault.Invalid("absolute clean file path outside reserved system paths required")
	}
	t, err := s.shellTarget(ctx, in.TerminalTarget, "upload_file")
	if err != nil {
		return domain.UploadOutput{}, err
	}
	if err = s.shellCheck(ctx, owner, t, "file.upload.prepare", "", in.Path); err != nil {
		return domain.UploadOutput{}, err
	}
	s.uploads.Lock()
	defer s.uploads.Unlock()
	if s.uploads.tickets == nil {
		s.uploads.tickets = map[string]downloadTicket{}
	}
	now := time.Now()
	for id, ticket := range s.uploads.tickets {
		if !now.Before(ticket.Expires) {
			delete(s.uploads.tickets, id)
		}
	}
	if len(s.uploads.tickets) >= 4096 {
		return domain.UploadOutput{}, fault.Conflict("too many prepared upload references")
	}
	id := uuid.NewString()
	expires := now.Add(5 * time.Minute)
	s.uploads.tickets[id] = downloadTicket{Owner: owner, Target: t, Path: in.Path, Expires: expires}
	return domain.UploadOutput{UploadID: id, UploadURL: s.downloads.baseURL + UploadRoute, Method: "POST", Path: in.Path, ExpiresAt: expires.UTC().Format(time.RFC3339), PodUID: t.UID}, nil
}

// Fixed scripts run inside the target container; caller paths are separate argv.
// A new same-directory staging folder keeps the final hard-link publication atomic.
const uploadStage = `umask 077; mkdir "$1" && : > "$1/$2"`
const uploadReceive = `head -c "$3" > "$1/$2" && test "$(wc -c < "$1/$2")" -eq "$3" && sha256sum < "$1/$2"`
const uploadCommit = `ln "$1/$3" "$2/"`
const uploadCleanup = `rm -f "$1/$2" && rmdir "$1"`

// ReceivePodFile owns body and closes it to unblock a producer after stream failure.
// No whole-file buffering, local subprocess, or caller-supplied shell command is used.
func (s *ClusterService) ReceivePodFile(ctx context.Context, id string, body io.ReadCloser, size int64) (domain.UploadResult, error) {
	defer body.Close()
	if size < 0 {
		return domain.UploadResult{}, fault.Invalid("upload requires Content-Length")
	}
	owner, err := s.terminalOwner(ctx)
	if err != nil {
		return domain.UploadResult{}, err
	}
	s.uploads.Lock()
	ticket, ok := s.uploads.tickets[id]
	s.uploads.Unlock()
	if !ok || ticket.Owner != owner || !time.Now().Before(ticket.Expires) {
		return domain.UploadResult{}, fault.NotFound("upload reference unavailable or expired")
	}
	t, err := s.shellTarget(ctx, ticket.Target.TerminalTarget, "upload_file")
	if err != nil {
		return domain.UploadResult{}, err
	}
	if t.UID != ticket.Target.UID {
		return domain.UploadResult{}, fault.Conflict("upload Pod replaced")
	}
	if err = s.shellCheck(ctx, owner, t, "file.upload", id, ticket.Path); err != nil {
		return domain.UploadResult{}, err
	}
	s.uploads.Lock()
	_, ok = s.uploads.tickets[id]
	if ok {
		delete(s.uploads.tickets, id)
	}
	s.uploads.Unlock()
	if !ok {
		return domain.UploadResult{}, fault.Conflict("upload reference already consumed")
	}
	stage := path.Join(path.Dir(ticket.Path), ".k8s-ctl-upload-"+uuid.NewString())
	// Cleanup is best effort: an unavailable/replaced Pod or revoked grant must not be bypassed.
	defer func() {
		cleanCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		current, e := s.shellTarget(cleanCtx, ticket.Target.TerminalTarget, "upload_file")
		if e != nil || current.UID != t.UID {
			return
		}
		if e = s.shellCheck(cleanCtx, owner, current, "file.upload.cleanup", id, ticket.Path); e != nil {
			return
		}
		_ = s.streamBackend.Stream(cleanCtx, t.Namespace, t.Pod, t.Container, []string{"/bin/sh", "-c", uploadCleanup, "k8s-ctl-upload", stage, path.Base(ticket.Path)}, false, nil, io.Discard, io.Discard)
	}()
	// Validate/create the writable destination before opening stdin. In some runtimes
	// an exec which exits without reading a large stdin can block stream teardown.
	err = s.streamBackend.Stream(ctx, t.Namespace, t.Pod, t.Container, []string{"/bin/sh", "-c", uploadStage, "k8s-ctl-upload", stage, path.Base(ticket.Path)}, false, nil, io.Discard, io.Discard)
	if err != nil {
		return domain.UploadResult{}, fault.Unavailable("cannot prepare writable upload staging directory")
	}
	type copied struct {
		n      int64
		err    error
		digest string
	}
	done := make(chan copied, 1)
	r, w := io.Pipe()
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		hash := sha256.New()
		n, e := io.Copy(io.MultiWriter(w, hash), body)
		if e == nil && n != size {
			e = fault.Invalid("upload length mismatch")
		}
		if e != nil {
			cancel()
			_ = w.CloseWithError(e)
		}
		// Keep stdin open until the length-aware receiver acknowledges the file.
		// Closing stdin immediately after sending can race buffered SPDY frames.
		done <- copied{n: n, err: e, digest: fmt.Sprintf("%x", hash.Sum(nil))}
	}()
	var remoteDigest uploadDigest
	err = s.streamBackend.Stream(streamCtx, t.Namespace, t.Pod, t.Container, []string{"/bin/sh", "-c", uploadReceive, "k8s-ctl-upload", stage, path.Base(ticket.Path), strconv.FormatInt(size, 10)}, false, r, &remoteDigest, io.Discard)
	_ = r.Close()
	_ = w.Close()
	_ = body.Close()
	result := <-done
	if err != nil || result.err != nil || ctx.Err() != nil {
		return domain.UploadResult{}, fault.Unavailable("upload transfer failed; final file was not published")
	}
	fields := strings.Fields(string(remoteDigest.data[:remoteDigest.n]))
	if len(fields) != 2 || fields[0] != result.digest || fields[1] != "-" {
		return domain.UploadResult{}, fault.Unavailable("remote upload checksum mismatch; final file was not published")
	}
	// Recheck policy and Pod before publication; never retry an uncertain commit.
	current, err := s.shellTarget(ctx, ticket.Target.TerminalTarget, "upload_file")
	if err != nil || current.UID != t.UID {
		return domain.UploadResult{}, fault.Conflict("upload target changed before publication")
	}
	if err = s.shellCheck(ctx, owner, current, "file.upload.commit", id, ticket.Path); err != nil {
		return domain.UploadResult{}, err
	}
	err = s.streamBackend.Stream(ctx, t.Namespace, t.Pod, t.Container, []string{"/bin/sh", "-c", uploadCommit, "k8s-ctl-upload", stage, path.Dir(ticket.Path), path.Base(ticket.Path)}, false, nil, io.Discard, io.Discard)
	if err != nil {
		return domain.UploadResult{}, fault.Unknown("upload publication not confirmed; check existing destination, filesystem and network before retrying")
	}
	return domain.UploadResult{Path: ticket.Path, Size: result.n, SHA256: result.digest, PodUID: t.UID}, nil
}

// Only a SHA-256 line is needed; never buffer unbounded container stdout.
type uploadDigest struct {
	data [128]byte
	n    int
}

func (d *uploadDigest) Write(p []byte) (int, error) {
	d.n += copy(d.data[d.n:], p)
	return len(p), nil
}

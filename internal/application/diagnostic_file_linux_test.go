//go:build linux

package application

import (
	"context"
	"github.com/stretchr/testify/require"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDiagnosticReaderRejectsLinksAndSpecialFiles(t *testing.T) {
	perl, err := exec.LookPath("perl")
	if err != nil {
		t.Skip("Perl/Fcntl unavailable")
	}
	dir := t.TempDir()
	allowed := filepath.Join(dir, "config")
	require.NoError(t, os.WriteFile(allowed, []byte("fixture-config"), 0600))
	read := func(path string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		data, err := exec.CommandContext(ctx, perl, "-e", diagnosticFileReader, "--", path).Output()
		return string(data), err
	}
	value, err := read(allowed)
	require.NoError(t, err)
	require.Equal(t, "fixture-config", value)
	link := filepath.Join(dir, "link")
	require.NoError(t, os.Symlink(allowed, link))
	_, err = read(link)
	require.Error(t, err)
	parentLink := filepath.Join(dir, "parent")
	require.NoError(t, os.Symlink(dir, parentLink))
	_, err = read(filepath.Join(parentLink, "config"))
	require.Error(t, err)
	_, err = read(dir)
	require.Error(t, err)
	fifo := filepath.Join(dir, "fifo")
	require.NoError(t, syscall.Mkfifo(fifo, 0600))
	_, err = read(fifo)
	require.Error(t, err)
	require.NoError(t, os.WriteFile(allowed, []byte(strings.Repeat("x", 100000)), 0600))
	value, err = read(allowed)
	require.NoError(t, err)
	require.Len(t, value, 65537)
}

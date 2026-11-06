package kubernetes

import (
	"context"
	"fmt"
	"io"
	"k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// Stream delegates only to the selected Provider-backed API server, never a caller URL.
// v0.28 client-go supports SPDY; this path needs pods/exec create permission.
func (c *Client) Stream(ctx context.Context, ns, pod, container string, argv []string, tty bool, stdin io.Reader, stdout, stderr io.Writer) error {
	if c.restConfig == nil || c.core.CoreV1().RESTClient() == nil {
		return fmt.Errorf("stream backend unavailable")
	}
	req := c.core.CoreV1().RESTClient().Post().Resource("pods").Namespace(ns).Name(pod).SubResource("exec")
	req.VersionedParams(&v1.PodExecOptions{Container: container, Command: argv, Stdin: stdin != nil, Stdout: true, Stderr: !tty, TTY: tty}, scheme.ParameterCodec)
	cfg := rest.CopyConfig(c.restConfig)
	cfg.Timeout = 0 // stream lifetime is controlled by the session context
	executor, err := remotecommand.NewSPDYExecutor(cfg, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("create container stream failed")
	}
	if tty {
		stderr = nil
	}
	return executor.StreamWithContext(ctx, remotecommand.StreamOptions{Stdin: stdin, Stdout: stdout, Stderr: stderr, Tty: tty})
}

package kubernetes

import (
	"context"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"olixops-k8s-ctl/internal/domain/fault"
	"slices"
)

// CheckPodExecTarget observes an opt-in, unprivileged target; exec has no UID precondition.
func (c *Client) CheckPodExecTarget(ctx context.Context, namespace, pod, container string, approvedUIDs ...string) (string, error) {
	p, err := c.core.CoreV1().Pods(namespace).Get(ctx, pod, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	if (p.Labels["k8s-ctl/exec-enabled"] != "true" && !slices.Contains(approvedUIDs, string(p.UID))) || p.Labels["app"] == "olixops-k8s-ctl" || p.DeletionTimestamp != nil || p.Spec.HostNetwork || p.Spec.HostPID || p.Spec.HostIPC {
		return "", fault.Forbidden("Pod not eligible for exec")
	}
	for _, v := range p.Spec.Volumes {
		if v.HostPath != nil {
			return "", fault.Forbidden("hostPath Pod not eligible for exec")
		}
	}
	found := false
	allContainers := append(append([]corev1.Container{}, p.Spec.Containers...), p.Spec.InitContainers...)
	for _, ec := range p.Spec.EphemeralContainers {
		allContainers = append(allContainers, corev1.Container{SecurityContext: ec.SecurityContext})
	}
	for _, c := range allContainers {
		if c.SecurityContext != nil {
			sc := c.SecurityContext
			if sc.Privileged != nil && *sc.Privileged {
				return "", fault.Forbidden("privileged Pod not eligible for exec")
			}
		}
		if c.Name == container {
			found = true
		}
	}
	if !found {
		return "", fault.NotFound("container not found")
	}
	for _, status := range p.Status.ContainerStatuses {
		if status.Name == container && status.State.Running != nil {
			return string(p.UID), nil
		}
	}
	return "", fault.Conflict("target container is not running")
}

// CheckDiagnosticPodUID rejects a grant left over from a replaced Pod.
func (c *Client) CheckDiagnosticPodUID(ctx context.Context, namespace, pod, uid string) error {
	p, err := c.core.CoreV1().Pods(namespace).Get(ctx, pod, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if string(p.UID) != uid {
		return fault.Conflict("diagnostic Pod UID changed")
	}
	return nil
}

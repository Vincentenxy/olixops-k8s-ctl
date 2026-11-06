package kubernetes

import (
	"context"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	"testing"
)

func TestExecTargetEligibility(t *testing.T) {
	yes := true
	for name, change := range map[string]func(*corev1.Pod){
		"eligible":       func(*corev1.Pod) {},
		"missing opt in": func(p *corev1.Pod) { p.Labels = nil },
		"control plane":  func(p *corev1.Pod) { p.Labels["app"] = "olixops-k8s-ctl" },
		"host network":   func(p *corev1.Pod) { p.Spec.HostNetwork = true },
		"host pid":       func(p *corev1.Pod) { p.Spec.HostPID = true },
		"host ipc":       func(p *corev1.Pod) { p.Spec.HostIPC = true },
		"host path": func(p *corev1.Pod) {
			p.Spec.Volumes = []corev1.Volume{{Name: "host", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/"}}}}
		},
		"privileged": func(p *corev1.Pod) { p.Spec.Containers[0].SecurityContext = &corev1.SecurityContext{Privileged: &yes} },
		"privileged init": func(p *corev1.Pod) {
			p.Spec.InitContainers = []corev1.Container{{Name: "init", SecurityContext: &corev1.SecurityContext{Privileged: &yes}}}
		},
		"stopped":     func(p *corev1.Pod) { p.Status.ContainerStatuses = nil },
		"terminating": func(p *corev1.Pod) { now := metav1.Now(); p.DeletionTimestamp = &now },
	} {
		t.Run(name, func(t *testing.T) {
			p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "demo", UID: types.UID("uid-123"), Labels: map[string]string{"k8s-ctl/exec-enabled": "true"}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "main"}}}, Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "main", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}}}
			change(p)
			c := &Client{core: fake.NewSimpleClientset(p)}
			uid, err := c.CheckPodExecTarget(context.Background(), "demo", "demo", "main")
			if name == "eligible" {
				require.NoError(t, err)
				require.Equal(t, "uid-123", uid)
				_, err = c.CheckPodExecTarget(context.Background(), "demo", "demo", "missing")
				require.Error(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestExplicitDiagnosticUIDOptIn(t *testing.T) {
	p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "demo", UID: types.UID("uid-1")}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}}, Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "app", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}}}
	c := &Client{core: fake.NewSimpleClientset(p)}
	_, err := c.CheckPodExecTarget(context.Background(), "demo", "app", "app", "uid-other")
	require.Error(t, err)
	uid, err := c.CheckPodExecTarget(context.Background(), "demo", "app", "app", "uid-1")
	require.NoError(t, err)
	require.Equal(t, "uid-1", uid)
	require.Error(t, c.CheckDiagnosticPodUID(context.Background(), "demo", "app", "uid-other"))
	require.NoError(t, c.CheckDiagnosticPodUID(context.Background(), "demo", "app", "uid-1"))
}

package kubernetes

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

// RolloutStatus is a point-in-time snapshot, not a watch or wait operation.
type RolloutStatus struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Desired   int32  `json:"desired"`
	Ready     int32  `json:"ready"`
	Updated   int32  `json:"updated"`
	Observed  bool   `json:"observed"`
	Complete  bool   `json:"complete"`
	Failed    bool   `json:"failed"`
}

func (c *Client) GetRolloutStatus(ctx context.Context, namespace, kind, name string) (RolloutStatus, error) {
	result := RolloutStatus{Kind: kind, Namespace: namespace, Name: name}
	if problems := validation.IsDNS1123Subdomain(name); len(problems) > 0 {
		return result, fmt.Errorf("invalid workload name")
	}
	switch kind {
	case "Deployment":
		item, err := c.core.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return result, err
		}
		result.Desired = 1
		if item.Spec.Replicas != nil {
			result.Desired = *item.Spec.Replicas
		}
		result.Ready, result.Updated = item.Status.ReadyReplicas, item.Status.UpdatedReplicas
		result.Observed = item.Status.ObservedGeneration >= item.Generation
		for _, condition := range item.Status.Conditions {
			if condition.Type == "Progressing" && condition.Status == "False" && condition.Reason == "ProgressDeadlineExceeded" {
				result.Failed = true
			}
		}
		result.Complete = result.Observed && !result.Failed && result.Updated == result.Desired && item.Status.Replicas == result.Desired && item.Status.AvailableReplicas == result.Desired
	case "StatefulSet":
		item, err := c.core.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return result, err
		}
		result.Desired = 1
		if item.Spec.Replicas != nil {
			result.Desired = *item.Spec.Replicas
		}
		result.Ready, result.Updated = item.Status.ReadyReplicas, item.Status.UpdatedReplicas
		result.Observed = item.Status.ObservedGeneration >= item.Generation
		// For partitioned or OnDelete updates report conservatively until all replicas are updated.
		result.Complete = result.Observed && item.Status.Replicas == result.Desired && result.Ready == result.Desired && result.Updated == result.Desired && item.Status.CurrentRevision == item.Status.UpdateRevision
	case "DaemonSet":
		item, err := c.core.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return result, err
		}
		result.Desired = item.Status.DesiredNumberScheduled
		result.Ready = item.Status.NumberReady
		result.Updated = item.Status.UpdatedNumberScheduled
		result.Observed = item.Status.ObservedGeneration >= item.Generation
		result.Complete = result.Observed && result.Updated == result.Desired && item.Status.NumberAvailable == result.Desired && item.Status.NumberMisscheduled == 0
	default:
		return result, fmt.Errorf("kind must be Deployment, StatefulSet or DaemonSet")
	}
	return result, nil
}

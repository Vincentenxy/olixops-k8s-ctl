package kubernetes

import (
	"context"
	"fmt"
	"strconv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type WorkloadRevision struct {
	Revision  int64  `json:"revision"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
}
type WorkloadHistory struct {
	Items    []WorkloadRevision `json:"items"`
	Continue string             `json:"continue,omitempty"`
}

func (c *Client) GetWorkloadHistory(ctx context.Context, namespace, kind, name, token string, limit int64) (WorkloadHistory, error) {
	result := WorkloadHistory{Items: []WorkloadRevision{}}
	var uid types.UID
	var selector *metav1.LabelSelector
	switch kind {
	case "Deployment":
		item, err := c.core.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return result, err
		}
		uid, selector = item.UID, item.Spec.Selector
	case "StatefulSet":
		item, err := c.core.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return result, err
		}
		uid, selector = item.UID, item.Spec.Selector
	case "DaemonSet":
		item, err := c.core.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return result, err
		}
		uid, selector = item.UID, item.Spec.Selector
	default:
		return result, fmt.Errorf("unsupported workload kind")
	}
	labelSelector, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return result, err
	}
	options := metav1.ListOptions{LabelSelector: labelSelector.String(), Limit: limit, Continue: token}
	owned := func(references []metav1.OwnerReference) bool {
		for _, ref := range references {
			if ref.UID == uid && ref.Controller != nil && *ref.Controller {
				return true
			}
		}
		return false
	}
	if kind == "Deployment" {
		list, err := c.core.AppsV1().ReplicaSets(namespace).List(ctx, options)
		if err != nil {
			return result, err
		}
		result.Continue = list.Continue
		for _, item := range list.Items {
			if !owned(item.OwnerReferences) {
				continue
			}
			revision, err := strconv.ParseInt(item.Annotations["deployment.kubernetes.io/revision"], 10, 64)
			if err != nil {
				continue
			}
			result.Items = append(result.Items, WorkloadRevision{Revision: revision, Name: item.Name, CreatedAt: item.CreationTimestamp.UTC().Format(time.RFC3339)})
		}
	} else {
		list, err := c.core.AppsV1().ControllerRevisions(namespace).List(ctx, options)
		if err != nil {
			return result, err
		}
		result.Continue = list.Continue
		for _, item := range list.Items {
			if owned(item.OwnerReferences) {
				result.Items = append(result.Items, WorkloadRevision{Revision: item.Revision, Name: item.Name, CreatedAt: item.CreationTimestamp.UTC().Format(time.RFC3339)})
			}
		}
	}
	return result, nil
}

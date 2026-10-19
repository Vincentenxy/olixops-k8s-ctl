package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"

	"olixops-k8s-ctl/internal/domain"
)

type ResourceDetail struct {
	APIVersion      string         `json:"apiVersion"`
	Kind            string         `json:"kind"`
	Namespace       string         `json:"namespace"`
	Name            string         `json:"name"`
	UID             string         `json:"uid"`
	ResourceVersion string         `json:"resourceVersion"`
	Generation      int64          `json:"generation"`
	Spec            map[string]any `json:"spec,omitempty"`
	Status          map[string]any `json:"status,omitempty"`
	DataKeys        []string       `json:"dataKeys,omitempty"`
}
type ResourcePage struct {
	Items    []ResourceDetail `json:"items"`
	Continue string           `json:"continue,omitempty"`
}
type ResourceYAML struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	YAML      string `json:"yaml"`
	Redacted  bool   `json:"redacted"`
}
type MutationResult struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Accepted  bool   `json:"accepted"`
}

func (c *Client) resource(ctx context.Context, kind, namespace string) (dynamic.ResourceInterface, error) {
	resource, ok := domain.LookupResource(kind)
	if !ok {
		return nil, fmt.Errorf("unsupported resource kind")
	}
	// Select a served version without exposing arbitrary GVRs to callers.
	for _, version := range resource.Versions {
		gvr := schema.GroupVersionResource{Group: resource.Group, Version: version, Resource: resource.Resource}
		apiVersion := version
		if resource.Group != "" {
			apiVersion = resource.Group + "/" + version
		}
		served, err := c.discovery.ServerResourcesForGroupVersion(apiVersion)
		if err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return nil, err
		}
		for _, item := range served.APIResources {
			if item.Name == resource.Resource && item.Namespaced {
				return c.dynamic.Resource(gvr).Namespace(namespace), nil
			}
		}
	}
	return nil, fmt.Errorf("resource kind is not served")
}

func (c *Client) GetResource(ctx context.Context, namespace, kind, name string) (ResourceDetail, error) {
	resource, err := c.resource(ctx, kind, namespace)
	if err != nil {
		return ResourceDetail{}, err
	}
	item, err := resource.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return ResourceDetail{}, err
	}
	return resourceDetail(item), nil
}

func (c *Client) GetResourceYAML(ctx context.Context, namespace, kind, name string) (ResourceYAML, error) {
	var object map[string]any
	if kind == "Namespace" {
		item, err := c.core.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return ResourceYAML{}, err
		}
		object, err = runtime.DefaultUnstructuredConverter.ToUnstructured(item)
		if err != nil {
			return ResourceYAML{}, err
		}
		// TypeMeta is commonly omitted from typed client responses, but it is
		// required for a useful standalone YAML manifest.
		object["apiVersion"] = "v1"
		object["kind"] = "Namespace"
	} else {
		resource, err := c.resource(ctx, kind, namespace)
		if err != nil {
			return ResourceYAML{}, err
		}
		item, err := resource.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return ResourceYAML{}, err
		}
		object = item.Object
	}

	// Return a readable manifest while applying the same data minimization used by
	// structured resource reads. Secret resources are rejected by the application
	// layer and are never serialized by this generic endpoint.
	scrub(object)
	data, err := yaml.Marshal(object)
	if err != nil {
		return ResourceYAML{}, err
	}
	item := unstructured.Unstructured{Object: object}
	return ResourceYAML{Kind: item.GetKind(), Namespace: item.GetNamespace(), Name: item.GetName(), YAML: string(data), Redacted: true}, nil
}

func (c *Client) ListResourceObjects(ctx context.Context, namespace, kind, selector, token string, limit int64) (ResourcePage, error) {
	if _, err := labels.Parse(selector); err != nil {
		return ResourcePage{}, fmt.Errorf("invalid label selector")
	}
	resource, err := c.resource(ctx, kind, namespace)
	if err != nil {
		return ResourcePage{}, err
	}
	list, err := resource.List(ctx, metav1.ListOptions{LabelSelector: selector, Limit: limit, Continue: token})
	if err != nil {
		return ResourcePage{}, err
	}
	result := ResourcePage{Items: make([]ResourceDetail, 0, len(list.Items)), Continue: list.GetContinue()}
	for i := range list.Items {
		result.Items = append(result.Items, resourceDetail(&list.Items[i]))
	}
	return result, nil
}

func resourceDetail(item *unstructured.Unstructured) ResourceDetail {
	spec, _, _ := unstructured.NestedMap(item.Object, "spec")
	status, _, _ := unstructured.NestedMap(item.Object, "status")
	scrub(spec)
	scrub(status)
	result := ResourceDetail{APIVersion: item.GetAPIVersion(), Kind: item.GetKind(), Namespace: item.GetNamespace(), Name: item.GetName(), UID: string(item.GetUID()), ResourceVersion: item.GetResourceVersion(), Generation: item.GetGeneration(), Spec: spec, Status: status}
	if item.GetKind() == "ConfigMap" {
		for _, field := range []string{"data", "binaryData"} {
			data, _, _ := unstructured.NestedMap(item.Object, field)
			for key := range data {
				result.DataKeys = append(result.DataKeys, key)
			}
		}
	}
	sort.Strings(result.DataKeys)
	return result
}

// Drop metadata annotations and literal env/command values, including nested Pod templates.
// This is data minimization, not a guarantee that arbitrary workload status text is secret-free.
func scrub(value any) {
	switch node := value.(type) {
	case map[string]any:
		for key, child := range node {
			switch key {
			case "annotations", "managedFields", "data", "binaryData", "stringData":
				delete(node, key)
			case "command", "args", "value":
				node[key] = "<redacted>"
			default:
				scrub(child)
			}
		}
	case []any:
		for _, child := range node {
			scrub(child)
		}
	}
}

func (c *Client) DeleteResource(ctx context.Context, namespace, kind, name string) (MutationResult, error) {
	result := MutationResult{Kind: kind, Namespace: namespace, Name: name}
	resource, err := c.resource(ctx, kind, namespace)
	if err != nil {
		return result, err
	}
	// Bind the deletion to the instance just read to avoid deleting a replacement object.
	current, err := resource.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return result, err
	}
	uid := current.GetUID()
	propagation := metav1.DeletePropagationBackground
	err = resource.Delete(ctx, name, metav1.DeleteOptions{PropagationPolicy: &propagation, Preconditions: &metav1.Preconditions{UID: &uid}})
	result.Accepted = err == nil
	return result, err
}

func (c *Client) ScaleWorkload(ctx context.Context, namespace, kind, name string, replicas int32) (MutationResult, error) {
	result := MutationResult{Kind: kind, Namespace: namespace, Name: name}
	var err error
	switch kind {
	case "Deployment":
		scale, e := c.core.AppsV1().Deployments(namespace).GetScale(ctx, name, metav1.GetOptions{})
		if e != nil {
			return result, e
		}
		scale.Spec.Replicas = replicas
		_, err = c.core.AppsV1().Deployments(namespace).UpdateScale(ctx, name, scale, metav1.UpdateOptions{})
	case "StatefulSet":
		scale, e := c.core.AppsV1().StatefulSets(namespace).GetScale(ctx, name, metav1.GetOptions{})
		if e != nil {
			return result, e
		}
		scale.Spec.Replicas = replicas
		_, err = c.core.AppsV1().StatefulSets(namespace).UpdateScale(ctx, name, scale, metav1.UpdateOptions{})
	default:
		return result, fmt.Errorf("only Deployment and StatefulSet support scaling")
	}
	result.Accepted = err == nil
	return result, err
}

func (c *Client) RestartWorkload(ctx context.Context, namespace, kind, name string) (MutationResult, error) {
	result := MutationResult{Kind: kind, Namespace: namespace, Name: name}
	resource, err := c.resource(ctx, kind, namespace)
	if err != nil {
		return result, err
	}
	// A fresh timestamp requests a rollout. Repeating this operation can restart it again.
	patch, _ := json.Marshal(map[string]any{"spec": map[string]any{"template": map[string]any{"metadata": map[string]any{"annotations": map[string]string{"kubectl.kubernetes.io/restartedAt": time.Now().UTC().Format(time.RFC3339Nano)}}}}})
	_, err = resource.Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	result.Accepted = err == nil
	return result, err
}

func (c *Client) UpdateConfigMap(ctx context.Context, namespace, name string, data map[string]string) (MutationResult, error) {
	result := MutationResult{Kind: "ConfigMap", Namespace: namespace, Name: name}
	patch, err := json.Marshal(map[string]any{"data": data})
	if err != nil {
		return result, err
	}
	_, err = c.core.CoreV1().ConfigMaps(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	result.Accepted = err == nil
	return result, err
}

func (c *Client) CreateNamespace(ctx context.Context, name string) (MutationResult, error) {
	result := MutationResult{Kind: "Namespace", Name: name}
	_, err := c.core.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}, metav1.CreateOptions{})
	result.Accepted = err == nil
	return result, err
}
func (c *Client) DeleteNamespace(ctx context.Context, name string) (MutationResult, error) {
	result := MutationResult{Kind: "Namespace", Name: name}
	current, err := c.core.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return result, err
	}
	err = c.core.CoreV1().Namespaces().Delete(ctx, name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &current.UID}})
	result.Accepted = err == nil
	return result, err
}

func (c *Client) UpdateNamespaceMetadata(ctx context.Context, name string, labels, annotations map[string]string, removeLabels, removeAnnotations []string) (MutationResult, error) {
	result := MutationResult{Kind: "Namespace", Name: name}
	patchMetadata := make(map[string]any)
	if len(labels) > 0 || len(removeLabels) > 0 {
		patchLabels := make(map[string]any, len(labels)+len(removeLabels))
		for key, value := range labels {
			patchLabels[key] = value
		}
		for _, key := range removeLabels {
			patchLabels[key] = nil
		}
		patchMetadata["labels"] = patchLabels
	}
	if len(annotations) > 0 || len(removeAnnotations) > 0 {
		patchAnnotations := make(map[string]any, len(annotations)+len(removeAnnotations))
		for key, value := range annotations {
			patchAnnotations[key] = value
		}
		for _, key := range removeAnnotations {
			patchAnnotations[key] = nil
		}
		patchMetadata["annotations"] = patchAnnotations
	}
	patch, err := json.Marshal(map[string]any{"metadata": patchMetadata})
	if err != nil {
		return result, err
	}
	_, err = c.core.CoreV1().Namespaces().Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	result.Accepted = err == nil
	return result, err
}

type LogOptions struct {
	Namespace    string `json:"namespace"`
	Pod          string `json:"pod"`
	Container    string `json:"container,omitempty"`
	TailLines    int64  `json:"tailLines,omitempty"`
	SinceSeconds int64  `json:"sinceSeconds,omitempty"`
	Previous     bool   `json:"previous,omitempty"`
	Timestamps   bool   `json:"timestamps,omitempty"`
}
type PodLogs struct {
	Namespace string `json:"namespace"`
	Pod       string `json:"pod"`
	Container string `json:"container,omitempty"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}

func (c *Client) GetPodLogs(ctx context.Context, options LogOptions) (PodLogs, error) {
	const maxBytes int64 = 256 << 10
	limit := maxBytes + 1
	logs := &corev1.PodLogOptions{Container: options.Container, TailLines: &options.TailLines, LimitBytes: &limit, Previous: options.Previous, Timestamps: options.Timestamps}
	if options.SinceSeconds > 0 {
		logs.SinceSeconds = &options.SinceSeconds
	}
	stream, err := c.core.CoreV1().Pods(options.Namespace).GetLogs(options.Pod, logs).Stream(ctx)
	if err != nil {
		return PodLogs{}, err
	}
	defer stream.Close()
	data, err := io.ReadAll(io.LimitReader(stream, maxBytes+1))
	if err != nil {
		return PodLogs{}, err
	}
	truncated := int64(len(data)) > maxBytes
	if truncated {
		data = data[:maxBytes]
	}
	return PodLogs{Namespace: options.Namespace, Pod: options.Pod, Container: options.Container, Text: strings.ToValidUTF8(string(data), "�"), Truncated: truncated}, nil
}

type EventSummary struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	Reason        string `json:"reason"`
	Message       string `json:"message"`
	Kind          string `json:"kind"`
	ResourceName  string `json:"resourceName"`
	Count         int32  `json:"count"`
	LastTimestamp string `json:"lastTimestamp"`
}
type EventPage struct {
	Items    []EventSummary `json:"items"`
	Continue string         `json:"continue,omitempty"`
}

func (c *Client) ListEvents(ctx context.Context, namespace, kind, name, token string, limit int64) (EventPage, error) {
	selector := fields.Set{}
	if kind != "" {
		selector["involvedObject.kind"] = kind
	}
	if name != "" {
		selector["involvedObject.name"] = name
	}
	list, err := c.core.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{FieldSelector: selector.AsSelector().String(), Limit: limit, Continue: token})
	if err != nil {
		return EventPage{}, err
	}
	result := EventPage{Items: make([]EventSummary, 0, len(list.Items)), Continue: list.Continue}
	for _, item := range list.Items {
		timestamp := item.LastTimestamp.Time
		if timestamp.IsZero() {
			timestamp = item.EventTime.Time
		}
		result.Items = append(result.Items, EventSummary{Name: item.Name, Type: item.Type, Reason: item.Reason, Message: item.Message, Kind: item.InvolvedObject.Kind, ResourceName: item.InvolvedObject.Name, Count: item.Count, LastTimestamp: timestamp.UTC().Format(time.RFC3339)})
	}
	return result, nil
}

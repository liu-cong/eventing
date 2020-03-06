package resources

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"knative.dev/pkg/system"
)

// MakeHierarchyConfiguration creates a default HierarchyConfiguration object for Namespace 'namespace'.
func MakeHierarchyConfiguration(ns *corev1.Namespace, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "hnc.x-k8s.io/v1alpha1",
			"kind":       "HierarchyConfiguration",
			"metadata": map[string]interface{}{
				"name":      "hierarchy",
				"namespace": ns.Name,
			},
			"spec": map[string]interface{}{
				"parent": system.Namespace(),
			},
		},
	}
}

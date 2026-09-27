package tests

import (
	"testing"

	"github.com/stretchr/testify/require"

	"olixops-k8s-ctl/internal/application"
)

func TestValidateManifest(t *testing.T) {
	resources, err := application.ValidateManifest(`apiVersion: v1
kind: ConfigMap
metadata:
  name: example
`, "default", nil)
	require.NoError(t, err)
	require.Len(t, resources, 1)
	require.Equal(t, "default", resources[0].Namespace)

	_, err = application.ValidateManifest(`apiVersion: v1
kind: Secret
metadata:
  name: forbidden
`, "default", nil)
	require.ErrorContains(t, err, "Secret manifests are not accepted")
}

func TestValidateManifestRejectsNamespaceMismatch(t *testing.T) {
	_, err := application.ValidateManifest(`apiVersion: v1
kind: Service
metadata:
  name: example
  namespace: other
`, "default", map[string]struct{}{"default": {}})
	require.ErrorContains(t, err, `expected "default"`)
}

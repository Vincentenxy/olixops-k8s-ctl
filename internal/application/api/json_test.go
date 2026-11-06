package api

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestDecodePresenceAndClosedObjects(t *testing.T) {
	good := `{"namespace":"demo","kind":"Deployment","name":"app","replicas":0}`
	in, e := Decode[ScaleInput](strings.NewReader(good))
	require.NoError(t, e)
	require.Zero(t, in.Replicas)
	for _, bad := range []string{`null`, `[]`, `{}`, strings.Replace(good, `,"replicas":0`, "", 1), strings.Replace(good, `:0`, `:null`, 1), strings.TrimSuffix(good, "}") + `,"unexpected":true}`, good + ` {}`, strings.Replace(good, `"replicas":0`, `"replicas":0,"replicasExtra":null`, 1), strings.Repeat(" ", int(MaxJSONBytes)+1)} {
		_, e := Decode[ScaleInput](strings.NewReader(bad))
		require.Error(t, e)
	}
}

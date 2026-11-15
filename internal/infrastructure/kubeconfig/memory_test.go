package kubeconfig

import (
	"context"
	"strings"
	"testing"
)

const memoryFixture = `apiVersion: v1
kind: Config
current-context: selected
clusters:
- name: target
  cluster:
    server: https://127.0.0.1:6443
contexts:
- name: selected
  context:
    cluster: target
    user: user
users:
- name: user
  user:
    token: fixture-only
`

func TestMemoryProviderIsolation(t *testing.T) {
	data := []byte(memoryFixture)
	p, err := NewMemoryProvider(data)
	if err != nil {
		t.Fatal(err)
	}
	data[0] = 'X'
	doc, err := p.Load(context.Background(), Reference{})
	if err != nil || doc.Bytes[0] != 'a' {
		t.Fatal("input aliases provider")
	}
	doc.Bytes[0] = 'Y'
	doc, _ = p.Load(context.Background(), Reference{})
	if doc.Bytes[0] != 'a' {
		t.Fatal("output aliases provider")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = p.Load(ctx, Reference{}); err == nil {
		t.Fatal("cancellation ignored")
	}
}
func TestMemoryProviderRejectsHostAccess(t *testing.T) {
	for _, field := range []string{"exec:\n      command: /bin/sh", "tokenFile: /tmp/token", "client-certificate: /tmp/cert", "client-key: /tmp/key", "as: root", "auth-provider:\n      name: external"} {
		t.Run(strings.Split(field, ":")[0], func(t *testing.T) {
			doc := strings.Replace(memoryFixture, "token: fixture-only", field, 1)
			if _, err := NewMemoryProvider([]byte(doc)); err == nil {
				t.Fatal("host credential mechanism accepted")
			}
		})
	}
	for _, doc := range []string{"", strings.Repeat("x", 128*1024+1), strings.Replace(memoryFixture, "server: https://127.0.0.1:6443", "server: https://127.0.0.1:6443\n    certificate-authority: /tmp/ca", 1), strings.Replace(memoryFixture, "current-context: selected", "current-context: absent", 1)} {
		if _, err := NewMemoryProvider([]byte(doc)); err == nil {
			t.Fatal("invalid document accepted")
		}
	}
}

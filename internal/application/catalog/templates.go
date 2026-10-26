package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kyaml "k8s.io/apimachinery/pkg/util/yaml"
	"regexp"
	"strings"
	"text/template"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation"
	d "olixops-k8s-ctl/internal/domain/catalog"
)

const baseTemplate = `apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: {{.Name}}-data
  namespace: {{.Namespace}}
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: {{q .Parameters.StorageClass}}
  resources:
    requests:
      storage: {{q .Parameters.Storage}}
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{.Name}}-config
  namespace: {{.Namespace}}
data:
{{if eq .Software "redis"}}  redis.conf: |
    bind 0.0.0.0
    port 6379
    appendonly yes
    dir /data
    protected-mode yes
{{else}}  postgresql.conf: |
    listen_addresses = '*'
    port = 5432
{{end}}---
apiVersion: v1
kind: Service
metadata:
  name: {{.Name}}
  namespace: {{.Namespace}}
spec:
  type: ClusterIP
  selector:
    olixops.io/instance-id: {{.InstanceID}}
  ports:
    - name: database
      port: {{.BackendPort}}
      targetPort: {{.BackendPort}}
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: {{.Name}}
  namespace: {{.Namespace}}
spec:
  serviceName: {{.Name}}
  replicas: 1
  selector:
    matchLabels:
      olixops.io/instance-id: {{.InstanceID}}
  template:
    metadata:
      labels:
        olixops.io/instance-id: {{.InstanceID}}
    spec:
      terminationGracePeriodSeconds: 60
      containers:
        - name: database
          image: {{q .Parameters.Image}}
{{if eq .Software "redis"}}          command: [redis-server]
          args: [/etc/olixops/redis.conf, --requirepass, "$(REDIS_PASSWORD)"]
          env:
            - name: REDIS_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: {{q .Parameters.SecretName}}
                  key: {{q .Parameters.SecretKey}}
{{else}}          args: [postgres, -c, config_file=/etc/olixops/postgresql.conf]
          env:
            - name: POSTGRES_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: {{q .Parameters.SecretName}}
                  key: {{q .Parameters.SecretKey}}
            - name: POSTGRES_USER
              value: {{q .Parameters.Username}}
            - name: POSTGRES_DB
              value: {{q .Parameters.Database}}
            - name: PGDATA
              value: /var/lib/postgresql/data/pgdata
{{end}}          ports:
            - containerPort: {{.BackendPort}}
              name: database
          resources:
            requests:
              cpu: {{q .Parameters.CPU}}
              memory: {{q .Parameters.Memory}}
            limits:
              cpu: {{q .Parameters.CPU}}
              memory: {{q .Parameters.Memory}}
          startupProbe:
            tcpSocket: {port: database}
            failureThreshold: 60
            periodSeconds: 5
          readinessProbe:
            tcpSocket: {port: database}
            periodSeconds: 5
          livenessProbe:
            tcpSocket: {port: database}
            periodSeconds: 10
          volumeMounts:
            - name: data
              mountPath: {{if eq .Software "redis"}}/data{{else}}/var/lib/postgresql/data{{end}}
            - name: config
              mountPath: /etc/olixops
              readOnly: true
      volumes:
        - name: data
          persistentVolumeClaim:
            claimName: {{.Name}}-data
        - name: config
          configMap:
            name: {{.Name}}-config
`

type SaveTemplateInput struct {
	Software         string       `json:"software"`
	Version          int          `json:"version"`
	Defaults         d.Parameters `json:"defaults"`
	ManifestTemplate string       `json:"manifestTemplate,omitempty"`
}
type TemplateInput struct {
	Software string `json:"software"`
	Version  int    `json:"version"`
}

func templateID(software string, version int) string { return fmt.Sprintf("%s:%d", software, version) }
func softwareValid(s string) bool                    { return s == "redis" || s == "postgres" }
func backend(s string) int {
	if s == "redis" {
		return 6379
	}
	return 5432
}
func schema(sw string) string {
	p := map[string]any{}
	for _, key := range []string{"image", "cpu", "memory", "storage", "storageClass", "secretName", "secretKey", "database", "username"} {
		p[key] = map[string]any{"type": "string"}
	}
	required := []string{"image", "cpu", "memory", "storage", "storageClass", "secretName", "secretKey"}
	if sw == "postgres" {
		required = append(required, "database", "username")
	}
	b, _ := json.Marshal(map[string]any{"type": "object", "additionalProperties": false, "properties": p, "required": required})
	return string(b)
}
func seeds() []d.Template {
	now := time.Now().UTC()
	var out []d.Template
	for _, sw := range []string{"redis", "postgres"} {
		p := d.Parameters{CPU: "250m", Memory: "256Mi", Storage: "1Gi", SecretKey: "password"}
		if sw == "redis" {
			p.Image = "redis:7.4.2"
		} else {
			p.Image = "postgres:17.4"
			p.Memory = "512Mi"
			p.Storage = "5Gi"
			p.Database = "app"
			p.Username = "app"
		}
		out = append(out, d.Template{ID: templateID(sw, 1), Software: sw, Version: 1, Defaults: p, ManifestTemplate: baseTemplate, ParameterSchema: schema(sw), CreatedAt: now})
	}
	return out
}
func (s *Service) Templates(ctx context.Context) ([]d.Template, error) {
	if err := s.policy(ctx, "template.list", false, nil); err != nil {
		return nil, err
	}
	out := []d.Template{}
	err := s.repo.View(ctx, func(st *d.State) error {
		for _, v := range st.Templates {
			out = append(out, v)
		}
		return nil
	})
	sortBy(out, func(v d.Template) string { return v.ID })
	return out, err
}
func (s *Service) Template(ctx context.Context, in TemplateInput) (d.Template, error) {
	var out d.Template
	if err := s.policy(ctx, "template.get", false, in); err != nil {
		return out, err
	}
	err := s.repo.View(ctx, func(st *d.State) error {
		var ok bool
		out, ok = st.Templates[templateID(in.Software, in.Version)]
		if !ok {
			return d.Fail("NOT_FOUND", "template version does not exist")
		}
		return nil
	})
	return out, err
}
func (s *Service) SaveTemplate(ctx context.Context, in SaveTemplateInput) (d.Template, error) {
	var out d.Template
	if err := s.policy(ctx, "template.save", true, in); err != nil {
		return out, err
	}
	if !softwareValid(in.Software) || in.Version < 1 || len(in.ManifestTemplate) > 1<<20 {
		return out, d.Fail("INVALID_PARAMETER", "supported software is redis or postgres; version must be positive; template limit is 1 MiB")
	}
	if in.ManifestTemplate == "" {
		in.ManifestTemplate = baseTemplate
	}
	out = d.Template{ID: templateID(in.Software, in.Version), Software: in.Software, Version: in.Version, Defaults: in.Defaults, ParameterSchema: schema(in.Software), ManifestTemplate: in.ManifestTemplate, CreatedAt: time.Now().UTC()}
	// Validate even templates whose required installation parameters have no default.
	sample := out.Defaults
	if sample.StorageClass == "" {
		sample.StorageClass = "validation-only"
	}
	if sample.SecretName == "" {
		sample.SecretName = "validation-only"
	}
	sample = merge(seedsFor(in.Software).Defaults, sample)
	if err := validateParams(in.Software, sample); err != nil {
		return d.Template{}, err
	}
	yaml, err := render(out, d.Instance{ID: "validation", ResourceName: "validation", Namespace: s.gateway.Namespace, Software: in.Software}, sample, 1)
	if err != nil {
		return d.Template{}, err
	}
	if err = s.cluster.Check(ctx, yaml, s.gateway.Namespace); err != nil {
		return d.Template{}, err
	}
	err = s.repo.Update(ctx, func(st *d.State) error {
		if old, ok := st.Templates[out.ID]; ok {
			if old.ManifestTemplate == out.ManifestTemplate && old.Defaults == out.Defaults {
				out = old
				return nil
			}
			return d.Fail("TEMPLATE_VERSION_EXISTS", "template versions are immutable; use a new version")
		}
		st.Templates[out.ID] = out
		return nil
	})
	return out, err
}
func seedsFor(sw string) d.Template {
	for _, v := range seeds() {
		if v.Software == sw {
			return v
		}
	}
	return d.Template{}
}
func merge(base, over d.Parameters) d.Parameters {
	if over.Image != "" {
		base.Image = over.Image
	}
	if over.CPU != "" {
		base.CPU = over.CPU
	}
	if over.Memory != "" {
		base.Memory = over.Memory
	}
	if over.Storage != "" {
		base.Storage = over.Storage
	}
	if over.StorageClass != "" {
		base.StorageClass = over.StorageClass
	}
	if over.SecretName != "" {
		base.SecretName = over.SecretName
	}
	if over.SecretKey != "" {
		base.SecretKey = over.SecretKey
	}
	if over.Database != "" {
		base.Database = over.Database
	}
	if over.Username != "" {
		base.Username = over.Username
	}
	return base
}
func validateParams(sw string, p d.Parameters) error {
	for key, val := range map[string]string{"image": p.Image, "cpu": p.CPU, "memory": p.Memory, "storage": p.Storage, "storageClass": p.StorageClass, "secretName": p.SecretName, "secretKey": p.SecretKey} {
		if val == "" {
			return d.Fail("MISSING_PARAMETER", "required parameter: "+key)
		}
	}
	for key, val := range map[string]string{"cpu": p.CPU, "memory": p.Memory, "storage": p.Storage} {
		q, err := resource.ParseQuantity(val)
		if err != nil || q.Sign() <= 0 {
			return d.Fail("INVALID_PARAMETER", key+" must be a positive Kubernetes quantity")
		}
	}
	if len(validation.IsDNS1123Subdomain(p.StorageClass)) > 0 || len(validation.IsDNS1123Subdomain(p.SecretName)) > 0 || len(validation.IsConfigMapKey(p.SecretKey)) > 0 {
		return d.Fail("INVALID_PARAMETER", "invalid StorageClass or Secret reference")
	}
	if len(p.Image) > 256 || strings.ContainsAny(p.Image, " \t\n\r") || (!strings.Contains(p.Image, ":") && !strings.Contains(p.Image, "@sha256:")) {
		return d.Fail("INVALID_PARAMETER", "image must include an explicit tag or digest")
	}
	if sw == "postgres" {
		r := regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,62}$`)
		if !r.MatchString(p.Database) || !r.MatchString(p.Username) {
			return d.Fail("INVALID_PARAMETER", "PostgreSQL database and username must be simple identifiers")
		}
	}
	return nil
}
func render(t d.Template, i d.Instance, p d.Parameters, rev int) (string, error) {
	tpl, err := template.New("manifest").Option("missingkey=error").Funcs(template.FuncMap{"q": func(v string) string { b, _ := json.Marshal(v); return string(b) }}).Parse(t.ManifestTemplate)
	if err != nil {
		return "", d.Fail("INVALID_TEMPLATE", "cannot parse manifest template")
	}
	var b bytes.Buffer
	err = tpl.Execute(&b, struct {
		Name, Namespace, Software, InstanceID string
		BackendPort                           int
		Parameters                            d.Parameters
	}{i.ResourceName, i.Namespace, i.Software, i.ID, backend(i.Software), p})
	if err != nil || b.Len() > 2<<20 {
		return "", d.Fail("INVALID_TEMPLATE", "cannot render template or output exceeds 2 MiB")
	}
	decoder := kyaml.NewYAMLOrJSONDecoder(strings.NewReader(b.String()), 4096)
	for {
		var obj map[string]any
		err := decoder.Decode(&obj)
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", d.Fail("INVALID_TEMPLATE", "rendered YAML is invalid")
		}
		if len(obj) == 0 {
			continue
		}
		u := &unstructured.Unstructured{Object: obj}
		if u.GetKind() == "TCPRoute" || u.GetNamespace() != i.Namespace {
			return "", d.Fail("INVALID_TEMPLATE", "template resources must declare the instance namespace; TCPRoute is generated separately")
		}
	}
	return stamp(b.String(), i.ID, rev)
}

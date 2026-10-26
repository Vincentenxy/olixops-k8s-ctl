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
func softwareValid(s string) bool                    { _, ok := d.Profile(s); return ok }
func backend(s string) int                           { profile, _ := d.Profile(s); return profile.BackendPort }
func schema(sw string) string {
	profile, _ := d.Profile(sw)
	properties := map[string]any{}
	for _, key := range []string{"image", "cpu", "memory"} {
		properties[key] = map[string]any{"type": "string"}
	}
	required := []string{"image", "cpu", "memory"}
	if profile.Persistent {
		for _, key := range []string{"storage", "storageClass"} {
			properties[key] = map[string]any{"type": "string"}
		}
		required = append(required, "storage", "storageClass")
	}
	if supportsSecret(sw) {
		for _, key := range []string{"secretName", "secretKey"} {
			properties[key] = map[string]any{"type": "string"}
		}
	}
	if sw == "postgres" || sw == "mysql" || sw == "mongodb" {
		properties["database"] = map[string]any{"type": "string"}
		properties["username"] = map[string]any{"type": "string"}
		if sw != "mongodb" {
			required = append(required, "database", "username")
		}
	}
	if sw == "nginx" {
		properties["content"] = map[string]any{"type": "string", "maxLength": 262144}
	}
	if sw == "sandbox" {
		properties["runtimeClassName"] = map[string]any{"type": "string"}
		properties["ttlSeconds"] = map[string]any{"type": "integer", "minimum": 60, "maximum": 86400}
	}
	b, _ := json.Marshal(map[string]any{"type": "object", "additionalProperties": false, "properties": properties, "required": required})
	return string(b)
}
func supportsSecret(sw string) bool {
	return sw == "redis" || sw == "postgres" || sw == "mysql" || sw == "mongodb"
}
func legacySchema(sw string) string {
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
	// Preserve historical definitions; saved template versions are immutable.
	for _, sw := range []string{"redis", "postgres"} {
		p := d.Parameters{CPU: "250m", Memory: "256Mi", Storage: "1Gi", SecretKey: "password", Image: "redis:7.4.2"}
		if sw == "postgres" {
			p.Image = "postgres:17.4"
			p.Memory = "512Mi"
			p.Storage = "5Gi"
			p.Database = "app"
			p.Username = "app"
		}
		out = append(out, d.Template{ID: templateID(sw, 1), Software: sw, Version: 1, Defaults: p, ManifestTemplate: baseTemplate, ParameterSchema: legacySchema(sw), CreatedAt: now})
	}
	for _, profile := range d.Profiles() {
		sw := profile.Software
		p := d.Parameters{CPU: "250m", Memory: "512Mi", Storage: "5Gi"}
		version := 1
		switch sw {
		case "redis":
			version = 2
			p.Image = "redis:7.4.2"
			p.Memory = "256Mi"
			p.Storage = "1Gi"
			p.SecretKey = "password"
		case "postgres":
			version = 2
			p.Image = "postgres:17.4"
			p.Database = "app"
			p.Username = "app"
			p.SecretKey = "password"
		case "mysql":
			p.Image = "mysql:8.4.4"
			p.Memory = "1Gi"
			p.Database = "app"
			p.Username = "root"
			p.SecretKey = "password"
		case "mongodb":
			p.Image = "mongo:8.0.8"
			p.Username = "root"
			p.Database = "app"
			p.SecretKey = "password"
		case "milvus":
			p.Image = "milvusdb/milvus:v2.5.11"
			p.CPU = "2"
			p.Memory = "4Gi"
			p.Storage = "10Gi"
		case "nginx":
			p.Image = "nginx:1.28-alpine"
			p.CPU = "100m"
			p.Memory = "128Mi"
			p.Storage = ""
			p.Content = "<!doctype html><html><head><title>Nginx</title></head><body><h1>Nginx is ready</h1></body></html>"
		case "sandbox":
			p.Image = "quay.io/jupyter/base-notebook:2025-03-14"
			p.CPU = "500m"
			p.Memory = "1Gi"
			p.Storage = ""
			p.TTLSeconds = 7200
		}
		out = append(out, d.Template{ID: templateID(sw, version), Software: sw, Version: version, Defaults: p, ManifestTemplate: standardTemplate, ParameterSchema: schema(sw), CreatedAt: now})
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
	in.Software = d.NormalizeSoftware(in.Software)
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
	in.Software = d.NormalizeSoftware(in.Software)
	if err := s.policy(ctx, "template.save", true, in); err != nil {
		return out, err
	}
	if !softwareValid(in.Software) || in.Version < 1 || len(in.ManifestTemplate) > 1<<20 {
		return out, d.Fail("INVALID_PARAMETER", "unsupported software, non-positive version, or template exceeds 1 MiB")
	}
	if in.ManifestTemplate == "" {
		in.ManifestTemplate = standardTemplate
	}
	out = d.Template{ID: templateID(in.Software, in.Version), Software: in.Software, Version: in.Version, Defaults: in.Defaults, ParameterSchema: schema(in.Software), ManifestTemplate: in.ManifestTemplate, CreatedAt: time.Now().UTC()}
	// Validate even templates whose required installation parameters have no default.
	sample := out.Defaults
	if sample.StorageClass == "" {
		sample.StorageClass = "validation-only"
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
	sw = d.NormalizeSoftware(sw)
	var current d.Template
	for _, v := range seeds() {
		if v.Software == sw && v.Version > current.Version {
			current = v
		}
	}
	return current
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
	if over.Content != "" {
		base.Content = over.Content
	}
	if over.RuntimeClassName != "" {
		base.RuntimeClassName = over.RuntimeClassName
	}
	if over.TTLSeconds != 0 {
		base.TTLSeconds = over.TTLSeconds
	}
	return base
}
func validateParams(sw string, p d.Parameters) error {
	sw = d.NormalizeSoftware(sw)
	profile, ok := d.Profile(sw)
	if !ok {
		return d.Fail("INVALID_PARAMETER", "unsupported software")
	}
	required := map[string]string{"image": p.Image, "cpu": p.CPU, "memory": p.Memory}
	quantities := map[string]string{"cpu": p.CPU, "memory": p.Memory}
	if profile.Persistent {
		required["storage"] = p.Storage
		required["storageClass"] = p.StorageClass
		quantities["storage"] = p.Storage
	}
	if p.SecretName != "" {
		if !supportsSecret(sw) {
			return d.Fail("INVALID_PARAMETER", "this software does not accept a Secret reference")
		}
		required["secretKey"] = p.SecretKey
		if len(validation.IsDNS1123Subdomain(p.SecretName)) > 0 || len(validation.IsConfigMapKey(p.SecretKey)) > 0 {
			return d.Fail("INVALID_PARAMETER", "invalid Secret reference")
		}
	}
	for key, val := range required {
		if val == "" {
			return d.Fail("MISSING_PARAMETER", "required parameter: "+key)
		}
	}
	for key, val := range quantities {
		q, err := resource.ParseQuantity(val)
		if err != nil || q.Sign() <= 0 {
			return d.Fail("INVALID_PARAMETER", key+" must be a positive Kubernetes quantity")
		}
	}
	if profile.Persistent && len(validation.IsDNS1123Subdomain(p.StorageClass)) > 0 {
		return d.Fail("INVALID_PARAMETER", "invalid StorageClass")
	}
	if !explicitImage(p.Image) {
		return d.Fail("INVALID_PARAMETER", "image must include an explicit tag or digest")
	}
	if sw == "postgres" || sw == "mysql" || (sw == "mongodb" && p.SecretName != "") {
		r := regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,62}$`)
		if !r.MatchString(p.Username) || (sw != "mongodb" && !r.MatchString(p.Database)) {
			return d.Fail("INVALID_PARAMETER", "database and username must be simple identifiers")
		}
		if sw == "mysql" && len(p.Username) > 32 {
			return d.Fail("INVALID_PARAMETER", "MySQL username cannot exceed 32 characters")
		}
		if sw == "mysql" && p.SecretName == "" && p.Username != "root" {
			return d.Fail("INVALID_PARAMETER", "MySQL without a Secret uses username root")
		}
	}
	if sw != "nginx" && p.Content != "" {
		return d.Fail("INVALID_PARAMETER", "content is only supported by nginx")
	}
	if len(p.Content) > 256<<10 {
		return d.Fail("INVALID_PARAMETER", "content exceeds 256 KiB")
	}
	if sw != "sandbox" && (p.RuntimeClassName != "" || p.TTLSeconds != 0) {
		return d.Fail("INVALID_PARAMETER", "runtimeClassName and ttlSeconds are only supported by sandbox")
	}
	if p.RuntimeClassName != "" && len(validation.IsDNS1123Subdomain(p.RuntimeClassName)) > 0 {
		return d.Fail("INVALID_PARAMETER", "invalid RuntimeClass name")
	}
	if sw == "sandbox" && (p.TTLSeconds < 60 || p.TTLSeconds > 86400) {
		return d.Fail("INVALID_PARAMETER", "sandbox ttlSeconds must be between 60 and 86400")
	}
	return nil
}
func explicitImage(image string) bool {
	if len(image) > 256 || strings.ContainsAny(image, " \t\n\r") {
		return false
	}
	if strings.Contains(image, "@") {
		parts := strings.Split(image, "@sha256:")
		if len(parts) != 2 || parts[0] == "" || len(parts[1]) != 64 {
			return false
		}
		for _, ch := range parts[1] {
			if !strings.ContainsRune("0123456789abcdef", ch) {
				return false
			}
		}
		return true
	}
	last := image[strings.LastIndex(image, "/")+1:]
	parts := strings.Split(last, ":")
	return len(parts) == 2 && parts[0] != "" && regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`).MatchString(parts[1])
}
func render(t d.Template, i d.Instance, p d.Parameters, rev int) (string, error) {
	if t.ManifestTemplate == baseTemplate && p.SecretName == "" {
		return "", d.Fail("MISSING_PARAMETER", "legacy template requires secretName; use the current template for a deployment without authentication")
	}
	tpl, err := template.New("manifest").Option("missingkey=error").Funcs(template.FuncMap{"q": func(v string) string { b, _ := json.Marshal(v); return string(b) }}).Parse(t.ManifestTemplate)
	if err != nil {
		return "", d.Fail("INVALID_TEMPLATE", "cannot parse manifest template")
	}
	profile, ok := d.Profile(i.Software)
	if !ok {
		return "", d.Fail("INVALID_TEMPLATE", "unsupported software")
	}
	var b bytes.Buffer
	err = tpl.Execute(&b, struct {
		Name, Namespace, Software, InstanceID string
		BackendPort                           int
		Parameters                            d.Parameters
		Profile                               d.SoftwareProfile
		DataPath                              string
	}{i.ResourceName, i.Namespace, profile.Software, i.ID, profile.BackendPort, p, profile, dataPath(profile.Software)})
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
		allowed := map[string]string{"StatefulSet": "apps/v1", "Deployment": "apps/v1", "Service": "v1", "PersistentVolumeClaim": "v1", "ConfigMap": "v1"}
		if allowed[u.GetKind()] != u.GetAPIVersion() || u.GetNamespace() != i.Namespace {
			return "", d.Fail("INVALID_TEMPLATE", "template may only contain allowed workload, Service, PVC and ConfigMap resources in the instance namespace; TCPRoute is generated separately")
		}
	}
	return stamp(b.String(), i.ID, rev)
}

package catalog

// standardTemplate is stored as an immutable catalog template. All processes and
// arguments are predefined; callers can only override the typed Parameters.
const standardTemplate = `{{if .Profile.Persistent}}apiVersion: v1
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
kind: Service
metadata:
  name: {{.Name}}-headless
  namespace: {{.Namespace}}
spec:
  clusterIP: None
  selector:
    olixops.io/instance-id: {{.InstanceID}}
  ports:
    - name: service
      port: {{.BackendPort}}
      targetPort: service
---
{{end}}apiVersion: v1
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
    protected-mode {{if .Parameters.SecretName}}yes{{else}}no{{end}}
{{else if eq .Software "postgres"}}  postgresql.conf: |
    listen_addresses = '*'
    port = 5432
{{else if eq .Software "mysql"}}  custom.cnf: |
    [mysqld]
    bind-address=0.0.0.0
    port=3306
    character-set-server=utf8mb4
{{else if eq .Software "mongodb"}}  mongod.conf: |
    storage:
      dbPath: /data/db
    net:
      bindIpAll: true
      port: 27017
{{else if eq .Software "milvus"}}  embedEtcd.yaml: |
    listen-client-urls: http://127.0.0.1:2379
    advertise-client-urls: http://127.0.0.1:2379
    quota-backend-bytes: 4294967296
    auto-compaction-mode: revision
    auto-compaction-retention: '1000'
  user.yaml: |
    common:
      storageType: local
    mq:
      type: rocksmq
    rocksmq:
      path: /var/lib/milvus/rdb_data
    localStorage:
      path: /var/lib/milvus/data
    log:
      file:
        rootPath: /var/lib/milvus/logs
{{else if eq .Software "nginx"}}  index.html: {{q .Parameters.Content}}
{{else}}  README.txt: "Python JupyterLab development environment; files are ephemeral."
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
    - name: service
      port: {{.BackendPort}}
      targetPort: service
---
apiVersion: apps/v1
kind: {{.Profile.WorkloadKind}}
metadata:
  name: {{.Name}}
  namespace: {{.Namespace}}
spec:
{{if .Profile.Persistent}}  serviceName: {{.Name}}-headless
{{else}}  strategy:
    type: Recreate
{{end}}  replicas: 1
  selector:
    matchLabels:
      olixops.io/instance-id: {{.InstanceID}}
  template:
    metadata:
      labels:
        olixops.io/instance-id: {{.InstanceID}}
    spec:
      automountServiceAccountToken: false
      terminationGracePeriodSeconds: 60
{{if eq .Software "milvus"}}      securityContext:
        seccompProfile:
          type: Unconfined
{{end}}{{if eq .Software "sandbox"}}      securityContext:
        runAsNonRoot: true
        runAsUser: 1000
        runAsGroup: 100
        fsGroup: 100
        seccompProfile:
          type: RuntimeDefault
{{if .Parameters.RuntimeClassName}}      runtimeClassName: {{q .Parameters.RuntimeClassName}}
{{end}}{{end}}      containers:
        - name: service
          image: {{q .Parameters.Image}}
{{if eq .Software "redis"}}          command: [redis-server]
          args: [/etc/olixops/redis.conf{{if .Parameters.SecretName}}, --requirepass, "$(REDIS_PASSWORD)"{{end}}]
{{if .Parameters.SecretName}}          env:
            - name: REDIS_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: {{q .Parameters.SecretName}}
                  key: {{q .Parameters.SecretKey}}
{{end}}{{else if eq .Software "postgres"}}          args: [postgres, -c, config_file=/etc/olixops/postgresql.conf]
          env:
{{if .Parameters.SecretName}}            - name: POSTGRES_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: {{q .Parameters.SecretName}}
                  key: {{q .Parameters.SecretKey}}
{{else}}            - name: POSTGRES_HOST_AUTH_METHOD
              value: trust
{{end}}            - name: POSTGRES_USER
              value: {{q .Parameters.Username}}
            - name: POSTGRES_DB
              value: {{q .Parameters.Database}}
            - name: PGDATA
              value: /var/lib/postgresql/data/pgdata
{{else if eq .Software "mysql"}}          env:
{{if .Parameters.SecretName}}            - name: MYSQL_ROOT_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: {{q .Parameters.SecretName}}
                  key: {{q .Parameters.SecretKey}}
{{if ne .Parameters.Username "root"}}            - name: MYSQL_USER
              value: {{q .Parameters.Username}}
            - name: MYSQL_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: {{q .Parameters.SecretName}}
                  key: {{q .Parameters.SecretKey}}
{{end}}{{else}}            - name: MYSQL_ALLOW_EMPTY_PASSWORD
              value: "yes"
{{end}}            - name: MYSQL_ROOT_HOST
              value: "%"
            - name: MYSQL_DATABASE
              value: {{q .Parameters.Database}}
{{else if eq .Software "mongodb"}}          args: [mongod, --config, /etc/olixops/mongod.conf]
{{if .Parameters.SecretName}}          env:
            - name: MONGO_INITDB_ROOT_USERNAME
              value: {{q .Parameters.Username}}
            - name: MONGO_INITDB_ROOT_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: {{q .Parameters.SecretName}}
                  key: {{q .Parameters.SecretKey}}
{{end}}{{else if eq .Software "milvus"}}          command: [milvus]
          args: [run, standalone]
          env:
            - {name: DEPLOY_MODE, value: STANDALONE}
            - {name: ETCD_USE_EMBED, value: "true"}
            - {name: ETCD_DATA_DIR, value: /var/lib/milvus/etcd}
            - {name: ETCD_CONFIG_PATH, value: /etc/olixops/embedEtcd.yaml}
            - {name: COMMON_STORAGETYPE, value: local}
{{else if eq .Software "sandbox"}}          args: [start-notebook.py, --IdentityProvider.token=, --PasswordIdentityProvider.hashed_password=, --ServerApp.ip=0.0.0.0, --ServerApp.port=8888, --ServerApp.open_browser=False, --ServerApp.allow_remote_access=True]
          workingDir: /home/jovyan/work
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop: [ALL]
{{end}}          ports:
            - containerPort: {{.BackendPort}}
              name: service
          resources:
            requests:
              cpu: {{q .Parameters.CPU}}
              memory: {{q .Parameters.Memory}}
            limits:
              cpu: {{q .Parameters.CPU}}
              memory: {{q .Parameters.Memory}}
          startupProbe:
{{if eq .Software "milvus"}}            httpGet: {path: /healthz, port: 9091}
{{else}}            tcpSocket: {port: service}
{{end}}            failureThreshold: 60
            periodSeconds: 5
          readinessProbe:
{{if eq .Software "milvus"}}            httpGet: {path: /healthz, port: 9091}
{{else}}            tcpSocket: {port: service}
{{end}}            periodSeconds: 5
          livenessProbe:
{{if eq .Software "milvus"}}            httpGet: {path: /healthz, port: 9091}
{{else}}            tcpSocket: {port: service}
{{end}}            periodSeconds: 10
          volumeMounts:
{{if .Profile.Persistent}}            - name: data
              mountPath: {{q .DataPath}}
{{end}}{{if eq .Software "nginx"}}            - name: config
              mountPath: /usr/share/nginx/html
              readOnly: true
{{else if eq .Software "mysql"}}            - name: config
              mountPath: /etc/mysql/conf.d
              readOnly: true
{{else if eq .Software "milvus"}}            - name: config
              mountPath: /etc/olixops
              readOnly: true
            - name: config
              mountPath: /milvus/configs/user.yaml
              subPath: user.yaml
              readOnly: true
{{else if eq .Software "sandbox"}}            - name: work
              mountPath: /home/jovyan/work
            - name: config
              mountPath: /etc/olixops
              readOnly: true
{{else}}            - name: config
              mountPath: /etc/olixops
              readOnly: true
{{end}}      volumes:
{{if .Profile.Persistent}}        - name: data
          persistentVolumeClaim:
            claimName: {{.Name}}-data
{{end}}{{if eq .Software "sandbox"}}        - name: work
          emptyDir:
            sizeLimit: 1Gi
{{end}}        - name: config
          configMap:
            name: {{.Name}}-config
`

func dataPath(software string) string {
	switch software {
	case "redis":
		return "/data"
	case "postgres":
		return "/var/lib/postgresql/data"
	case "mysql":
		return "/var/lib/mysql"
	case "mongodb":
		return "/data/db"
	case "milvus":
		return "/var/lib/milvus"
	default:
		return ""
	}
}

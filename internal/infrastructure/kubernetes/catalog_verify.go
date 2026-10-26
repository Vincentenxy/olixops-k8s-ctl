package kubernetes

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	d "olixops-k8s-ctl/internal/domain/catalog"
)

// CatalogVerify runs fixed, non-mutating protocol commands against the configured
// LB and this saved deployment's port. Credentials are fetched only in memory.
func (c *Client) CatalogVerify(ctx context.Context, g d.Gateway, dep d.Deployment, i d.Instance, p d.Parameters) (d.AccessReport, error) {
	out := d.AccessReport{Port: dep.Port, CheckedAt: time.Now().UTC(), Result: "LB_ADDRESS_UNAVAILABLE"}
	profile, ok := d.Profile(i.Software)
	if !ok {
		return out, d.Fail("UNSUPPORTED_SOFTWARE", "standard service software is not supported")
	}
	svc, err := c.core.CoreV1().Services(g.Namespace).Get(ctx, g.Service, metav1.GetOptions{})
	if err != nil {
		return out, err
	}
	if len(svc.Status.LoadBalancer.Ingress) == 0 {
		return out, nil
	}
	entry := svc.Status.LoadBalancer.Ingress[0]
	host := entry.IP
	if host == "" {
		host = entry.Hostname
	}
	if host == "" {
		return out, nil
	}
	out.Address = net.JoinHostPort(host, strconv.Itoa(dep.Port))
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", out.Address)
	if err != nil {
		out.Result = "TCP_UNREACHABLE"
		return out, nil
	}
	defer conn.Close()
	out.TCPVerified = true
	password := ""
	if p.SecretName != "" {
		secret, err := c.core.CoreV1().Secrets(i.Namespace).Get(ctx, p.SecretName, metav1.GetOptions{})
		if err != nil {
			return out, d.Fail("SECRET_UNAVAILABLE", "referenced credential is unavailable")
		}
		password = string(secret.Data[p.SecretKey])
		if password == "" {
			return out, d.Fail("SECRET_KEY_MISSING", "referenced credential key is empty")
		}
	}
	out.Result = "PROTOCOL_CHECK_FAILED"
	if profile.Protocol == "redis" {
		out.ProtocolVerified = verifyCatalogRedis(ctx, conn, password, p.SecretName != "")
	} else {
		conn.Close()
		switch profile.Protocol {
		case "postgres":
			out.ProtocolVerified = verifyCatalogPostgres(ctx, host, dep.Port, p, password)
		case "mysql":
			out.ProtocolVerified = verifyCatalogMySQL(ctx, out.Address, p, password)
		case "mongodb":
			out.ProtocolVerified = verifyCatalogMongo(ctx, out.Address, p, password)
		case "http", "milvus":
			out.ProtocolVerified = verifyCatalogHTTP(ctx, out.Address, profile.Protocol, p, password)
		}
	}
	if out.ProtocolVerified {
		out.Result = "SERVER_SIDE_PROTOCOL_VERIFIED"
	}
	return out, nil
}

func verifyCatalogRedis(ctx context.Context, conn net.Conn, password string, authenticate bool) bool {
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)
	reader := bufio.NewReader(io.LimitReader(conn, 8192))
	if authenticate {
		auth := fmt.Sprintf("*2\r\n$4\r\nAUTH\r\n$%d\r\n%s\r\n", len(password), password)
		if _, err := io.WriteString(conn, auth); err != nil {
			return false
		}
		line, err := reader.ReadString('\n')
		if err != nil || strings.TrimSpace(line) != "+OK" {
			return false
		}
	}
	if _, err := io.WriteString(conn, "*1\r\n$4\r\nPING\r\n"); err != nil {
		return false
	}
	line, err := reader.ReadString('\n')
	return err == nil && strings.TrimSpace(line) == "+PONG"
}

func verifyCatalogPostgres(ctx context.Context, host string, port int, p d.Parameters, password string) bool {
	// Templates do not configure TLS. Never accept caller-supplied DSNs or return
	// raw driver errors: both could reveal credentials or connection details.
	cfg, err := pgx.ParseConfig("sslmode=disable")
	if err != nil {
		return false
	}
	cfg.Fallbacks = nil
	cfg.RuntimeParams = map[string]string{"application_name": "olixops-k8s-ctl-verify"}
	cfg.Host, cfg.Port, cfg.User, cfg.Database, cfg.Password = host, uint16(port), p.Username, p.Database, password
	cfg.ConnectTimeout = 3 * time.Second
	db, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return false
	}
	defer db.Close(ctx)
	var one int
	return db.QueryRow(ctx, "SELECT 1").Scan(&one) == nil && one == 1
}

func verifyCatalogMySQL(ctx context.Context, address string, p d.Parameters, password string) bool {
	cfg := mysql.NewConfig()
	cfg.Net, cfg.Addr, cfg.User, cfg.Passwd, cfg.DBName = "tcp", address, p.Username, password, p.Database
	cfg.Timeout, cfg.ReadTimeout, cfg.WriteTimeout = 3*time.Second, 3*time.Second, 3*time.Second
	// Driver diagnostics can include server-provided text. Keep verification
	// results deliberately bounded and exclude this text from process logs.
	cfg.Logger = log.New(io.Discard, "", 0)
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return false
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	return db.PingContext(ctx) == nil
}

func verifyCatalogMongo(ctx context.Context, address string, p d.Parameters, password string) bool {
	cfg := options.Client().SetHosts([]string{address}).SetDirect(true).SetConnectTimeout(3 * time.Second).SetServerSelectionTimeout(3 * time.Second)
	if p.SecretName != "" {
		// The official image initializes the root user in the admin database.
		cfg.SetAuth(options.Credential{AuthSource: "admin", Username: p.Username, Password: password})
	}
	db, err := mongo.Connect(cfg)
	if err != nil {
		return false
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		db.Disconnect(cleanup)
	}()
	return db.Ping(ctx, readpref.Primary()) == nil
}

func verifyCatalogHTTP(ctx context.Context, address, protocol string, p d.Parameters, password string) bool {
	method, path, body := http.MethodGet, "/", ""
	if protocol == "milvus" {
		// A fixed collection listing verifies the REST endpoint without writing
		// data. No caller-supplied paths, queries or commands are accepted.
		method, path, body = http.MethodPost, "/v2/vectordb/collections/list", `{"dbName":"default"}`
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+address+path, strings.NewReader(body))
	if err != nil {
		return false
	}
	if protocol == "milvus" {
		req.Header.Set("Content-Type", "application/json")
		if p.SecretName != "" {
			req.Header.Set("Authorization", "Bearer "+p.Username+":"+password)
		}
	}
	transport := &http.Transport{DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, DisableKeepAlives: true, ResponseHeaderTimeout: 3 * time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if protocol == "http" {
		// Jupyter redirects its root to /lab. Do not follow redirects beyond
		// the pinned endpoint, and treat authentication/server failures as false.
		return resp.StatusCode >= 200 && resp.StatusCode < 400
	}
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var result struct {
		Code *int     `json:"code"`
		Data []string `json:"data"`
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&result) == nil && result.Code != nil && *result.Code == 0 && result.Data != nil
}

package kubernetes

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	d "olixops-k8s-ctl/internal/domain/catalog"
)

// CatalogVerify runs fixed, non-mutating protocol commands against the configured
// LB and this saved deployment's port. Credentials are fetched only in memory.
func (c *Client) CatalogVerify(ctx context.Context, g d.Gateway, dep d.Deployment, i d.Instance, p d.Parameters) (d.AccessReport, error) {
	out := d.AccessReport{Port: dep.Port, CheckedAt: time.Now().UTC(), Result: "LB_ADDRESS_UNAVAILABLE"}
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
	out.TCPVerified = true
	secret, err := c.core.CoreV1().Secrets(i.Namespace).Get(ctx, p.SecretName, metav1.GetOptions{})
	if err != nil {
		conn.Close()
		return out, d.Fail("SECRET_UNAVAILABLE", "referenced credential is unavailable")
	}
	password := string(secret.Data[p.SecretKey])
	if password == "" {
		conn.Close()
		return out, d.Fail("SECRET_KEY_MISSING", "referenced credential key is empty")
	}
	out.Result = "PROTOCOL_CHECK_FAILED"
	if i.Software == "redis" {
		defer conn.Close()
		deadline, _ := ctx.Deadline()
		conn.SetDeadline(deadline)
		reader := bufio.NewReader(io.LimitReader(conn, 8192))
		auth := fmt.Sprintf("*2\r\n$4\r\nAUTH\r\n$%d\r\n%s\r\n", len(password), password)
		if _, err = io.WriteString(conn, auth); err != nil {
			return out, nil
		}
		line, err := reader.ReadString('\n')
		if err != nil || strings.TrimSpace(line) != "+OK" {
			return out, nil
		}
		if _, err = io.WriteString(conn, "*1\r\n$4\r\nPING\r\n"); err != nil {
			return out, nil
		}
		line, err = reader.ReadString('\n')
		out.ProtocolVerified = err == nil && strings.TrimSpace(line) == "+PONG"
	} else {
		conn.Close()
		// The initial templates do not configure TLS. TLS-enabled database templates
		// need a separate connection policy; never accept caller-supplied DSNs.
		cfg, err := pgx.ParseConfig("sslmode=disable")
		if err != nil {
			return out, err
		}
		cfg.Fallbacks = nil
		cfg.RuntimeParams = map[string]string{"application_name": "olixops-k8s-ctl-verify"}
		cfg.Host = host
		cfg.Port = uint16(dep.Port)
		cfg.User = p.Username
		cfg.Database = p.Database
		cfg.Password = password
		cfg.ConnectTimeout = 3 * time.Second
		db, err := pgx.ConnectConfig(ctx, cfg)
		if err != nil {
			return out, nil
		}
		defer db.Close(ctx)
		var one int
		err = db.QueryRow(ctx, "SELECT 1").Scan(&one)
		out.ProtocolVerified = err == nil && one == 1
	}
	if out.ProtocolVerified {
		out.Result = "SERVER_SIDE_PROTOCOL_VERIFIED"
	}
	return out, nil
}

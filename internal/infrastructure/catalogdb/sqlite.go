// Package catalogdb persists small catalog aggregates in embedded SQLite.
package catalogdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
	d "olixops-k8s-ctl/internal/domain/catalog"
)

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("catalog database path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	if err = os.Chmod(abs, 0600); err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: abs}
	q := u.Query()
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(FULL)")
	q.Set("_txlock", "immediate")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err = s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) migrate() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY);`); err != nil {
		return err
	}
	var version int
	if err = tx.QueryRow("SELECT COALESCE(MAX(version),0) FROM schema_migrations").Scan(&version); err != nil {
		return err
	}
	if version > 1 {
		return fmt.Errorf("unsupported catalog schema version")
	}
	for _, table := range []string{"service_templates", "service_instances", "service_revisions", "service_deployments", "gateway_ports", "port_events"} {
		if _, err = tx.Exec("CREATE TABLE IF NOT EXISTS " + table + " (id TEXT PRIMARY KEY, payload BLOB NOT NULL CHECK(json_valid(payload)))"); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("INSERT OR IGNORE INTO schema_migrations(version) VALUES(1)"); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) View(ctx context.Context, fn func(*d.State) error) error {
	return s.transact(ctx, false, fn)
}
func (s *Store) Update(ctx context.Context, fn func(*d.State) error) error {
	return s.transact(ctx, true, fn)
}
func (s *Store) transact(ctx context.Context, write bool, fn func(*d.State) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	state := d.NewState()
	if err = load(ctx, tx, "service_templates", state.Templates); err != nil {
		return err
	}
	if err = load(ctx, tx, "service_instances", state.Instances); err != nil {
		return err
	}
	if err = load(ctx, tx, "service_revisions", state.Revisions); err != nil {
		return err
	}
	if err = load(ctx, tx, "service_deployments", state.Deployments); err != nil {
		return err
	}
	if err = load(ctx, tx, "gateway_ports", state.Ports); err != nil {
		return err
	}
	if err = load(ctx, tx, "port_events", state.Events); err != nil {
		return err
	}
	if err = fn(state); err != nil {
		return err
	}
	if !write {
		return tx.Commit()
	}
	if err = save(ctx, tx, "service_templates", state.Templates); err != nil {
		return err
	}
	if err = save(ctx, tx, "service_instances", state.Instances); err != nil {
		return err
	}
	if err = save(ctx, tx, "service_revisions", state.Revisions); err != nil {
		return err
	}
	if err = save(ctx, tx, "service_deployments", state.Deployments); err != nil {
		return err
	}
	if err = save(ctx, tx, "gateway_ports", state.Ports); err != nil {
		return err
	}
	if err = save(ctx, tx, "port_events", state.Events); err != nil {
		return err
	}
	return tx.Commit()
}
func load[T any](ctx context.Context, tx *sql.Tx, table string, dest map[string]T) error {
	rows, err := tx.QueryContext(ctx, "SELECT id,payload FROM "+table)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var raw []byte
		if err = rows.Scan(&id, &raw); err != nil {
			return err
		}
		var v T
		if err = json.Unmarshal(raw, &v); err != nil {
			return err
		}
		dest[id] = v
	}
	return rows.Err()
}
func save[T any](ctx context.Context, tx *sql.Tx, table string, src map[string]T) error {
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO "+table+"(id,payload) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload WHERE payload != excluded.payload")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for id, v := range src {
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if _, err = stmt.ExecContext(ctx, id, raw); err != nil {
			return err
		}
	}
	return nil
}

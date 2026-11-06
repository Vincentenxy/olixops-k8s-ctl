package auth

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
	d "olixops-k8s-ctl/internal/domain/auth"
)

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("PAT database path required")
	}
	abs, e := filepath.Abs(path)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(filepath.Dir(abs), 0700); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(abs, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	f.Close()
	if e = os.Chmod(abs, 0600); e != nil {
		return nil, e
	}
	u := url.URL{Scheme: "file", Path: abs}
	q := u.Query()
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(FULL)")
	q.Set("_txlock", "immediate")
	u.RawQuery = q.Encode()
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	_, e = db.Exec(`CREATE TABLE IF NOT EXISTS personal_tokens (id TEXT PRIMARY KEY,owner_id TEXT NOT NULL,name TEXT NOT NULL,hash TEXT NOT NULL,created_at INTEGER NOT NULL,expires_at INTEGER NOT NULL,last_used_at INTEGER,revoked_at INTEGER); CREATE INDEX IF NOT EXISTS personal_tokens_owner ON personal_tokens(owner_id);`)
	if e != nil {
		db.Close()
		return nil, e
	}
	return &Store{db}, nil
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Create(ctx context.Context, t d.Token, limit int) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var count int
	e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM personal_tokens WHERE owner_id=? AND revoked_at IS NULL AND expires_at>?`, t.OwnerID, t.CreatedAt.Unix()).Scan(&count)
	if e != nil {
		return e
	}
	if count >= limit {
		return d.ErrLimit
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO personal_tokens(id,owner_id,name,hash,created_at,expires_at) VALUES(?,?,?,?,?,?)`, t.ID, t.OwnerID, t.Name, t.Hash, t.CreatedAt.Unix(), t.ExpiresAt.Unix())
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) List(ctx context.Context, owner string) ([]d.Token, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT id,name,created_at,expires_at,last_used_at,revoked_at FROM personal_tokens WHERE owner_id=? ORDER BY created_at DESC,id`, owner)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []d.Token{}
	for rows.Next() {
		var t d.Token
		var created, expires int64
		var used, revoked sql.NullInt64
		if e = rows.Scan(&t.ID, &t.Name, &created, &expires, &used, &revoked); e != nil {
			return nil, e
		}
		t.OwnerID = owner
		t.CreatedAt = time.Unix(created, 0).UTC()
		t.ExpiresAt = time.Unix(expires, 0).UTC()
		if used.Valid {
			x := time.Unix(used.Int64, 0).UTC()
			t.LastUsedAt = &x
		}
		if revoked.Valid {
			x := time.Unix(revoked.Int64, 0).UTC()
			t.RevokedAt = &x
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Store) Revoke(ctx context.Context, id, owner string, now time.Time) error {
	result, e := s.db.ExecContext(ctx, `UPDATE personal_tokens SET revoked_at=COALESCE(revoked_at,?) WHERE id=? AND owner_id=?`, now.Unix(), id, owner)
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return d.ErrNotFound
	}
	return nil
}
func (s *Store) Validate(ctx context.Context, id, owner, hash string, now time.Time) (bool, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return false, e
	}
	defer tx.Rollback()
	var stored string
	e = tx.QueryRowContext(ctx, `SELECT hash FROM personal_tokens WHERE id=? AND owner_id=? AND revoked_at IS NULL AND expires_at>?`, id, owner, now.Unix()).Scan(&stored)
	if e == sql.ErrNoRows {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	if subtle.ConstantTimeCompare([]byte(stored), []byte(hash)) != 1 {
		return false, nil
	}
	_, e = tx.ExecContext(ctx, `UPDATE personal_tokens SET last_used_at=? WHERE id=? AND (last_used_at IS NULL OR last_used_at<?)`, now.Unix(), id, now.Add(-5*time.Minute).Unix())
	if e != nil {
		return false, e
	}
	if e = tx.Commit(); e != nil {
		return false, e
	}
	return true, nil
}

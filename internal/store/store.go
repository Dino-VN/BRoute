package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Store struct {
	db *sql.DB
}

type Settings struct {
	RequireLogin         bool
	PasswordHash         string
	SetupComplete        bool
	BruteForceProtection bool
}

type ProviderConnection struct {
	ID                   string         `json:"id"`
	Provider             string         `json:"provider"`
	Name                 string         `json:"name"`
	Email                string         `json:"email,omitempty"`
	DisplayName          string         `json:"displayName,omitempty"`
	AuthType             string         `json:"authType"`
	IsActive             bool           `json:"isActive"`
	Priority             int            `json:"priority"`
	DefaultModel         string         `json:"defaultModel,omitempty"`
	RateLimitProtection  bool           `json:"rateLimitProtection"`
	AccessToken          string         `json:"accessToken,omitempty"`
	RefreshToken         string         `json:"refreshToken,omitempty"`
	APIKey               string         `json:"apiKey,omitempty"`
	ProjectID            string         `json:"projectId,omitempty"`
	ProviderSpecificData map[string]any `json:"providerSpecificData,omitempty"`
	CreatedAt            time.Time      `json:"createdAt"`
	UpdatedAt            time.Time      `json:"updatedAt"`
}

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", filepath.Join(dataDir, "broute.db")+"?_busy_timeout=5000&_journal_mode=WAL")
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS provider_connections (
			id TEXT PRIMARY KEY,
			provider TEXT NOT NULL,
			name TEXT NOT NULL,
			email TEXT NOT NULL DEFAULT '',
			display_name TEXT NOT NULL DEFAULT '',
			auth_type TEXT NOT NULL,
			is_active INTEGER NOT NULL DEFAULT 1,
			priority INTEGER NOT NULL DEFAULT 0,
			default_model TEXT NOT NULL DEFAULT '',
			rate_limit_protection INTEGER NOT NULL DEFAULT 0,
			access_token TEXT NOT NULL DEFAULT '',
			refresh_token TEXT NOT NULL DEFAULT '',
			api_key TEXT NOT NULL DEFAULT '',
			project_id TEXT NOT NULL DEFAULT '',
			provider_specific_data TEXT NOT NULL DEFAULT '{}',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			action TEXT NOT NULL,
			actor TEXT NOT NULL,
			status TEXT NOT NULL,
			metadata TEXT NOT NULL DEFAULT '{}',
			created_at DATETIME NOT NULL
		)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	alterStmts := []string{
		`ALTER TABLE provider_connections ADD COLUMN email TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE provider_connections ADD COLUMN display_name TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE provider_connections ADD COLUMN is_active INTEGER NOT NULL DEFAULT 1`,
		`ALTER TABLE provider_connections ADD COLUMN priority INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE provider_connections ADD COLUMN default_model TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE provider_connections ADD COLUMN rate_limit_protection INTEGER NOT NULL DEFAULT 0`,
	}
	for _, stmt := range alterStmts {
		_, _ = s.db.Exec(stmt)
	}
	return nil
}

func (s *Store) GetSettings() (Settings, error) {
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return Settings{}, err
	}
	defer rows.Close()

	settings := Settings{RequireLogin: true, BruteForceProtection: true}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return Settings{}, err
		}
		switch key {
		case "requireLogin":
			settings.RequireLogin = value != "false"
		case "password":
			settings.PasswordHash = value
		case "setupComplete":
			settings.SetupComplete = value == "true"
		case "bruteForceProtection":
			settings.BruteForceProtection = value != "false"
		}
	}
	return settings, rows.Err()
}

func (s *Store) UpdateSettings(values map[string]string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, v := range values {
		if _, err := tx.Exec(`INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Audit(action, actor, status string, metadata map[string]any) {
	data, _ := json.Marshal(metadata)
	_, _ = s.db.Exec(`INSERT INTO audit_events(action, actor, status, metadata, created_at) VALUES(?, ?, ?, ?, ?)`, action, actor, status, string(data), time.Now().UTC())
}

func (s *Store) ListConnections(provider string) ([]ProviderConnection, error) {
	query := `SELECT id, provider, name, email, display_name, auth_type, is_active, priority, default_model, rate_limit_protection, access_token, refresh_token, api_key, project_id, provider_specific_data, created_at, updated_at FROM provider_connections`
	args := []any{}
	if provider != "" {
		query += ` WHERE provider = ?`
		args = append(args, provider)
	}
	query += ` ORDER BY provider, priority ASC, updated_at DESC, name ASC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProviderConnection
	for rows.Next() {
		c, err := scanConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) FirstConnection(provider string) (ProviderConnection, error) {
	row := s.db.QueryRow(`SELECT id, provider, name, email, display_name, auth_type, is_active, priority, default_model, rate_limit_protection, access_token, refresh_token, api_key, project_id, provider_specific_data, created_at, updated_at FROM provider_connections WHERE provider = ? AND is_active = 1 AND access_token <> '' ORDER BY priority ASC, updated_at DESC LIMIT 1`, provider)
	return scanConnection(row)
}

func (s *Store) GetConnection(id string) (ProviderConnection, error) {
	row := s.db.QueryRow(`SELECT id, provider, name, email, display_name, auth_type, is_active, priority, default_model, rate_limit_protection, access_token, refresh_token, api_key, project_id, provider_specific_data, created_at, updated_at FROM provider_connections WHERE id = ?`, id)
	return scanConnection(row)
}

func (s *Store) UpsertConnection(c ProviderConnection) error {
	if c.ID == "" || c.Provider == "" {
		return errors.New("missing connection id or provider")
	}
	if c.AuthType == "" {
		c.AuthType = "oauth"
	}
	now := time.Now().UTC()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	psd, _ := json.Marshal(c.ProviderSpecificData)
	_, err := s.db.Exec(`INSERT INTO provider_connections(id, provider, name, email, display_name, auth_type, is_active, priority, default_model, rate_limit_protection, access_token, refresh_token, api_key, project_id, provider_specific_data, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET provider=excluded.provider, name=excluded.name, email=excluded.email, display_name=excluded.display_name, auth_type=excluded.auth_type, is_active=excluded.is_active, priority=excluded.priority, default_model=excluded.default_model, rate_limit_protection=excluded.rate_limit_protection, access_token=excluded.access_token, refresh_token=excluded.refresh_token, api_key=excluded.api_key, project_id=excluded.project_id, provider_specific_data=excluded.provider_specific_data, updated_at=excluded.updated_at`,
		c.ID, c.Provider, c.Name, c.Email, c.DisplayName, c.AuthType, boolInt(c.IsActive), c.Priority, c.DefaultModel, boolInt(c.RateLimitProtection), c.AccessToken, c.RefreshToken, c.APIKey, c.ProjectID, string(psd), c.CreatedAt, c.UpdatedAt)
	return err
}

func (s *Store) DeleteConnection(id string) error {
	res, err := s.db.Exec(`DELETE FROM provider_connections WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

type scanner interface{ Scan(dest ...any) error }

func scanConnection(row scanner) (ProviderConnection, error) {
	var c ProviderConnection
	var psd string
	var active, rateLimitProtection int
	if err := row.Scan(&c.ID, &c.Provider, &c.Name, &c.Email, &c.DisplayName, &c.AuthType, &active, &c.Priority, &c.DefaultModel, &rateLimitProtection, &c.AccessToken, &c.RefreshToken, &c.APIKey, &c.ProjectID, &psd, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return ProviderConnection{}, err
	}
	c.IsActive = active != 0
	c.RateLimitProtection = rateLimitProtection != 0
	if err := json.Unmarshal([]byte(psd), &c.ProviderSpecificData); err != nil {
		return ProviderConnection{}, fmt.Errorf("decode provider data: %w", err)
	}
	return c, nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

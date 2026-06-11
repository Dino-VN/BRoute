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
	QuotaStatus          string         `json:"quotaStatus,omitempty"`
	QuotaDetail          string         `json:"quotaDetail,omitempty"`
	QuotaResetAt         *time.Time     `json:"quotaResetAt,omitempty"`
	QuotaCheckedAt       *time.Time     `json:"quotaCheckedAt,omitempty"`
	QuotaData            map[string]any `json:"quotaData,omitempty"`
	ProviderSpecificData map[string]any `json:"providerSpecificData,omitempty"`
	CreatedAt            time.Time      `json:"createdAt"`
	UpdatedAt            time.Time      `json:"updatedAt"`
}

type APIKey struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Key           string    `json:"key,omitempty"`
	AllowedModels []string  `json:"allowedModels"`
	IsActive      bool      `json:"isActive"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
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
			quota_status TEXT NOT NULL DEFAULT '',
			quota_detail TEXT NOT NULL DEFAULT '',
			quota_reset_at DATETIME,
			quota_checked_at DATETIME,
			quota_data TEXT NOT NULL DEFAULT '{}',
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
		`CREATE TABLE IF NOT EXISTS api_keys (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			key TEXT NOT NULL UNIQUE,
			allowed_models TEXT NOT NULL DEFAULT '[]',
			is_active INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
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
		`ALTER TABLE provider_connections ADD COLUMN quota_status TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE provider_connections ADD COLUMN quota_detail TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE provider_connections ADD COLUMN quota_reset_at DATETIME`,
		`ALTER TABLE provider_connections ADD COLUMN quota_checked_at DATETIME`,
		`ALTER TABLE provider_connections ADD COLUMN quota_data TEXT NOT NULL DEFAULT '{}'`,
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
	query := connectionSelect() + ` FROM provider_connections`
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
	row := s.db.QueryRow(connectionSelect()+` FROM provider_connections WHERE provider = ? AND is_active = 1 AND access_token <> '' ORDER BY priority ASC, updated_at DESC LIMIT 1`, provider)
	return scanConnection(row)
}

func (s *Store) GetConnection(id string) (ProviderConnection, error) {
	row := s.db.QueryRow(connectionSelect()+` FROM provider_connections WHERE id = ?`, id)
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
	quotaData, _ := json.Marshal(c.QuotaData)
	_, err := s.db.Exec(`INSERT INTO provider_connections(id, provider, name, email, display_name, auth_type, is_active, priority, default_model, rate_limit_protection, access_token, refresh_token, api_key, project_id, quota_status, quota_detail, quota_reset_at, quota_checked_at, quota_data, provider_specific_data, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET provider=excluded.provider, name=excluded.name, email=excluded.email, display_name=excluded.display_name, auth_type=excluded.auth_type, is_active=excluded.is_active, priority=excluded.priority, default_model=excluded.default_model, rate_limit_protection=excluded.rate_limit_protection, access_token=excluded.access_token, refresh_token=excluded.refresh_token, api_key=excluded.api_key, project_id=excluded.project_id, quota_status=excluded.quota_status, quota_detail=excluded.quota_detail, quota_reset_at=excluded.quota_reset_at, quota_checked_at=excluded.quota_checked_at, quota_data=excluded.quota_data, provider_specific_data=excluded.provider_specific_data, updated_at=excluded.updated_at`,
		c.ID, c.Provider, c.Name, c.Email, c.DisplayName, c.AuthType, boolInt(c.IsActive), c.Priority, c.DefaultModel, boolInt(c.RateLimitProtection), c.AccessToken, c.RefreshToken, c.APIKey, c.ProjectID, c.QuotaStatus, c.QuotaDetail, c.QuotaResetAt, c.QuotaCheckedAt, string(quotaData), string(psd), c.CreatedAt, c.UpdatedAt)
	return err
}

func (s *Store) UpdateConnectionQuota(id, status, detail string, resetAt *time.Time, data map[string]any) error {
	if id == "" {
		return errors.New("missing connection id")
	}
	checkedAt := time.Now().UTC()
	if data == nil {
		data = map[string]any{}
	}
	encoded, _ := json.Marshal(data)
	_, err := s.db.Exec(`UPDATE provider_connections SET quota_status = ?, quota_detail = ?, quota_reset_at = ?, quota_checked_at = ?, quota_data = ?, updated_at = ? WHERE id = ?`, status, detail, resetAt, checkedAt, string(encoded), checkedAt, id)
	return err
}

func (s *Store) UpdateConnectionTokens(id, accessToken, refreshToken string, providerSpecificData map[string]any) error {
	if id == "" {
		return errors.New("missing connection id")
	}
	updatedAt := time.Now().UTC()
	psd, _ := json.Marshal(providerSpecificData)
	_, err := s.db.Exec(`UPDATE provider_connections SET access_token = ?, refresh_token = ?, provider_specific_data = ?, updated_at = ? WHERE id = ?`, accessToken, refreshToken, string(psd), updatedAt, id)
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

func (s *Store) ListAPIKeys() ([]APIKey, error) {
	rows, err := s.db.Query(`SELECT id, name, key, allowed_models, is_active, created_at, updated_at FROM api_keys ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIKey
	for rows.Next() {
		key, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, key)
	}
	return out, rows.Err()
}

func (s *Store) GetAPIKey(value string) (APIKey, error) {
	row := s.db.QueryRow(`SELECT id, name, key, allowed_models, is_active, created_at, updated_at FROM api_keys WHERE key = ? AND is_active = 1`, value)
	return scanAPIKey(row)
}

func (s *Store) UpsertAPIKey(key APIKey) error {
	if key.ID == "" || key.Name == "" || key.Key == "" {
		return errors.New("missing api key id, name, or key")
	}
	now := time.Now().UTC()
	if key.CreatedAt.IsZero() {
		key.CreatedAt = now
	}
	key.UpdatedAt = now
	allowed, _ := json.Marshal(key.AllowedModels)
	_, err := s.db.Exec(`INSERT INTO api_keys(id, name, key, allowed_models, is_active, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, allowed_models=excluded.allowed_models, is_active=excluded.is_active, updated_at=excluded.updated_at`,
		key.ID, key.Name, key.Key, string(allowed), boolInt(key.IsActive), key.CreatedAt, key.UpdatedAt)
	return err
}

func (s *Store) DeleteAPIKey(id string) error {
	res, err := s.db.Exec(`DELETE FROM api_keys WHERE id = ?`, id)
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

func connectionSelect() string {
	return `SELECT id, provider, name, email, display_name, auth_type, is_active, priority, default_model, rate_limit_protection, access_token, refresh_token, api_key, project_id, quota_status, quota_detail, quota_reset_at, quota_checked_at, quota_data, provider_specific_data, created_at, updated_at`
}

func scanAPIKey(row scanner) (APIKey, error) {
	var key APIKey
	var allowed string
	var active int
	if err := row.Scan(&key.ID, &key.Name, &key.Key, &allowed, &active, &key.CreatedAt, &key.UpdatedAt); err != nil {
		return APIKey{}, err
	}
	key.IsActive = active != 0
	if err := json.Unmarshal([]byte(allowed), &key.AllowedModels); err != nil {
		return APIKey{}, fmt.Errorf("decode allowed models: %w", err)
	}
	if key.AllowedModels == nil {
		key.AllowedModels = []string{}
	}
	return key, nil
}

func scanConnection(row scanner) (ProviderConnection, error) {
	var c ProviderConnection
	var psd, quotaData string
	var quotaResetAt, quotaCheckedAt sql.NullTime
	var active, rateLimitProtection int
	if err := row.Scan(&c.ID, &c.Provider, &c.Name, &c.Email, &c.DisplayName, &c.AuthType, &active, &c.Priority, &c.DefaultModel, &rateLimitProtection, &c.AccessToken, &c.RefreshToken, &c.APIKey, &c.ProjectID, &c.QuotaStatus, &c.QuotaDetail, &quotaResetAt, &quotaCheckedAt, &quotaData, &psd, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return ProviderConnection{}, err
	}
	c.IsActive = active != 0
	c.RateLimitProtection = rateLimitProtection != 0
	if quotaResetAt.Valid {
		c.QuotaResetAt = &quotaResetAt.Time
	}
	if quotaCheckedAt.Valid {
		c.QuotaCheckedAt = &quotaCheckedAt.Time
	}
	if err := json.Unmarshal([]byte(quotaData), &c.QuotaData); err != nil {
		return ProviderConnection{}, fmt.Errorf("decode quota data: %w", err)
	}
	if c.QuotaData == nil {
		c.QuotaData = map[string]any{}
	}
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

package repository

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"malaxis-fleet/internal/config"
	"malaxis-fleet/internal/domain"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// postgresRepository implements the Repository interface for PostgreSQL.
type postgresRepository struct {
	db *sql.DB
}

// NewRepository creates a new repository instance for PostgreSQL.
// Implements a retry backoff loop to handle SQLSTATE 57P03 (cannot_connect_now)
// which occurs when the PostgreSQL container is still initializing.
func NewRepository(cfg *config.Config) (Repository, error) {
	connString := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		cfg.PostgresHost, cfg.PostgresPort, cfg.PostgresUser, cfg.PostgresPassword, cfg.PostgresDB)

	db, err := sql.Open("pgx", connString)
	if err != nil {
		return nil, fmt.Errorf("failed to open postgres connection: %w", err)
	}

	// Connection pool tuning: cap concurrent connections and idle retention so
	// the master keeps a modest pool under burst load (node polling storms,
	// mass subscription refreshes) without exhausting container resources.
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Retry loop: up to 10 attempts with 2-second sleep intervals
	maxRetries := 10
	retryInterval := 2 * time.Second
	for i := 0; i < maxRetries; i++ {
		if err := db.Ping(); err != nil {
			log.Printf("PostgreSQL not ready (attempt %d/%d): %v", i+1, maxRetries, err)
			if i < maxRetries-1 {
				log.Printf("Retrying in %v...", retryInterval)
				time.Sleep(retryInterval)
			}
			continue
		}
		log.Println("PostgreSQL connection established successfully")
		return &postgresRepository{db: db}, nil
	}

	return nil, fmt.Errorf("failed to connect to postgres database after %d retries", maxRetries)
}

// Init creates the necessary tables if they don't exist.
func (r *postgresRepository) Init() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id SERIAL PRIMARY KEY,
			username VARCHAR(255) UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			role VARCHAR(50) NOT NULL,
			created_at TIMESTAMP NOT NULL,
			color_hex VARCHAR(7) DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS nodes (
			id TEXT PRIMARY KEY,
			name TEXT,
			hostname TEXT,
			device_type TEXT,
			ip_lan TEXT,
			sub_url TEXT,
			active_server TEXT,
			active_engine TEXT,
			active_proto TEXT,
			active_ip_ext TEXT,
			active_outbound_json TEXT,
			available_servers TEXT NOT NULL DEFAULT '[]',
			last_seen TIMESTAMP,
			pending_command TEXT,
			pending_msg_id BIGINT,
			pipeline_status TEXT,
			status_message TEXT,
			user_id BIGINT REFERENCES users(id) ON DELETE SET NULL
		)`,
		`CREATE TABLE IF NOT EXISTS roles (
			id SERIAL PRIMARY KEY,
			name VARCHAR(255) UNIQUE NOT NULL,
			color_hex VARCHAR(7) NOT NULL DEFAULT '#6B7280',
			owner_id TEXT,
			permissions_json TEXT NOT NULL DEFAULT '{}',
			rank INT NOT NULL DEFAULT 10,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS audit_logs (
			id SERIAL PRIMARY KEY,
			timestamp TIMESTAMP NOT NULL,
			actor_username VARCHAR(255) NOT NULL,
			action TEXT NOT NULL,
			target_device TEXT,
			details TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS subscription_providers (
			domain TEXT PRIMARY KEY,
			name TEXT NOT NULL
		)`,
	}

	for _, query := range queries {
		if _, err := r.db.Exec(query); err != nil {
			return fmt.Errorf("failed to execute schema query: %w", err)
		}
	}

	// Idempotent migrations. Every statement is error-checked and logged with
	// its SQL so schema drift can never fail silently.
	type migration struct {
		query string
		args  []interface{}
	}
	migrations := []migration{
		// Migrate: Add color_hex column if it doesn't exist (idempotent)
		{query: `ALTER TABLE users ADD COLUMN IF NOT EXISTS color_hex VARCHAR(7) DEFAULT ''`},
		// Migrate: User personalization columns (accent color, theme, language, bot emoji rendering)
		{query: `ALTER TABLE users ADD COLUMN IF NOT EXISTS accent_color VARCHAR(20) NOT NULL DEFAULT 'indigo'`},
		{query: `ALTER TABLE users ADD COLUMN IF NOT EXISTS theme_mode VARCHAR(20) NOT NULL DEFAULT 'obsidian'`},
		{query: `ALTER TABLE users ADD COLUMN IF NOT EXISTS language VARCHAR(5) NOT NULL DEFAULT 'ru'`},
		{query: `ALTER TABLE users ADD COLUMN IF NOT EXISTS bot_emojis_enabled BOOLEAN NOT NULL DEFAULT TRUE`},
		{query: `ALTER TABLE users ADD COLUMN IF NOT EXISTS blur_enabled BOOLEAN NOT NULL DEFAULT TRUE`},
		// Migrate: Add user_id column if it doesn't exist
		{query: `ALTER TABLE nodes ADD COLUMN IF NOT EXISTS user_id BIGINT REFERENCES users(id) ON DELETE SET NULL`},
		// Migrate: Add available_servers column if it doesn't exist
		{query: `ALTER TABLE nodes ADD COLUMN IF NOT EXISTS available_servers TEXT NOT NULL DEFAULT '[]'`},
		// Migrate: Add hardware_hash column for hardware fingerprint dedup
		{query: `ALTER TABLE nodes ADD COLUMN IF NOT EXISTS hardware_hash TEXT`},
		{query: `CREATE INDEX IF NOT EXISTS idx_nodes_hardware_hash ON nodes(hardware_hash)`},
		// Migrate: Multi-subscription support - sub_urls JSONB array replaces
		// the single sub_url TEXT column (kept as a legacy mirror of the first
		// entry for backwards compatibility).
		{query: `ALTER TABLE nodes ADD COLUMN IF NOT EXISTS sub_urls JSONB NOT NULL DEFAULT '[]'`},
		// Backfill: copy any legacy single sub_url into the new array exactly
		// once (guarded by the empty-array check, so it is idempotent).
		{query: `UPDATE nodes SET sub_urls = to_jsonb(ARRAY[sub_url]) WHERE (sub_url IS NOT NULL AND sub_url != '') AND sub_urls = '[]'::jsonb`},
		// Mirror the first array entry back into the legacy sub_url column so
		// any remaining sub_url consumers keep working after upgrades.
		{query: `UPDATE nodes SET sub_url = sub_urls->>0 WHERE jsonb_array_length(sub_urls) > 0 AND COALESCE(sub_url, '') = ''`},
		// Migrate: server_providers column - agent-reported map of server name
		// -> provider name used for grouping in the web UI, bot and CLI.
		{query: `ALTER TABLE nodes ADD COLUMN IF NOT EXISTS server_providers TEXT`},
		// Migrate: Add node_logs column (JSON map of container -> last log tail)
		{query: `ALTER TABLE nodes ADD COLUMN IF NOT EXISTS node_logs TEXT`},
		// Migrate: Set default value for device_type and update existing NULL values
		{query: `ALTER TABLE nodes ALTER COLUMN device_type SET DEFAULT 'node'`},
		{query: `UPDATE nodes SET device_type = 'node' WHERE device_type IS NULL`},
		// Migrate: Ensure roles table has all required columns (idempotent for upgrades)
		{query: `ALTER TABLE roles ADD COLUMN IF NOT EXISTS permissions_json TEXT NOT NULL DEFAULT '{}'`},
		{query: `ALTER TABLE roles ALTER COLUMN permissions_json SET DEFAULT '{}'`},
		{query: `ALTER TABLE roles ALTER COLUMN color_hex TYPE VARCHAR(7)`},
		{query: `ALTER TABLE roles ALTER COLUMN color_hex SET DEFAULT '#6B7280'`},
		{query: `ALTER TABLE roles ALTER COLUMN name TYPE VARCHAR(255)`},
		{query: `ALTER TABLE roles DROP CONSTRAINT IF EXISTS roles_owner_id_fkey`},
		{query: `ALTER TABLE roles ALTER COLUMN owner_id TYPE TEXT`},
		{query: `ALTER TABLE roles ALTER COLUMN owner_id DROP NOT NULL`},
		// Migrate: Add configurable rank column (int hierarchy) if it doesn't exist
		{query: `ALTER TABLE roles ADD COLUMN IF NOT EXISTS rank INT NOT NULL DEFAULT 10`},
		{query: `ALTER TABLE roles ALTER COLUMN rank SET DEFAULT 10`},
		// Backfill ranks for the built-in system roles. Idempotent: only roles
		// found in the DB are touched, so existing deployments keep their data.
		{query: `UPDATE roles SET rank = $1 WHERE name = 'owner'`, args: []interface{}{domain.RoleRankOwner}},
		{query: `UPDATE roles SET rank = $1 WHERE name = 'admin'`, args: []interface{}{domain.RoleRankAdmin}},
		{query: `UPDATE roles SET rank = $1 WHERE name = 'client'`, args: []interface{}{domain.RoleRankClient}},
		{query: `UPDATE roles SET rank = $1 WHERE name = 'viewer'`, args: []interface{}{domain.RoleRankViewer}},
		// Any non-system/custom role that still defaults to the client rank keeps
		// its stored value; only NULL/zero ranks are normalized to the default.
		{query: `UPDATE roles SET rank = 10 WHERE rank IS NULL OR rank < 1 OR rank > 100`},
		// Ensure the built-in system roles exist even on upgraded deployments whose
		// roles table was seeded before the owner/viewer rows were introduced.
		{query: `INSERT INTO roles (name, color_hex, owner_id, permissions_json, rank, created_at)
			SELECT 'admin', '#EF4444', 'system', '{"can_view_nodes":true,"can_switch_vpn":true,"can_edit_sub":true,"can_rename_node":true,"can_terminate_node":true,"can_update_client":true,"can_purge_nodes":true,"can_manage_providers":true,"can_view_users":true,"can_create_users":true,"can_edit_users":true,"can_delete_users":true,"can_view_roles":true,"can_manage_roles":true,"can_view_audit":true,"can_view_node_logs":true,"can_view_master_logs":true,"can_view_audit_logs":true}', 80, NOW()
			WHERE NOT EXISTS (SELECT 1 FROM roles WHERE name = 'admin')`},
		{query: `INSERT INTO roles (name, color_hex, owner_id, permissions_json, rank, created_at)
			SELECT 'client', '#3B82F6', 'system', '{"can_view_nodes":true}', 30, NOW()
			WHERE NOT EXISTS (SELECT 1 FROM roles WHERE name = 'client')`},
		{query: `INSERT INTO roles (name, color_hex, owner_id, permissions_json, rank, created_at)
			SELECT 'owner', '#FF5733', 'system', '{"can_view_nodes":true,"can_switch_vpn":true,"can_edit_sub":true,"can_rename_node":true,"can_terminate_node":true,"can_update_client":true,"can_purge_nodes":true,"can_manage_providers":true,"can_view_users":true,"can_create_users":true,"can_edit_users":true,"can_delete_users":true,"can_view_roles":true,"can_manage_roles":true,"can_view_audit":true,"can_view_node_logs":true,"can_view_master_logs":true,"can_view_audit_logs":true}', 100, NOW()
			WHERE NOT EXISTS (SELECT 1 FROM roles WHERE name = 'owner')`},
		{query: `INSERT INTO roles (name, color_hex, owner_id, permissions_json, rank, created_at)
			SELECT 'viewer', '#6B7280', 'system', '{"can_view_nodes":true}', 10, NOW()
			WHERE NOT EXISTS (SELECT 1 FROM roles WHERE name = 'viewer')`},
		// Migrate: Rename the legacy 'admin' account to 'owner' so existing
		// deployments keep dashboard access under the new default credential
		// (ADMIN_USER=owner). Guarded against a unique-conflict when a user
		// named 'owner' was already created manually.
		{query: `UPDATE users SET username = 'owner' WHERE username = 'admin' AND NOT EXISTS (SELECT 1 FROM users WHERE username = 'owner')`},
		// Migrate: When both an 'admin' and an 'owner' account exist (an old
		// deployment where UpsertAdminUser previously re-seeded 'admin' as an
		// owner), the 'admin' row is a duplicate owner: demote it to the
		// regular admin role so the canonical ADMIN_USER account stays the
		// sole owner and ADMIN_PASS keeps force-syncing.
		{query: `UPDATE users SET role = 'admin' WHERE username = 'admin' AND role = 'owner' AND EXISTS (SELECT 1 FROM users WHERE username = 'owner')`},
		// Seed automated-backup routing defaults (idempotent). Missing values
		// default to local-only storage; both are safe regardless of migration.
		{query: `INSERT INTO settings (key, value, updated_at)
			SELECT 'backup_to_local', 'true', NOW()
			WHERE NOT EXISTS (SELECT 1 FROM settings WHERE key = 'backup_to_local')`},
		{query: `INSERT INTO settings (key, value, updated_at)
			SELECT 'backup_to_telegram', 'false', NOW()
			WHERE NOT EXISTS (SELECT 1 FROM settings WHERE key = 'backup_to_telegram')`},
		{query: `INSERT INTO settings (key, value, updated_at)
			SELECT 'backup_interval_hours', '24', NOW()
			WHERE NOT EXISTS (SELECT 1 FROM settings WHERE key = 'backup_interval_hours')`},
	}
	for _, m := range migrations {
		if _, err := r.db.Exec(m.query, m.args...); err != nil {
			log.Printf("ERROR: migration statement failed: %v\nquery: %s", err, m.query)
		}
	}

	// Migrate: Copy old bot settings keys to new tg_ prefixed keys
	r.migrateBotSettings()

	// Migrate: Ensure the system admin role carries every current permission key,
	// including the granular log permissions introduced for server-side RBAC.
	r.migrateSystemRolePermissions()

	return nil
}

// migrateSystemRolePermissions merges the full permission set into the system
// admin role's permissions_json on every startup. This is the idempotent
// upgrade path for existing deployments whose roles table was seeded before
// granular keys (can_view_node_logs, can_view_master_logs, can_view_audit_logs)
// existed. Only roles owned by "system" (admin/client) are touched.
func (r *postgresRepository) migrateSystemRolePermissions() {
	systemRoles, err := r.GetCustomRolesByOwner("system")
	if err != nil {
		log.Printf("ERROR: failed to load system roles for permission migration: %v", err)
		return
	}

	for _, role := range systemRoles {
		permSet := map[string]bool{}
		// Merge existing permissions (array or map encoding).
		if err := json.Unmarshal([]byte(role.PermissionsJSON), &permSet); err != nil {
			var arr []string
			if err2 := json.Unmarshal([]byte(role.PermissionsJSON), &arr); err2 == nil {
				for _, p := range arr {
					if p != "" {
						permSet[p] = true
					}
				}
			}
		}
		merged := role.PermissionsJSON
		if role.Name == "admin" {
			for _, p := range domain.AllPermissions {
				if !permSet[p] {
					log.Printf("Migrating system role %q: adding permission %s", role.Name, p)
					permSet[p] = true
				}
			}
			b, err := json.Marshal(permSet)
			if err != nil {
				log.Printf("ERROR: failed to encode migrated permissions for role %q: %v", role.Name, err)
				continue
			}
			merged = string(b)
		}
		if merged != role.PermissionsJSON {
			if err := r.UpdateCustomRole(&domain.CustomRole{ID: role.ID, Name: role.Name, ColorHex: role.ColorHex, PermissionsJSON: merged, Rank: role.Rank}); err != nil {
				log.Printf("ERROR: failed to persist migrated permissions for role %q: %v", role.Name, err)
			} else {
				log.Printf("Migrated permissions_json for system role %q", role.Name)
			}
		}
	}
}

func (r *postgresRepository) migrateBotSettings() {
	oldEnabled, err1 := r.GetSetting("bot_enabled")
	oldToken, err2 := r.GetSetting("bot_token")
	oldChatID, err3 := r.GetSetting("bot_chat_id")

	if err1 == nil && oldEnabled != "" {
		newVal, _ := r.GetSetting("tg_bot_enabled")
		if newVal == "" {
			if err := r.SetSetting("tg_bot_enabled", oldEnabled); err != nil {
				log.Printf("ERROR: failed to migrate bot_enabled setting: %v", err)
			}
		}
	}
	if err2 == nil && oldToken != "" {
		newVal, _ := r.GetSetting("tg_bot_token")
		if newVal == "" {
			if err := r.SetSetting("tg_bot_token", oldToken); err != nil {
				log.Printf("ERROR: failed to migrate bot_token setting: %v", err)
			}
		}
	}
	if err3 == nil && oldChatID != "" {
		newVal, _ := r.GetSetting("tg_admin_chat_id")
		if newVal == "" {
			if err := r.SetSetting("tg_admin_chat_id", oldChatID); err != nil {
				log.Printf("ERROR: failed to migrate bot_chat_id setting: %v", err)
			}
		}
	}
}

// Close closes the database connection.
func (r *postgresRepository) Close() error {
	return r.db.Close()
}

// --- Node Methods ---

// unmarshalAvailableServers parses the stored JSON value of available servers.
// v1.2.2 reports a grouped object {provider: [server, ...]}; values written by
// older agents are flat arrays and are normalized to a single provider-less
// group. Logs (never silently ignores) a corrupted value.
func unmarshalAvailableServers(raw string) map[string][]string {
	out := map[string][]string{}
	if raw == "" || raw == "null" {
		return out
	}
	var grouped map[string][]string
	if err := json.Unmarshal([]byte(raw), &grouped); err == nil {
		return grouped
	}
	var flat []string
	if err := json.Unmarshal([]byte(raw), &flat); err == nil && len(flat) > 0 {
		out[""] = flat
		return out
	}
	if err := json.Unmarshal([]byte(raw), &flat); err != nil {
		log.Printf("ERROR: invalid available_servers JSON %q: %v", raw, err)
	}
	return out
}

func marshalAvailableServers(m map[string][]string) string {
	if len(m) == 0 {
		return "{}"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func unmarshalSubURLs(raw string) []string {
	var out []string
	if raw == "" || raw == "null" {
		return out
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		// Pre-migration race: a bare string stored instead of a JSON array.
		var single string
		if json.Unmarshal([]byte(raw), &single) == nil && single != "" {
			out = []string{single}
		}
	}
	return out
}

func unmarshalServerProviders(raw string) map[string]string {
	out := map[string]string{}
	if raw == "" || raw == "null" {
		return out
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		log.Printf("ERROR: invalid server_providers JSON %q: %v", raw, err)
	}
	return out
}

func marshalSubURLs(subURLs []string) string {
	if len(subURLs) == 0 {
		return "[]"
	}
	b, err := json.Marshal(subURLs)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func marshalServerProviders(m map[string]string) string {
	if len(m) == 0 {
		return "{}"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func firstSubURL(subURLs []string) string {
	if len(subURLs) > 0 {
		return subURLs[0]
	}
	return ""
}

func (r *postgresRepository) GetNodeByID(id string) (*domain.Node, error) {
	var n domain.Node
	var availRaw, subURLsRaw, providersRaw string
	query := `SELECT id, name, hostname, COALESCE(device_type, 'node'), COALESCE(ip_lan, ''), COALESCE(sub_urls::text, '[]'), COALESCE(active_server, ''), COALESCE(active_engine, ''), COALESCE(active_proto, ''), COALESCE(active_ip_ext, ''), COALESCE(active_outbound_json, ''), COALESCE(available_servers, '[]'), COALESCE(server_providers, '{}'), COALESCE(last_seen, '1970-01-01 00:00:00'), COALESCE(pending_command, ''), COALESCE(pending_msg_id, 0), COALESCE(pipeline_status, ''), COALESCE(status_message, ''), user_id FROM nodes WHERE id = $1`
	err := r.db.QueryRow(query, id).Scan(&n.ID, &n.Name, &n.Hostname, &n.DeviceType, &n.IPLan, &subURLsRaw, &n.ActiveServer, &n.ActiveEngine, &n.ActiveProto, &n.ActiveIPExt, &n.ActiveOutboundJSON, &availRaw, &providersRaw, &n.LastSeen, &n.PendingCommand, &n.PendingMsgID, &n.PipelineStatus, &n.StatusMessage, &n.UserID)
	if err != nil {
		return nil, err
	}
	n.AvailableServers = unmarshalAvailableServers(availRaw)
	n.SubURLs = unmarshalSubURLs(subURLsRaw)
	n.ServerProviders = unmarshalServerProviders(providersRaw)
	return &n, nil
}

func (r *postgresRepository) GetAllNodes() ([]domain.Node, error) {
	rows, err := r.db.Query(`SELECT id, name, hostname, COALESCE(device_type, 'node'), COALESCE(ip_lan, ''), COALESCE(sub_urls::text, '[]'), COALESCE(active_server, ''), COALESCE(active_engine, ''), COALESCE(active_proto, ''), COALESCE(active_ip_ext, ''), COALESCE(active_outbound_json, ''), COALESCE(available_servers, '[]'), COALESCE(server_providers, '{}'), COALESCE(last_seen, '1970-01-01 00:00:00'), COALESCE(pending_command, ''), COALESCE(pending_msg_id, 0), COALESCE(pipeline_status, ''), COALESCE(status_message, ''), user_id FROM nodes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []domain.Node
	for rows.Next() {
		var n domain.Node
		var availRaw, subURLsRaw, providersRaw string
		if err := rows.Scan(&n.ID, &n.Name, &n.Hostname, &n.DeviceType, &n.IPLan, &subURLsRaw, &n.ActiveServer, &n.ActiveEngine, &n.ActiveProto, &n.ActiveIPExt, &n.ActiveOutboundJSON, &availRaw, &providersRaw, &n.LastSeen, &n.PendingCommand, &n.PendingMsgID, &n.PipelineStatus, &n.StatusMessage, &n.UserID); err != nil {
			return nil, err
		}
		n.AvailableServers = unmarshalAvailableServers(availRaw)
		n.SubURLs = unmarshalSubURLs(subURLsRaw)
		n.ServerProviders = unmarshalServerProviders(providersRaw)
		nodes = append(nodes, n)
	}
	return nodes, nil
}

func (r *postgresRepository) GetNodesByUserID(userID int64) ([]domain.Node, error) {
	rows, err := r.db.Query(`SELECT id, name, hostname, COALESCE(device_type, 'node'), COALESCE(ip_lan, ''), COALESCE(sub_urls::text, '[]'), COALESCE(active_server, ''), COALESCE(active_engine, ''), COALESCE(active_proto, ''), COALESCE(active_ip_ext, ''), COALESCE(active_outbound_json, ''), COALESCE(available_servers, '[]'), COALESCE(server_providers, '{}'), COALESCE(last_seen, '1970-01-01 00:00:00'), COALESCE(pending_command, ''), COALESCE(pending_msg_id, 0), COALESCE(pipeline_status, ''), COALESCE(status_message, ''), user_id FROM nodes WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []domain.Node
	for rows.Next() {
		var n domain.Node
		var availRaw, subURLsRaw, providersRaw string
		if err := rows.Scan(&n.ID, &n.Name, &n.Hostname, &n.DeviceType, &n.IPLan, &subURLsRaw, &n.ActiveServer, &n.ActiveEngine, &n.ActiveProto, &n.ActiveIPExt, &n.ActiveOutboundJSON, &availRaw, &providersRaw, &n.LastSeen, &n.PendingCommand, &n.PendingMsgID, &n.PipelineStatus, &n.StatusMessage, &n.UserID); err != nil {
			return nil, err
		}
		n.AvailableServers = unmarshalAvailableServers(availRaw)
		n.SubURLs = unmarshalSubURLs(subURLsRaw)
		n.ServerProviders = unmarshalServerProviders(providersRaw)
		nodes = append(nodes, n)
	}
	return nodes, nil
}

func (r *postgresRepository) AddNode(node *domain.Node) error {
	query := `INSERT INTO nodes (id, name, hostname, ip_lan, sub_url, sub_urls, last_seen, user_id) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	subURLsJSON := marshalSubURLs(node.SubURLs)
	_, err := r.db.Exec(query, node.ID, node.Name, node.Hostname, node.IPLan, firstSubURL(node.SubURLs), subURLsJSON, time.Now(), node.UserID)
	return err
}

func (r *postgresRepository) UpsertNode(node *domain.Node) (string, bool, error) {
	// Dedup ghost nodes: if another node with the same hostname exists under a
	// different id (reinstall creates a fresh node_id), remove the stale one —
	// but only if it has been offline for a while, so a live duplicate is kept.
	if node.Hostname != "" {
		if _, err := r.db.Exec(`DELETE FROM nodes WHERE hostname = $1 AND id != $2 AND last_seen < NOW() - interval '10 minutes'`, node.Hostname, node.ID); err != nil {
			log.Printf("ERROR: ghost-node cleanup failed for hostname %q: %v", node.Hostname, err)
		}
	}

	// Hardware fingerprint dedup: a reinstalled node (fresh node_id.txt) whose
	// id no longer exists in the DB is adopted back to its original row via the
	// immutable hardware_hash, so the device keeps its name/history.
	if node.HardwareHash != "" {
		var existingID string
		err := r.db.QueryRow(`SELECT id FROM nodes WHERE hardware_hash = $1 AND id != $2 LIMIT 1`, node.HardwareHash, node.ID).Scan(&existingID)
		if err == nil && existingID != "" {
			node.ID = existingID
		}
	}

	// Detect whether this is a genuinely new device FIRST time we've ever seen
	// it. Hardware-hash adoption above collapses reinstalls onto their original
	// row, so a reconnecting device is NOT reported as new.
	var isNew bool
	err := r.db.QueryRow(`SELECT NOT EXISTS (SELECT 1 FROM nodes WHERE id = $1)`, node.ID).Scan(&isNew)
	if err != nil {
		return node.ID, false, err
	}

	query := `INSERT INTO nodes (id, name, hostname, ip_lan, sub_url, sub_urls, hardware_hash, last_seen)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		ON CONFLICT (id) DO UPDATE SET
			last_seen = NOW(),
			ip_lan = EXCLUDED.ip_lan,
			sub_urls = nodes.sub_urls,
			sub_url = nodes.sub_url,
			name = CASE
				WHEN nodes.name IS NULL OR nodes.name = '' OR nodes.name = nodes.hostname THEN EXCLUDED.name
				ELSE nodes.name
			END,
			hostname = COALESCE(EXCLUDED.hostname, nodes.hostname),
			hardware_hash = COALESCE(EXCLUDED.hardware_hash, nodes.hardware_hash)`
	_, err = r.db.Exec(query, node.ID, node.Name, node.Hostname, node.IPLan, firstSubURL(node.SubURLs), marshalSubURLs(node.SubURLs), node.HardwareHash)
	return node.ID, isNew, err
}

func (r *postgresRepository) RenameNode(id, name string) error {
	_, err := r.db.Exec("UPDATE nodes SET name = $1 WHERE id = $2", name, id)
	return err
}

func (r *postgresRepository) UpdateNodeNameIfUnset(id, name string) error {
	_, err := r.db.Exec(
		`UPDATE nodes SET name = $1
		 WHERE id = $2
		   AND (name IS NULL OR name = '' OR name = hostname OR name = $1)`,
		name, id)
	return err
}

func (r *postgresRepository) SetNodeHardwareHash(id, hardwareHash string) error {
	_, err := r.db.Exec("UPDATE nodes SET hardware_hash = $1 WHERE id = $2", hardwareHash, id)
	return err
}

func (r *postgresRepository) UpdateNode(node *domain.Node) error {
	query := `UPDATE nodes SET name = $1, hostname = $2, ip_lan = $3, sub_urls = $4, sub_url = CASE WHEN jsonb_array_length($4) > 0 THEN $4->>0 ELSE sub_url END, user_id = $5 WHERE id = $6`
	_, err := r.db.Exec(query, node.Name, node.Hostname, node.IPLan, marshalSubURLs(node.SubURLs), node.UserID, node.ID)
	return err
}

func (r *postgresRepository) DeleteNode(id string) error {
	_, err := r.db.Exec("DELETE FROM nodes WHERE id = $1", id)
	return err
}

func (r *postgresRepository) DeleteOfflineNodes(thresholdDays int) (int64, error) {
	// Also cleans Terminated nodes and any rows with a NULL last_seen.
	res, err := r.db.Exec(`DELETE FROM nodes WHERE COALESCE(last_seen, '1970-01-01') < NOW() - ($1::int * interval '1 day')`, thresholdDays)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (r *postgresRepository) UpdateNodeStatus(id, ipLan string) error {
	query := `UPDATE nodes SET ip_lan = $1, last_seen = $2 WHERE id = $3`
	_, err := r.db.Exec(query, ipLan, time.Now(), id)
	return err
}

// SetNodeLogs merges a JSON map of container -> log tail into the node's stored
// logs. Each agent report only carries the container it just fetched, so a naive
// overwrite would wipe the other containers' entries (node-agent polls would
// erase xray-node/singbox-node logs and vice versa).
func (r *postgresRepository) SetNodeLogs(id, logsJSON string) error {
	existing := map[string]string{}
	if raw, err := r.GetNodeLogs(id); err == nil && raw != "" {
		if err := json.Unmarshal([]byte(raw), &existing); err != nil {
			log.Printf("ERROR: stored node_logs for %q is invalid JSON, resetting: %v", id, err)
			existing = map[string]string{}
		}
	}
	incoming := map[string]string{}
	if err := json.Unmarshal([]byte(logsJSON), &incoming); err != nil {
		incoming = map[string]string{"raw": logsJSON}
	}
	for k, v := range incoming {
		existing[k] = v
	}
	merged, err := json.Marshal(existing)
	if err != nil {
		return err
	}
	_, err = r.db.Exec("UPDATE nodes SET node_logs = $1 WHERE id = $2", string(merged), id)
	return err
}

// GetNodeLogs returns the stored JSON map of container -> log tail for a node.
func (r *postgresRepository) GetNodeLogs(id string) (string, error) {
	var raw string
	err := r.db.QueryRow("SELECT COALESCE(node_logs, '') FROM nodes WHERE id = $1", id).Scan(&raw)
	if err != nil {
		return "", err
	}
	return raw, nil
}

func (r *postgresRepository) UpdateNodeReport(id, ipExt, engine, proto, outboundJSON, activeServer, availableServers string, subURLs []string, serverProviders map[string]string) error {
	query := `UPDATE nodes SET active_ip_ext = $1, active_engine = $2, active_proto = $3, active_outbound_json = $4, active_server = $5, available_servers = $6,
		sub_urls = COALESCE(NULLIF($8::jsonb, '[]'::jsonb), sub_urls),
		server_providers = COALESCE(NULLIF($9::text, ''), server_providers),
		sub_url = CASE WHEN jsonb_array_length(sub_urls) > 0 THEN sub_urls->>0 ELSE sub_url END
		WHERE id = $7`
	_, err := r.db.Exec(query, ipExt, engine, proto, outboundJSON, activeServer, availableServers, id, marshalSubURLs(subURLs), marshalServerProviders(serverProviders))
	return err
}

func (r *postgresRepository) UpdateNodePipelineStatus(nodeID, status, message string) error {
	query := `UPDATE nodes SET pipeline_status = $1, status_message = $2 WHERE id = $3`
	_, err := r.db.Exec(query, status, message, nodeID)
	return err
}

func (r *postgresRepository) GetNodesWithSubURL() ([]domain.Node, error) {
	rows, err := r.db.Query(`SELECT id, name, COALESCE(sub_urls::text, '[]'), COALESCE(active_server, ''), COALESCE(active_outbound_json, '') FROM nodes WHERE jsonb_array_length(sub_urls) > 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []domain.Node
	for rows.Next() {
		var n domain.Node
		var subURLsRaw string
		if err := rows.Scan(&n.ID, &n.Name, &subURLsRaw, &n.ActiveServer, &n.ActiveOutboundJSON); err != nil {
			return nil, err
		}
		n.SubURLs = unmarshalSubURLs(subURLsRaw)
		nodes = append(nodes, n)
	}
	return nodes, nil
}

func (r *postgresRepository) AssignNodeToUser(nodeID string, userID int64) error {
	_, err := r.db.Exec("UPDATE nodes SET user_id = $1 WHERE id = $2", userID, nodeID)
	return err
}

// UpdateAllNodesSubURLs replaces the subscription URLs for ALL nodes.
func (r *postgresRepository) UpdateAllNodesSubURLs(subURLs []string) error {
	_, err := r.db.Exec(`UPDATE nodes SET sub_urls = $1::jsonb, sub_url = CASE WHEN jsonb_array_length($1::jsonb) > 0 THEN $1::jsonb->>0 ELSE '' END`, marshalSubURLs(subURLs))
	if err != nil {
		return err
	}
	// Provider auto-discovery after the URLs actually changed.
	if err := r.SyncProviders(); err != nil {
		log.Printf("ERROR: provider sync after mass sub URL update failed: %v", err)
	}
	return nil
}

// --- Command Methods ---

func (r *postgresRepository) GetPendingCommand(nodeID string) (string, error) {
	var pendingCommand sql.NullString
	query := "SELECT pending_command FROM nodes WHERE id = $1"
	err := r.db.QueryRow(query, nodeID).Scan(&pendingCommand)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return pendingCommand.String, nil
}

func (r *postgresRepository) SetPendingCommand(nodeID, command string, messageID int64) error {
	res, err := r.db.Exec("UPDATE nodes SET pending_command = $1, pending_msg_id = $2, pipeline_status = 'Queued', status_message = '' WHERE id = $3", command, messageID, nodeID)
	if err != nil {
		return err
	}
	// Command-flow consistency: never silently queue a command for a node that
	// does not exist in the DB (the single source of truth).
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNodeNotFound
	}
	return nil
}

func (r *postgresRepository) UpdateNodeSubURLsAndQueue(nodeID string, subURLs []string, command string, messageID int64) error {
	// Single atomic UPDATE: saving the sub_urls and queuing the update_sub
	// command must succeed or fail together, otherwise a sub_urls change
	// could be persisted without ever triggering the agent to fetch it.
	res, err := r.db.Exec(`UPDATE nodes SET sub_urls = $1::jsonb,
		sub_url = CASE WHEN jsonb_array_length($1::jsonb) > 0 THEN $1::jsonb->>0 ELSE '' END,
		pending_command = $2, pending_msg_id = $3, pipeline_status = 'Queued', status_message = '' WHERE id = $4`,
		marshalSubURLs(subURLs), command, messageID, nodeID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNodeNotFound
	}
	// Provider auto-discovery after the node's URLs actually changed.
	if err := r.SyncProviders(); err != nil {
		log.Printf("ERROR: provider sync after sub URL update failed: %v", err)
	}
	return nil
}

func (r *postgresRepository) ClearPendingCommand(nodeID string) error {
	query := "UPDATE nodes SET pending_command = NULL, pending_msg_id = 0 WHERE id = $1"
	_, err := r.db.Exec(query, nodeID)
	return err
}

// --- User Methods ---

func (r *postgresRepository) GetUserByUsername(username string) (*domain.User, error) {
	var u domain.User
	query := `SELECT u.id, u.username, u.password_hash, u.role, u.created_at, COALESCE(u.color_hex, ''),
		COALESCE(r.name, u.role) AS role_name, COALESCE(r.color_hex, u.color_hex, '') AS role_color
		FROM users u LEFT JOIN roles r ON r.name = u.role WHERE u.username = $1`
	err := r.db.QueryRow(query, username).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt, &u.ColorHex, &u.RoleName, &u.RoleColor)
	return &u, err
}

func (r *postgresRepository) GetUserByID(id int64) (*domain.User, error) {
	var u domain.User
	query := `SELECT u.id, u.username, u.password_hash, u.role, u.created_at, COALESCE(u.color_hex, ''),
		COALESCE(r.name, u.role) AS role_name, COALESCE(r.color_hex, u.color_hex, '') AS role_color
		FROM users u LEFT JOIN roles r ON r.name = u.role WHERE u.id = $1`
	err := r.db.QueryRow(query, id).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt, &u.ColorHex, &u.RoleName, &u.RoleColor)
	return &u, err
}

func (r *postgresRepository) GetAllUsers() ([]domain.User, error) {
	query := `SELECT u.id, u.username, u.role, u.created_at, COALESCE(u.color_hex, ''),
		COALESCE(r.name, u.role) AS role_name,
		COALESCE(r.color_hex, u.color_hex, '') AS role_color
		FROM users u
		LEFT JOIN roles r ON r.name = u.role
		ORDER BY u.username`
	rows, err := r.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []domain.User
	for rows.Next() {
		var u domain.User
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt, &u.ColorHex, &u.RoleName, &u.RoleColor); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, nil
}

func (r *postgresRepository) AddUser(user *domain.User) (int64, error) {
	query := `INSERT INTO users (username, password_hash, role, created_at, color_hex) VALUES ($1, $2, $3, $4, $5) RETURNING id`
	var id int64
	err := r.db.QueryRow(query, user.Username, user.PasswordHash, user.Role, user.CreatedAt, user.ColorHex).Scan(&id)
	return id, err
}

// UpsertAdminUser creates the admin user if missing and ALWAYS overwrites its
// password hash (and owner role) on every startup. There is no "IF NOT EXISTS"
// skip path: an existing stale hash can never lock the dashboard out.
func (r *postgresRepository) UpsertAdminUser(username, passwordHash string) error {
	query := `INSERT INTO users (username, password_hash, role, created_at, color_hex)
		VALUES ($1, $2, 'owner', NOW(), '#FF5733')
		ON CONFLICT (username) DO UPDATE SET
			password_hash = EXCLUDED.password_hash,
			role = 'owner',
			color_hex = COALESCE(users.color_hex, '#FF5733')`
	_, err := r.db.Exec(query, username, passwordHash)
	return err
}

func (r *postgresRepository) UpdateUser(user *domain.User) error {
	query := "UPDATE users SET role = $1, color_hex = $2 WHERE id = $3"
	_, err := r.db.Exec(query, user.Role, user.ColorHex, user.ID)
	return err
}

func (r *postgresRepository) UpdateUserPassword(id int64, passwordHash string) error {
	_, err := r.db.Exec("UPDATE users SET password_hash = $1 WHERE id = $2", passwordHash, id)
	return err
}

// UpdateUserUsername renames a user account. The unique constraint on
// username is enforced by the database; a conflicting rename returns an error.
func (r *postgresRepository) UpdateUserUsername(id int64, username string) error {
	_, err := r.db.Exec("UPDATE users SET username = $1 WHERE id = $2", username, id)
	return err
}

func (r *postgresRepository) DeleteUser(id int64) error {
	_, err := r.db.Exec("DELETE FROM users WHERE id = $1", id)
	return err
}

func (r *postgresRepository) GetUserPreferences(id int64) (*domain.UserPreferences, error) {
	var p domain.UserPreferences
	err := r.db.QueryRow(`SELECT accent_color, theme_mode, language, bot_emojis_enabled, blur_enabled FROM users WHERE id = $1`, id).
		Scan(&p.AccentColor, &p.ThemeMode, &p.Language, &p.BotEmojisEnabled, &p.BlurEnabled)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *postgresRepository) UpdateUserPreferences(id int64, p domain.UserPreferences) error {
	_, err := r.db.Exec(`UPDATE users SET accent_color = $1, theme_mode = $2, language = $3, bot_emojis_enabled = $4, blur_enabled = $5 WHERE id = $6`,
		p.AccentColor, p.ThemeMode, p.Language, p.BotEmojisEnabled, p.BlurEnabled, id)
	return err
}

func (r *postgresRepository) IsUsersEmpty() (bool, error) {
	var count int
	err := r.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	return count == 0, err
}

func (r *postgresRepository) CountUsersByRole(role string) (int, error) {
	var count int
	err := r.db.QueryRow("SELECT COUNT(*) FROM users WHERE role = $1", role).Scan(&count)
	return count, err
}

// --- Custom Role Methods ---

func (r *postgresRepository) AddCustomRole(role *domain.CustomRole) (int64, error) {
	var id int64
	query := "INSERT INTO roles (name, color_hex, owner_id, permissions_json, rank, created_at) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id"
	err := r.db.QueryRow(query, role.Name, role.ColorHex, role.OwnerID, role.PermissionsJSON, role.Rank, role.CreatedAt).Scan(&id)
	return id, err
}

func (r *postgresRepository) GetCustomRolesByOwner(ownerID string) ([]domain.CustomRole, error) {
	rows, err := r.db.Query("SELECT id, name, color_hex, owner_id, permissions_json, rank, created_at FROM roles WHERE owner_id = $1 ORDER BY name", ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var roles []domain.CustomRole
	for rows.Next() {
		var c domain.CustomRole
		if err := rows.Scan(&c.ID, &c.Name, &c.ColorHex, &c.OwnerID, &c.PermissionsJSON, &c.Rank, &c.CreatedAt); err != nil {
			return nil, err
		}
		roles = append(roles, c)
	}
	return roles, nil
}

func (r *postgresRepository) GetRoleByName(name string) (*domain.CustomRole, error) {
	var c domain.CustomRole
	query := "SELECT id, name, color_hex, COALESCE(owner_id, ''), permissions_json, rank, created_at FROM roles WHERE name = $1"
	err := r.db.QueryRow(query, name).Scan(&c.ID, &c.Name, &c.ColorHex, &c.OwnerID, &c.PermissionsJSON, &c.Rank, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *postgresRepository) GetAllRoles() ([]domain.CustomRole, error) {
	rows, err := r.db.Query("SELECT id, name, color_hex, COALESCE(owner_id, ''), permissions_json, rank, created_at FROM roles ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var roles []domain.CustomRole
	for rows.Next() {
		var c domain.CustomRole
		if err := rows.Scan(&c.ID, &c.Name, &c.ColorHex, &c.OwnerID, &c.PermissionsJSON, &c.Rank, &c.CreatedAt); err != nil {
			return nil, err
		}
		roles = append(roles, c)
	}
	return roles, nil
}

func (r *postgresRepository) GetRoleByID(id int64) (*domain.CustomRole, error) {
	var c domain.CustomRole
	query := "SELECT id, name, color_hex, COALESCE(owner_id, ''), permissions_json, rank, created_at FROM roles WHERE id = $1"
	err := r.db.QueryRow(query, id).Scan(&c.ID, &c.Name, &c.ColorHex, &c.OwnerID, &c.PermissionsJSON, &c.Rank, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *postgresRepository) UpdateCustomRole(role *domain.CustomRole) error {
	query := "UPDATE roles SET name = $1, color_hex = $2, permissions_json = $3, rank = $4 WHERE id = $5"
	_, err := r.db.Exec(query, role.Name, role.ColorHex, role.PermissionsJSON, role.Rank, role.ID)
	return err
}

func (r *postgresRepository) DeleteCustomRole(id int64) error {
	_, err := r.db.Exec("DELETE FROM roles WHERE id = $1", id)
	return err
}

func (r *postgresRepository) CountUsersByRoleName(roleName string) (int, error) {
	var count int
	err := r.db.QueryRow("SELECT COUNT(*) FROM users WHERE role = $1", roleName).Scan(&count)
	return count, err
}

// --- Audit Log Methods ---

func (r *postgresRepository) AddAuditLog(log *domain.AuditLog) error {
	query := `INSERT INTO audit_logs (timestamp, actor_username, action, target_device, details) VALUES ($1, $2, $3, $4, $5)`
	_, err := r.db.Exec(query, log.Timestamp, log.Actor, log.Action, log.Target, log.Details)
	return err
}

func (r *postgresRepository) GetAuditLogs(limit, offset int) ([]domain.AuditLog, error) {
	rows, err := r.db.Query("SELECT id, timestamp, actor_username, action, target_device, details FROM audit_logs ORDER BY timestamp DESC LIMIT $1 OFFSET $2", limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []domain.AuditLog
	for rows.Next() {
		var l domain.AuditLog
		if err := rows.Scan(&l.ID, &l.Timestamp, &l.Actor, &l.Action, &l.Target, &l.Details); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}
	return logs, nil
}

// --- Settings Methods ---

func (r *postgresRepository) GetSetting(key string) (string, error) {
	var value string
	err := r.db.QueryRow("SELECT value FROM settings WHERE key = $1", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

func (r *postgresRepository) SetSetting(key, value string) error {
	query := `INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, $3)
              ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`
	_, err := r.db.Exec(query, key, value, time.Now())
	return err
}

func (r *postgresRepository) GetSettingKeysByPrefix(prefix string) ([]string, error) {
	rows, err := r.db.Query(`SELECT key FROM settings WHERE key LIKE $1 || '%' ORDER BY key`, prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []string{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// --- Subscription Provider Methods ---

func (r *postgresRepository) GetSubscriptionProviders() ([]domain.SubscriptionProvider, error) {
	rows, err := r.db.Query(`SELECT domain, name FROM subscription_providers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var providers []domain.SubscriptionProvider
	for rows.Next() {
		var p domain.SubscriptionProvider
		if err := rows.Scan(&p.Domain, &p.Name); err != nil {
			return nil, err
		}
		providers = append(providers, p)
	}
	return providers, rows.Err()
}

func (r *postgresRepository) GetProviderNames() (map[string]string, error) {
	rows, err := r.db.Query(`SELECT domain, name FROM subscription_providers`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	names := map[string]string{}
	for rows.Next() {
		var d, name string
		if err := rows.Scan(&d, &name); err != nil {
			return nil, err
		}
		names[d] = name
	}
	return names, rows.Err()
}

func (r *postgresRepository) UpsertSubscriptionProvider(p domain.SubscriptionProvider) error {
	query := `INSERT INTO subscription_providers (domain, name) VALUES ($1, $2)
		ON CONFLICT (domain) DO UPDATE SET name = EXCLUDED.name`
	_, err := r.db.Exec(query, p.Domain, p.Name)
	return err
}

func (r *postgresRepository) DeleteSubscriptionProvider(domain string) error {
	_, err := r.db.Exec(`DELETE FROM subscription_providers WHERE domain = $1`, domain)
	return err
}

// SyncProviders reconciles the subscription_providers table with the URLs
// actually referenced by nodes:
//  1. Every hostname found in any node's sub_urls (or the legacy sub_url
//     column) is guaranteed to exist as a provider row. Missing rows are
//     auto-added with the domain itself as the default friendly name;
//     ON CONFLICT DO NOTHING keeps manual renames intact.
//  2. Provider rows whose domain is referenced by no node are deleted
//     (garbage collection).
func (r *postgresRepository) SyncProviders() error {
	// 1. Extract the set of active domains across the whole fleet.
	rows, err := r.db.Query(`SELECT sub_urls::text, COALESCE(sub_url, '') FROM nodes`)
	if err != nil {
		return err
	}
	defer rows.Close()

	active := map[string]struct{}{}
	for rows.Next() {
		var rawSubURLs sql.NullString
		var legacyURL string
		if err := rows.Scan(&rawSubURLs, &legacyURL); err != nil {
			return err
		}
		var urls []string
		if rawSubURLs.Valid && rawSubURLs.String != "" {
			if err := json.Unmarshal([]byte(rawSubURLs.String), &urls); err != nil {
				urls = nil
			}
		}
		if legacyURL != "" {
			urls = append(urls, legacyURL)
		}
		for _, u := range urls {
			if dom := providerHost(u); dom != "" {
				active[dom] = struct{}{}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// 2. Auto-add every active domain that is missing from the table.
	for dom := range active {
		if _, err := r.db.Exec(`INSERT INTO subscription_providers (domain, name) VALUES ($1, $1) ON CONFLICT (domain) DO NOTHING`, dom); err != nil {
			return err
		}
	}

	// 3. Garbage collection: purge provider rows whose domain is no longer
	// referenced by any node's subscription.
	existing, err := r.GetProviderNames()
	if err != nil {
		return err
	}
	for dom := range existing {
		if _, ok := active[dom]; !ok {
			if _, err := r.db.Exec(`DELETE FROM subscription_providers WHERE domain = $1`, dom); err != nil {
				return err
			}
		}
	}
	return nil
}

// providerHost extracts the lowercased hostname of a subscription URL, or ""
// when the URL is malformed or carries no host. Scheme-less entries (e.g.
// "sub.example.com/path") are retried with an https:// prefix.
func providerHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.Hostname() == "" {
		u, err = url.Parse("https://" + raw)
		if err != nil || u.Hostname() == "" {
			return ""
		}
	}
	return strings.ToLower(u.Hostname())
}

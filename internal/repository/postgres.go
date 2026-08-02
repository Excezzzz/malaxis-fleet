package repository

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
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
	}

	for _, query := range queries {
		if _, err := r.db.Exec(query); err != nil {
			return fmt.Errorf("failed to execute schema query: %w", err)
		}
	}

	// Migrate: Add color_hex column if it doesn't exist (idempotent)
	r.db.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS color_hex VARCHAR(7) DEFAULT ''`)
	// Migrate: Add user_id column if it doesn't exist
	r.db.Exec(`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS user_id BIGINT REFERENCES users(id) ON DELETE SET NULL`)
	// Migrate: Add available_servers column if it doesn't exist
	r.db.Exec(`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS available_servers TEXT NOT NULL DEFAULT '[]'`)
	// Migrate: Add hardware_hash column for hardware fingerprint dedup
	r.db.Exec(`ALTER TABLE nodes ADD COLUMN IF NOT EXISTS hardware_hash TEXT`)
	r.db.Exec(`CREATE INDEX IF NOT EXISTS idx_nodes_hardware_hash ON nodes(hardware_hash)`)

	// Migrate: Set default value for device_type and update existing NULL values
	r.db.Exec(`ALTER TABLE nodes ALTER COLUMN device_type SET DEFAULT 'node'`)
	r.db.Exec(`UPDATE nodes SET device_type = 'node' WHERE device_type IS NULL`)

	// Migrate: Ensure roles table has all required columns (idempotent for upgrades)
	r.db.Exec(`ALTER TABLE roles ADD COLUMN IF NOT EXISTS permissions_json TEXT NOT NULL DEFAULT '{}'`)
	r.db.Exec(`ALTER TABLE roles ALTER COLUMN permissions_json SET DEFAULT '{}'`)
	r.db.Exec(`ALTER TABLE roles ALTER COLUMN color_hex TYPE VARCHAR(7)`)
	r.db.Exec(`ALTER TABLE roles ALTER COLUMN color_hex SET DEFAULT '#6B7280'`)
	r.db.Exec(`ALTER TABLE roles ALTER COLUMN name TYPE VARCHAR(255)`)
	r.db.Exec(`ALTER TABLE roles DROP CONSTRAINT IF EXISTS roles_owner_id_fkey`)
	r.db.Exec(`ALTER TABLE roles ALTER COLUMN owner_id TYPE TEXT`)
	r.db.Exec(`ALTER TABLE roles ALTER COLUMN owner_id DROP NOT NULL`)

	// Migrate: Copy old bot settings keys to new tg_ prefixed keys
	r.migrateBotSettings()

	return nil
}

func (r *postgresRepository) migrateBotSettings() {
	oldEnabled, err1 := r.GetSetting("bot_enabled")
	oldToken, err2 := r.GetSetting("bot_token")
	oldChatID, err3 := r.GetSetting("bot_chat_id")

	if err1 == nil && oldEnabled != "" {
		newVal, _ := r.GetSetting("tg_bot_enabled")
		if newVal == "" {
			r.SetSetting("tg_bot_enabled", oldEnabled)
		}
	}
	if err2 == nil && oldToken != "" {
		newVal, _ := r.GetSetting("tg_bot_token")
		if newVal == "" {
			r.SetSetting("tg_bot_token", oldToken)
		}
	}
	if err3 == nil && oldChatID != "" {
		newVal, _ := r.GetSetting("tg_admin_chat_id")
		if newVal == "" {
			r.SetSetting("tg_admin_chat_id", oldChatID)
		}
	}
}

// Close closes the database connection.
func (r *postgresRepository) Close() error {
	return r.db.Close()
}

// --- Node Methods ---

func (r *postgresRepository) GetNodeByID(id string) (*domain.Node, error) {
	var n domain.Node
	var availRaw string
	query := `SELECT id, name, hostname, COALESCE(device_type, 'node'), COALESCE(ip_lan, ''), COALESCE(sub_url, ''), COALESCE(active_server, ''), COALESCE(active_engine, ''), COALESCE(active_proto, ''), COALESCE(active_ip_ext, ''), COALESCE(active_outbound_json, ''), COALESCE(available_servers, '[]'), COALESCE(last_seen, '1970-01-01 00:00:00'), COALESCE(pending_command, ''), COALESCE(pending_msg_id, 0), COALESCE(pipeline_status, ''), COALESCE(status_message, ''), user_id FROM nodes WHERE id = $1`
	err := r.db.QueryRow(query, id).Scan(&n.ID, &n.Name, &n.Hostname, &n.DeviceType, &n.IPLan, &n.SubURL, &n.ActiveServer, &n.ActiveEngine, &n.ActiveProto, &n.ActiveIPExt, &n.ActiveOutboundJSON, &availRaw, &n.LastSeen, &n.PendingCommand, &n.PendingMsgID, &n.PipelineStatus, &n.StatusMessage, &n.UserID)
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(availRaw), &n.AvailableServers)
	return &n, nil
}

func (r *postgresRepository) GetAllNodes() ([]domain.Node, error) {
	rows, err := r.db.Query(`SELECT id, name, hostname, COALESCE(device_type, 'node'), COALESCE(ip_lan, ''), COALESCE(sub_url, ''), COALESCE(active_server, ''), COALESCE(active_engine, ''), COALESCE(active_proto, ''), COALESCE(active_ip_ext, ''), COALESCE(active_outbound_json, ''), COALESCE(available_servers, '[]'), COALESCE(last_seen, '1970-01-01 00:00:00'), COALESCE(pending_command, ''), COALESCE(pending_msg_id, 0), COALESCE(pipeline_status, ''), COALESCE(status_message, ''), user_id FROM nodes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []domain.Node
	for rows.Next() {
		var n domain.Node
		var availRaw string
		if err := rows.Scan(&n.ID, &n.Name, &n.Hostname, &n.DeviceType, &n.IPLan, &n.SubURL, &n.ActiveServer, &n.ActiveEngine, &n.ActiveProto, &n.ActiveIPExt, &n.ActiveOutboundJSON, &availRaw, &n.LastSeen, &n.PendingCommand, &n.PendingMsgID, &n.PipelineStatus, &n.StatusMessage, &n.UserID); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(availRaw), &n.AvailableServers)
		nodes = append(nodes, n)
	}
	return nodes, nil
}

func (r *postgresRepository) GetNodesByUserID(userID int64) ([]domain.Node, error) {
	rows, err := r.db.Query(`SELECT id, name, hostname, COALESCE(device_type, 'node'), COALESCE(ip_lan, ''), COALESCE(sub_url, ''), COALESCE(active_server, ''), COALESCE(active_engine, ''), COALESCE(active_proto, ''), COALESCE(active_ip_ext, ''), COALESCE(active_outbound_json, ''), COALESCE(available_servers, '[]'), COALESCE(last_seen, '1970-01-01 00:00:00'), COALESCE(pending_command, ''), COALESCE(pending_msg_id, 0), COALESCE(pipeline_status, ''), COALESCE(status_message, ''), user_id FROM nodes WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []domain.Node
	for rows.Next() {
		var n domain.Node
		var availRaw string
		if err := rows.Scan(&n.ID, &n.Name, &n.Hostname, &n.DeviceType, &n.IPLan, &n.SubURL, &n.ActiveServer, &n.ActiveEngine, &n.ActiveProto, &n.ActiveIPExt, &n.ActiveOutboundJSON, &availRaw, &n.LastSeen, &n.PendingCommand, &n.PendingMsgID, &n.PipelineStatus, &n.StatusMessage, &n.UserID); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(availRaw), &n.AvailableServers)
		nodes = append(nodes, n)
	}
	return nodes, nil
}

func (r *postgresRepository) AddNode(node *domain.Node) error {
	query := `INSERT INTO nodes (id, name, hostname, ip_lan, sub_url, last_seen, user_id) VALUES ($1, $2, $3, $4, $5, $6, $7)`
	_, err := r.db.Exec(query, node.ID, node.Name, node.Hostname, node.IPLan, node.SubURL, time.Now(), node.UserID)
	return err
}

func (r *postgresRepository) UpsertNode(node *domain.Node) (string, error) {
	// Dedup ghost nodes: if another node with the same hostname exists under a
	// different id (reinstall creates a fresh node_id), remove the stale one —
	// but only if it has been offline for a while, so a live duplicate is kept.
	if node.Hostname != "" {
		_, _ = r.db.Exec(`DELETE FROM nodes WHERE hostname = $1 AND id != $2 AND last_seen < NOW() - interval '10 minutes'`, node.Hostname, node.ID)
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

	query := `INSERT INTO nodes (id, name, hostname, ip_lan, sub_url, hardware_hash, last_seen)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())
		ON CONFLICT (id) DO UPDATE SET
			last_seen = NOW(),
			ip_lan = EXCLUDED.ip_lan,
			sub_url = COALESCE(EXCLUDED.sub_url, nodes.sub_url),
			name = CASE
				WHEN nodes.name IS NULL OR nodes.name = '' OR nodes.name = nodes.hostname THEN EXCLUDED.name
				ELSE nodes.name
			END,
			hostname = COALESCE(EXCLUDED.hostname, nodes.hostname),
			hardware_hash = COALESCE(EXCLUDED.hardware_hash, nodes.hardware_hash)`
	_, err := r.db.Exec(query, node.ID, node.Name, node.Hostname, node.IPLan, node.SubURL, node.HardwareHash)
	return node.ID, err
}

func (r *postgresRepository) RenameNode(id, name string) error {
	_, err := r.db.Exec("UPDATE nodes SET name = $1 WHERE id = $2", name, id)
	return err
}

func (r *postgresRepository) SetNodeHardwareHash(id, hardwareHash string) error {
	_, err := r.db.Exec("UPDATE nodes SET hardware_hash = $1 WHERE id = $2", hardwareHash, id)
	return err
}

func (r *postgresRepository) UpdateNode(node *domain.Node) error {
	query := `UPDATE nodes SET name = $1, hostname = $2, ip_lan = $3, sub_url = $4, user_id = $5 WHERE id = $6`
	_, err := r.db.Exec(query, node.Name, node.Hostname, node.IPLan, node.SubURL, node.UserID, node.ID)
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

func (r *postgresRepository) UpdateNodeReport(id, ipExt, engine, proto, outboundJSON, activeServer, availableServers, subURL string) error {
	query := `UPDATE nodes SET active_ip_ext = $1, active_engine = $2, active_proto = $3, active_outbound_json = $4, active_server = $5, available_servers = $6, sub_url = COALESCE($8, sub_url) WHERE id = $7`
	_, err := r.db.Exec(query, ipExt, engine, proto, outboundJSON, activeServer, availableServers, id, subURL)
	return err
}

func (r *postgresRepository) UpdateNodePipelineStatus(nodeID, status, message string) error {
	query := `UPDATE nodes SET pipeline_status = $1, status_message = $2 WHERE id = $3`
	_, err := r.db.Exec(query, status, message, nodeID)
	return err
}

func (r *postgresRepository) GetNodesWithSubURL() ([]domain.Node, error) {
	rows, err := r.db.Query(`SELECT id, name, COALESCE(sub_url, ''), COALESCE(active_server, ''), COALESCE(active_outbound_json, '') FROM nodes WHERE sub_url != '' AND sub_url IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []domain.Node
	for rows.Next() {
		var n domain.Node
		if err := rows.Scan(&n.ID, &n.Name, &n.SubURL, &n.ActiveServer, &n.ActiveOutboundJSON); err != nil {
			return nil, err
		}
		nodes = append(nodes, n)
	}
	return nodes, nil
}

func (r *postgresRepository) AssignNodeToUser(nodeID string, userID int64) error {
	_, err := r.db.Exec("UPDATE nodes SET user_id = $1 WHERE id = $2", userID, nodeID)
	return err
}

// UpdateAllNodesSubURL updates the sub_url field for ALL nodes in the database.
func (r *postgresRepository) UpdateAllNodesSubURL(subURL string) error {
	_, err := r.db.Exec("UPDATE nodes SET sub_url = $1", subURL)
	return err
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
	query := "UPDATE nodes SET pending_command = $1, pending_msg_id = $2, pipeline_status = 'Queued', status_message = '' WHERE id = $3"
	_, err := r.db.Exec(query, command, messageID, nodeID)
	return err
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
	var id int64
	query := "INSERT INTO users (username, password_hash, role, created_at, color_hex) VALUES ($1, $2, $3, $4, $5) RETURNING id"
	err := r.db.QueryRow(query, user.Username, user.PasswordHash, user.Role, user.CreatedAt, user.ColorHex).Scan(&id)
	return id, err
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

func (r *postgresRepository) DeleteUser(id int64) error {
	_, err := r.db.Exec("DELETE FROM users WHERE id = $1", id)
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
	query := "INSERT INTO roles (name, color_hex, owner_id, permissions_json, created_at) VALUES ($1, $2, $3, $4, $5) RETURNING id"
	err := r.db.QueryRow(query, role.Name, role.ColorHex, role.OwnerID, role.PermissionsJSON, role.CreatedAt).Scan(&id)
	return id, err
}

func (r *postgresRepository) GetCustomRolesByOwner(ownerID string) ([]domain.CustomRole, error) {
	rows, err := r.db.Query("SELECT id, name, color_hex, owner_id, permissions_json, created_at FROM roles WHERE owner_id = $1 ORDER BY name", ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var roles []domain.CustomRole
	for rows.Next() {
		var c domain.CustomRole
		if err := rows.Scan(&c.ID, &c.Name, &c.ColorHex, &c.OwnerID, &c.PermissionsJSON, &c.CreatedAt); err != nil {
			return nil, err
		}
		roles = append(roles, c)
	}
	return roles, nil
}

func (r *postgresRepository) GetRoleByName(name string) (*domain.CustomRole, error) {
	var c domain.CustomRole
	query := "SELECT id, name, color_hex, COALESCE(owner_id, ''), permissions_json, created_at FROM roles WHERE name = $1"
	err := r.db.QueryRow(query, name).Scan(&c.ID, &c.Name, &c.ColorHex, &c.OwnerID, &c.PermissionsJSON, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *postgresRepository) GetAllRoles() ([]domain.CustomRole, error) {
	rows, err := r.db.Query("SELECT id, name, color_hex, COALESCE(owner_id, ''), permissions_json, created_at FROM roles ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var roles []domain.CustomRole
	for rows.Next() {
		var c domain.CustomRole
		if err := rows.Scan(&c.ID, &c.Name, &c.ColorHex, &c.OwnerID, &c.PermissionsJSON, &c.CreatedAt); err != nil {
			return nil, err
		}
		roles = append(roles, c)
	}
	return roles, nil
}

func (r *postgresRepository) GetRoleByID(id int64) (*domain.CustomRole, error) {
	var c domain.CustomRole
	query := "SELECT id, name, color_hex, COALESCE(owner_id, ''), permissions_json, created_at FROM roles WHERE id = $1"
	err := r.db.QueryRow(query, id).Scan(&c.ID, &c.Name, &c.ColorHex, &c.OwnerID, &c.PermissionsJSON, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *postgresRepository) UpdateCustomRole(role *domain.CustomRole) error {
	query := "UPDATE roles SET name = $1, color_hex = $2, permissions_json = $3 WHERE id = $4"
	_, err := r.db.Exec(query, role.Name, role.ColorHex, role.PermissionsJSON, role.ID)
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

package domain

import "time"

// Role constants for RBAC. Hierarchy ranks:
//
//	owner (100) > admin (80) > client / custom roles (30) > viewer (10)
const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleClient = "client"
	RoleViewer = "viewer"
)

// Role rank constants define the strict hierarchy used by user management.
// A caller may only create/edit/delete users whose role rank is STRICTLY
// LOWER than their own.
const (
	RoleRankOwner  = 100
	RoleRankAdmin  = 80
	RoleRankClient = 30
	RoleRankViewer = 10
)

// RoleRank returns the numeric hierarchy rank of a role. Custom roles and
// unknown roles default to the client rank (30).
func RoleRank(role string) int {
	switch role {
	case RoleOwner:
		return RoleRankOwner
	case RoleAdmin:
		return RoleRankAdmin
	case RoleViewer:
		return RoleRankViewer
	default:
		return RoleRankClient
	}
}

// Permission constants for custom roles
const (
	PermViewNodes     = "can_view_nodes"
	PermSwitchVPN     = "can_switch_vpn"
	PermEditSub       = "can_edit_sub"
	PermRenameNode    = "can_rename_node"
	PermTerminateNode = "can_terminate_node"
	PermUpdateClient  = "can_update_client"
	PermPurgeNodes    = "can_purge_nodes"
	PermViewUsers     = "can_view_users"
	PermCreateUsers   = "can_create_users"
	PermEditUsers     = "can_edit_users"
	PermDeleteUsers   = "can_delete_users"
	PermViewRoles     = "can_view_roles"
	PermManageRoles   = "can_manage_roles"
	// Deprecated: kept for backwards compatibility with roles seeded before
	// the audit-log split. Not enforced by any route; the audit log endpoint
	// requires PermViewAuditLogs.
	PermViewAudit      = "can_view_audit"
	PermViewNodeLogs   = "can_view_node_logs"
	PermViewMasterLogs = "can_view_master_logs"
	PermViewAuditLogs  = "can_view_audit_logs"
	// Deprecated: kept for backwards compatibility with roles seeded before
	// the granular split. Migrated to PermViewUsers/CreateUsers/EditUsers/
	// DeleteUsers.
	PermManageUsers = "can_manage_users"
)

// AllPermissions is the complete set of permission keys granted implicitly to
// the owner and admin accounts. Used for /me responses and middleware checks.
// NOTE: backups are NOT in this list — DB backup access is hardcoded to the
// owner role only (see router.go / DownloadBackupHandler).
var AllPermissions = []string{
	PermViewNodes, PermSwitchVPN, PermEditSub, PermRenameNode,
	PermTerminateNode, PermUpdateClient, PermPurgeNodes,
	PermViewUsers, PermCreateUsers, PermEditUsers, PermDeleteUsers,
	PermViewRoles, PermManageRoles,
	PermViewAudit, PermViewNodeLogs, PermViewMasterLogs, PermViewAuditLogs,
}

// PermissionsGrantedBy maps a coarse/deprecated permission to the granular
// keys it implicitly grants. Used so roles seeded with can_manage_users keep
// working until they are migrated to the granular user permissions.
var PermissionsGrantedBy = map[string][]string{
	PermManageUsers: {PermViewUsers, PermCreateUsers, PermEditUsers, PermDeleteUsers},
}

// User represents an administrator in the system.
type User struct {
	ID           int64     `json:"id" db:"id"`
	Username     string    `json:"username" db:"username"`
	PasswordHash string    `json:"-" db:"password_hash"`
	Role         string    `json:"role" db:"role"`
	RoleID       *int64    `json:"role_id,omitempty" db:"role_id"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
	ColorHex     string    `json:"color_hex,omitempty" db:"color_hex"`
	RoleName     string    `json:"role_name,omitempty" db:"role_name"`
	RoleColor    string    `json:"role_color,omitempty" db:"role_color"`
}

// AllowedAccentColors are the selectable UI accent colors.
var AllowedAccentColors = []string{"indigo", "emerald", "amber", "rose", "cyan"}

// AllowedThemeModes are the selectable UI theme modes.
var AllowedThemeModes = []string{"obsidian", "dark", "light"}

// AllowedLanguages are the selectable UI languages.
var AllowedLanguages = []string{"ru", "en"}

// UserPreferences holds the per-user personalization settings: accent color,
// theme mode, UI language, bot emoji rendering and glassmorphism blur.
type UserPreferences struct {
	AccentColor      string `json:"accent_color"`
	ThemeMode        string `json:"theme_mode"`
	Language         string `json:"language"`
	BotEmojisEnabled bool   `json:"bot_emojis_enabled"`
	BlurEnabled      bool   `json:"blur_enabled"`
}

// CustomRole represents a custom role definition created by the Owner.
type CustomRole struct {
	ID              int64     `json:"id" db:"id"`
	Name            string    `json:"name" db:"name"`
	ColorHex        string    `json:"color_hex" db:"color_hex"`
	OwnerID         string    `json:"owner_id" db:"owner_id"`
	PermissionsJSON string    `json:"permissions_json" db:"permissions_json"`
	Rank            int       `json:"rank" db:"rank"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
}

type Node struct {
	ID                 string    `json:"id" db:"id"`
	Name               string    `json:"name" db:"name"`
	Hostname           string    `json:"hostname" db:"hostname"`
	DeviceType         string    `json:"device_type" db:"device_type"`
	IPLan              string    `json:"ip_lan" db:"ip_lan"`
	SubURL             string    `json:"sub_url" db:"sub_url"`
	ActiveServer       string    `json:"active_server" db:"active_server"`
	ActiveEngine       string    `json:"active_engine" db:"active_engine"`
	ActiveProto        string    `json:"active_proto" db:"active_proto"`
	ActiveIPExt        string    `json:"active_ip_ext" db:"active_ip_ext"`
	ActiveOutboundJSON string    `json:"active_outbound_json" db:"active_outbound_json"`
	AvailableServers   []string  `json:"available_servers" db:"available_servers"`
	LastSeen           time.Time `json:"last_seen" db:"last_seen"`
	PendingCommand     string    `json:"pending_command" db:"pending_command"`
	PendingMsgID       int64     `json:"pending_msg_id" db:"pending_msg_id"`
	PipelineStatus     string    `json:"pipeline_status" db:"pipeline_status"`
	StatusMessage      string    `json:"status_message" db:"status_message"`
	HardwareHash       string    `json:"hardware_hash" db:"hardware_hash"`
	UserID             *int64    `json:"user_id,omitempty" db:"user_id"`
}

type Outbound struct {
	Engine      string                 `json:"engine"`
	PrettyProto string                 `json:"pretty_proto"`
	Tag         string                 `json:"tag"`
	Type        string                 `json:"type"`
	RawParams   map[string]interface{} `json:"raw_params"`
}

type Command struct {
	Action   string   `json:"action"`
	Outbound Outbound `json:"outbound"`
}

// AuditLog records an administrative action.
type AuditLog struct {
	ID        int64     `json:"id" db:"id"`
	Timestamp time.Time `json:"timestamp" db:"timestamp"`
	Actor     string    `json:"actor" db:"actor_username"`
	Action    string    `json:"action" db:"action"`
	Target    string    `json:"target" db:"target_device"`
	Details   string    `json:"details" db:"details"`
}

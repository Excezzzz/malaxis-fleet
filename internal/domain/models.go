package domain

import "time"

// Role constants for three-tier RBAC
const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleClient = "client"
)

// Permission constants for custom roles
const (
	PermViewNodes     = "can_view_nodes"
	PermSwitchVPN     = "can_switch_vpn"
	PermEditSub       = "can_edit_sub"
	PermManageUsers   = "can_manage_users"
	PermViewAudit     = "can_view_audit"
	PermExportBackups = "can_export_backups"
)

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

// CustomRole represents a custom role definition created by the Owner.
type CustomRole struct {
	ID              int64     `json:"id" db:"id"`
	Name            string    `json:"name" db:"name"`
	ColorHex        string    `json:"color_hex" db:"color_hex"`
	OwnerID         string    `json:"owner_id" db:"owner_id"`
	PermissionsJSON string    `json:"permissions_json" db:"permissions_json"`
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

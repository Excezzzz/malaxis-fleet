package repository

import (
	"errors"

	"malaxis-fleet/internal/domain"
)

// ErrNodeNotFound is returned when a write targets a node id that does not
// exist in the database. It keeps the command flow consistent: a command can
// never be queued against a ghost node.
var ErrNodeNotFound = errors.New("node not found")

// Repository defines the interface for data access operations.
type Repository interface {
	// Init initializes the database, creating tables if they don't exist.
	Init() error
	// Close closes the database connection(s).
	Close() error

	// --- Node Methods ---
	GetNodeByID(id string) (*domain.Node, error)
	GetAllNodes() ([]domain.Node, error)
	GetNodesByUserID(userID int64) ([]domain.Node, error)
	AddNode(node *domain.Node) error
	// UpsertNode upserts the node and returns the canonical id together with a
	// bool reporting whether this registration created a BRAND NEW node row
	// (used by the backend to fire instant Telegram onboarding notifications).
	UpsertNode(node *domain.Node) (string, bool, error)
	RenameNode(id, name string) error
	SetNodeHardwareHash(id, hardwareHash string) error
	UpdateNode(node *domain.Node) error
	DeleteNode(id string) error
	DeleteOfflineNodes(thresholdDays int) (int64, error)
	UpdateNodeStatus(id, ipLan string) error
	UpdateNodeReport(id, ipExt, engine, proto, outboundJSON, activeServer, availableServers, subURL string) error
	UpdateNodePipelineStatus(nodeID, status, message string) error
	GetNodesWithSubURL() ([]domain.Node, error)
	AssignNodeToUser(nodeID string, userID int64) error
	UpdateAllNodesSubURL(subURL string) error
	SetNodeLogs(id, logsJSON string) error
	GetNodeLogs(id string) (string, error)

	// --- Command Methods ---
	GetPendingCommand(nodeID string) (string, error)
	SetPendingCommand(nodeID, command string, messageID int64) error
	ClearPendingCommand(nodeID string) error

	// --- User Methods ---
	GetUserByUsername(username string) (*domain.User, error)
	GetUserByID(id int64) (*domain.User, error)
	GetAllUsers() ([]domain.User, error)
	AddUser(user *domain.User) (int64, error)
	UpsertAdminUser(username, passwordHash string) error
	UpdateUser(user *domain.User) error
	UpdateUserPassword(id int64, passwordHash string) error
	UpdateUserUsername(id int64, username string) error
	DeleteUser(id int64) error
	IsUsersEmpty() (bool, error)
	CountUsersByRole(role string) (int, error)
	GetUserPreferences(id int64) (*domain.UserPreferences, error)
	UpdateUserPreferences(id int64, p domain.UserPreferences) error

	// --- Custom Role Methods ---
	AddCustomRole(role *domain.CustomRole) (int64, error)
	GetCustomRolesByOwner(ownerID string) ([]domain.CustomRole, error)
	GetRoleByName(name string) (*domain.CustomRole, error)
	GetAllRoles() ([]domain.CustomRole, error)
	GetRoleByID(id int64) (*domain.CustomRole, error)
	UpdateCustomRole(role *domain.CustomRole) error
	DeleteCustomRole(id int64) error
	CountUsersByRoleName(roleName string) (int, error)

	// --- Audit Log Methods ---
	AddAuditLog(log *domain.AuditLog) error
	GetAuditLogs(limit, offset int) ([]domain.AuditLog, error)

	// --- Settings Methods ---
	GetSetting(key string) (string, error)
	SetSetting(key, value string) error
}

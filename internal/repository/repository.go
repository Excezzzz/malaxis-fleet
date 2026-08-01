package repository

import "malaxis-fleet/internal/domain"

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
	UpsertNode(node *domain.Node) error
	UpdateNode(node *domain.Node) error
	DeleteNode(id string) error
	UpdateNodeStatus(id, ipLan string) error
	UpdateNodeReport(id, ipExt, engine, proto, outboundJSON, activeServer string) error
	UpdateNodePipelineStatus(nodeID, status, message string) error
	GetNodesWithSubURL() ([]domain.Node, error)
	AssignNodeToUser(nodeID string, userID int64) error
	UpdateAllNodesSubURL(subURL string) error

	// --- Command Methods ---
	GetPendingCommand(nodeID string) (string, error)
	SetPendingCommand(nodeID, command string, messageID int64) error
	ClearPendingCommand(nodeID string) error

	// --- User Methods ---
	GetUserByUsername(username string) (*domain.User, error)
	GetUserByID(id int64) (*domain.User, error)
	GetAllUsers() ([]domain.User, error)
	AddUser(user *domain.User) (int64, error)
	UpdateUser(user *domain.User) error
	UpdateUserPassword(id int64, passwordHash string) error
	DeleteUser(id int64) error
	IsUsersEmpty() (bool, error)
	CountUsersByRole(role string) (int, error)

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

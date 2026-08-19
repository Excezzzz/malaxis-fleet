package repository

import (
	"errors"

	"malaxis-fleet/internal/domain"
)

// ErrNodeNotFound is returned when a write targets a node id that does not exist in the database. It keeps the command flow consistent: a command can never be queued against a ghost node.
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
	// UpsertNode upserts the node and returns the canonical id together with a bool reporting whether this registration created a BRAND NEW node row (used by the backend to fire instant Telegram onboarding notifications).
	UpsertNode(node *domain.Node) (string, bool, error)
	RenameNode(id, name string) error
	// UpdateNodeNameIfUnset applies the agent-reported display name only when the node has not been explicitly renamed by an admin (i.e. the current name is empty, still the OS hostname, or already the reported name).
	UpdateNodeNameIfUnset(id, name string) error
	SetNodeHardwareHash(id, hardwareHash string) error
	UpdateNode(node *domain.Node) error
	DeleteNode(id string) error
	DeleteOfflineNodes(thresholdDays int) (int64, error)
	UpdateNodeStatus(id, ipLan string) error
	UpdateNodeReport(id, ipExt, engine, proto, outboundJSON, activeServer, activeProvider, availableServers string, subURLs []string, serverProviders map[string]string) error
	UpdateNodePipelineStatus(nodeID, status, message string) error
	GetNodesWithSubURL() ([]domain.Node, error)
	AssignNodeToUser(nodeID string, userID int64) error
	UpdateAllNodesSubURLs(subURLs []string) error
	SetNodeLogs(id, logsJSON string) error
	GetNodeLogs(id string) (string, error)

	// --- Subscription Provider Methods ---
	GetSubscriptionProviders() ([]domain.SubscriptionProvider, error)
	// GetProviderNames returns a domain -> friendly name map for the agent poll payload, so agents can tag cached servers with provider names.
	GetProviderNames() (map[string]string, error)
	UpsertSubscriptionProvider(p domain.SubscriptionProvider) error
	DeleteSubscriptionProvider(domain string) error
	// SyncProviders reconciles the subscription_providers table with the URLs actually referenced by nodes: missing domains are auto-added (name = domain) and rows whose domain is referenced by no node are deleted.
	SyncProviders() error

	// --- Command Methods ---
	GetPendingCommand(nodeID string) (string, error)
	SetPendingCommand(nodeID, command string, messageID int64) error
	// UpdateNodeSubURLsAndQueue atomically saves the subscription URLs and queues the agent update command in a single UPDATE, so a sub_urls change can never be persisted without triggering the agent to fetch it.
	UpdateNodeSubURLsAndQueue(nodeID string, subURLs []string, command string, messageID int64) error
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
	GetSettingKeysByPrefix(prefix string) ([]string, error)
}

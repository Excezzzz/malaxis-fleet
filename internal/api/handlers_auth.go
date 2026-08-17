package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"malaxis-fleet/internal/audit"
	"malaxis-fleet/internal/auth"
	"malaxis-fleet/internal/domain"
	"malaxis-fleet/internal/repository"

	"github.com/gorilla/mux"
	"golang.org/x/crypto/bcrypt"
)

// --- Structs for API Requests ---

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type PollRequest struct {
	ID           string `json:"id"`
	Hostname     string `json:"hostname"`
	IPLan        string `json:"ip_lan"`
	HardwareHash string `json:"hardware_hash"`
	// SubURL is the legacy single-URL field kept for backwards compatibility
	// with pre-v1.2.0 agents; new agents send SubURLs.
	SubURL  string   `json:"sub_url"`
	SubURLs []string `json:"sub_urls"`
	Name    string   `json:"name,omitempty"`
}

type ReportRequest struct {
	ID               string            `json:"id"`
	ExternalIP       string            `json:"external_ip"`
	Engine           string            `json:"engine"`
	Protocol         string            `json:"protocol"`
	OutboundJSON     string            `json:"outbound_json"`
	Status           string            `json:"status,omitempty"`
	Message          string            `json:"message,omitempty"`
	ActiveServer     string            `json:"active_server,omitempty"`
	ActiveProvider   string            `json:"active_provider,omitempty"`
	// AvailableServers holds either the v1.2.2 grouped object
	// {provider: [server, ...]} or the legacy flat array from older agents
	// (normalized to a provider-less group on read). Kept as RawMessage so the
	// stored bytes round-trip unchanged.
	AvailableServers json.RawMessage `json:"available_servers,omitempty"`
	SubURL           string            `json:"sub_url,omitempty"`
	SubURLs          []string          `json:"sub_urls,omitempty"`
	// ServerProviders maps server name -> provider name for UI grouping.
	ServerProviders map[string]string `json:"server_providers,omitempty"`
	Name             string            `json:"name,omitempty"`
	HardwareHash     string            `json:"hardware_hash,omitempty"`
	Logs             string            `json:"logs,omitempty"`
}

type PasswordUpdateRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type SubscriptionValidateRequest struct {
	SubscriptionURL string `json:"subscription_url"`
	NodeID          string `json:"node_id"`
	Hostname        string `json:"hostname"`
	Name            string `json:"name,omitempty"`
}

type BotSettingsRequest struct {
	Enabled bool   `json:"enabled"`
	Token   string `json:"token,omitempty"`
	ChatID  int64  `json:"chat_id,omitempty"`
}

type CustomRoleRequest struct {
	Name            string `json:"name"`
	ColorHex        string `json:"color_hex"`
	PermissionsJSON string `json:"permissions_json"`
	Rank            int    `json:"rank"`
}

type UpdateCustomRoleRequest struct {
	Name            string `json:"name"`
	ColorHex        string `json:"color_hex"`
	PermissionsJSON string `json:"permissions_json"`
	Rank            int    `json:"rank"`
}

type UpdateUserRequest struct {
	Role     string      `json:"role"`
	RoleID   interface{} `json:"role_id,omitempty"`
	ColorHex string      `json:"color_hex"`
	Password string      `json:"password,omitempty"`
	Username string      `json:"username,omitempty"`
}

type ErrResponse struct {
	Error string `json:"error"`
}

type ResetPasswordRequest struct {
	NewPassword string `json:"new_password"`
}

type UpdateNodeRequest struct {
	Name    string   `json:"name"`
	SubURL  string   `json:"sub_url"`
	SubURLs []string `json:"sub_urls"`
}

// --- Auth Handlers ---

// enforcePermission is the defense-in-depth check every restricted handler
// MUST run before mutating state: it re-verifies the session user's role
// against the permissions_json stored in PostgreSQL and writes a 403 response
// when the permission is missing. Returns true when the call is allowed.
func (a *API) enforcePermission(w http.ResponseWriter, r *http.Request, permission string) bool {
	userID, ok := r.Context().Value(auth.UserContextKey).(int64)
	if !ok || userID == 0 {
		http.Error(w, "Unauthorized: Not authenticated", http.StatusUnauthorized)
		return false
	}

	user, err := a.repo.GetUserByID(userID)
	if err != nil {
		http.Error(w, "Forbidden: User not found", http.StatusForbidden)
		return false
	}

	// Owner and admin implicitly hold every permission.
	if user.Role == domain.RoleOwner || user.Role == domain.RoleAdmin {
		return true
	}

	role, err := a.repo.GetRoleByName(user.Role)
	if err != nil {
		http.Error(w, "Forbidden: Role not found", http.StatusForbidden)
		return false
	}

	if auth.HasPermission(a.parsePermissionsJSON(role.PermissionsJSON), permission) {
		return true
	}

	http.Error(w, "Forbidden: Missing permission: "+permission, http.StatusForbidden)
	return false
}

// requireOwner is the defense-in-depth check for user/role management: only
// the owner role may manage other users or roles. Writes 403 when denied.
func (a *API) requireOwner(w http.ResponseWriter, r *http.Request) bool {
	userID, ok := r.Context().Value(auth.UserContextKey).(int64)
	if !ok || userID == 0 {
		http.Error(w, "Unauthorized: Not authenticated", http.StatusUnauthorized)
		return false
	}

	user, err := a.repo.GetUserByID(userID)
	if err != nil {
		http.Error(w, "Forbidden: User not found", http.StatusForbidden)
		return false
	}

	if user.Role == domain.RoleOwner {
		return true
	}

	http.Error(w, "Forbidden: Only the owner can perform this action", http.StatusForbidden)
	return false
}

// actor resolves the acting user from the session context, logging any
// resolution failure instead of silently ignoring it. Returns nil when the
// actor cannot be loaded.
func (a *API) actor(r *http.Request) *domain.User {
	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)
	u, err := a.repo.GetUserByID(actorID)
	if err != nil {
		log.Printf("ERROR: failed to resolve actor %d for audit logging: %v", actorID, err)
		return nil
	}
	return u
}

// actorName safely renders the acting user's username, never panicking on a
// failed actor resolution.
func (a *API) actorName(u *domain.User) string {
	if u == nil {
		return "unknown"
	}
	return u.Username
}

// actorRole safely renders the acting user's role, never panicking on a failed
// actor resolution.
func (a *API) actorRole(u *domain.User) string {
	if u == nil {
		return "unknown"
	}
	return u.Role
}

// writeForbidden writes a JSON 403 response with the given error message.
func (a *API) writeForbidden(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	json.NewEncoder(w).Encode(ErrResponse{Error: message})
}

// roleRank resolves the effective hierarchy rank of a role. It prefers the
// configurable rank stored in the roles table (for custom and seeded roles)
// and falls back to the built-in domain rank table for owners / hardcoded
// roles that are matched by name but might not exist as rows.
func (a *API) roleRank(roleName string) int {
	role, err := a.repo.GetRoleByName(roleName)
	if err == nil && role.Rank >= 1 && role.Rank <= 100 {
		return role.Rank
	}
	return domain.RoleRank(roleName)
}

// parsePermissionsJSON parses a role's permissions_json in both supported
// storage formats (array of strings, or map key->bool).
func (a *API) parsePermissionsJSON(raw string) []string {
	if raw == "" {
		return []string{}
	}
	var perms []string
	if err := json.Unmarshal([]byte(raw), &perms); err != nil {
		var permMap map[string]bool
		if err2 := json.Unmarshal([]byte(raw), &permMap); err2 == nil {
			for p, v := range permMap {
				if v {
					perms = append(perms, p)
				}
			}
		}
	}
	if perms == nil {
		perms = []string{}
	}
	return perms
}

// permissionsForUser resolves the permission list for a user. Owner and admin
// bypass checks (implicitly granted all permissions); custom roles read their
// permissions_json, supporting both array and map storage formats.
func (a *API) permissionsForUser(user *domain.User) []string {
	if user.Role == domain.RoleOwner || user.Role == domain.RoleAdmin {
		return domain.AllPermissions
	}

	var permissions []string
	role, err := a.repo.GetRoleByName(user.Role)
	if err != nil || role.PermissionsJSON == "" {
		return []string{}
	}
	permissions = a.parsePermissionsJSON(role.PermissionsJSON)
	return permissions
}

func (a *API) LoginHandler(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	user, err := a.repo.GetUserByUsername(req.Username)
	if err != nil {
		if err == sql.ErrNoRows {
			a.auditLogger.LogFromRequest(r, req.Username, audit.ActionLoginFailure, req.Username, "Invalid username or password")
			http.Error(w, "Invalid username or password", http.StatusUnauthorized)
			return
		}
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		a.auditLogger.LogFromRequest(r, req.Username, audit.ActionLoginFailure, req.Username, "Invalid username or password")
		http.Error(w, "Invalid username or password", http.StatusUnauthorized)
		return
	}

	session, _ := auth.Store.Get(r, "fleet-session")
	session.Values["user_id"] = user.ID
	session.Values["role"] = user.Role
	if err := session.Save(r, w); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	a.auditLogger.LogFromRequest(r, user.Username, audit.ActionLoginSuccess, user.Username, "User logged in successfully")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      "ok",
		"role":        user.Role,
		"permissions": a.permissionsForUser(user),
		"user": map[string]interface{}{
			"id":       user.ID,
			"username": user.Username,
			"role":     user.Role,
			"color":    user.ColorHex,
		},
	})
}

func (a *API) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	session, _ := auth.Store.Get(r, "fleet-session")
	userID, ok := session.Values["user_id"].(int64)
	if ok && userID != 0 {
		user, err := a.repo.GetUserByID(userID)
		if err == nil {
			a.auditLogger.LogFromRequest(r, user.Username, audit.ActionLogout, user.Username, "User logged out")
		}
	}

	session.Values["user_id"] = 0
	session.Options.MaxAge = -1 // Delete the cookie
	if err := session.Save(r, w); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (a *API) MeHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(auth.UserContextKey).(int64)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	user, err := a.repo.GetUserByID(userID)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	permissions := a.permissionsForUser(user)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":          user.ID,
		"username":    user.Username,
		"role":        user.Role,
		"color":       user.ColorHex,
		"role_name":   user.RoleName,
		"role_color":  user.RoleColor,
		"permissions": permissions,
	})
}

// --- Agent-Facing Handlers ---

func (a *API) PollHandler(w http.ResponseWriter, r *http.Request) {
	var req PollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	canonicalID, err := a.registerOrUpdateNode(req)
	if err != nil {
		log.Printf("ERROR: Failed to update node status for %s: %v", req.ID, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	log.Printf("Poll received from %s (%s)", canonicalID, req.Hostname)

	cmd, err := a.repo.GetPendingCommand(canonicalID)
	if err != nil {
		log.Printf("ERROR: Failed to get pending command for %s: %v", canonicalID, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	resp := map[string]interface{}{}
	if req.ID != canonicalID {
		// Hardware fingerprint adoption: tell the agent its canonical node_id.
		resp["node_id"] = canonicalID
	}

	// v1.2.0 multi-subscription: the master is the source of truth for the
	// node's subscription URLs. Return them (plus the legacy single URL for
	// old agents) together with the provider-name dictionary so the agent can
	// tag every cached server with its provider.
	node, err := a.repo.GetNodeByID(canonicalID)
	if err == nil {
		subURLs := node.SubURLs
		if len(subURLs) == 0 && req.SubURL != "" {
			subURLs = []string{req.SubURL}
		}
		resp["sub_urls"] = subURLs
		if len(subURLs) > 0 {
			resp["sub_url"] = subURLs[0]
		}
		if providerNames, err := a.repo.GetProviderNames(); err == nil {
			resp["providers"] = providerNames
		}
	}

	if cmd != "" {
		log.Printf("Sending pending command to %s", canonicalID)
		resp["command"] = cmd
		resp["status"] = "ok"

		if err := a.repo.UpdateNodePipelineStatus(canonicalID, "Fetched", ""); err != nil {
			log.Printf("ERROR: Failed to update pipeline status to Fetched for %s: %v", canonicalID, err)
		}

		// NOTE: pending_command is NOT cleared here. It stays queued in
		// PostgreSQL until the agent finishes executing and reports back
		// (ReportHandler clears it). This keeps the command resilient if the
		// agent goes offline or crashes mid-execution.
	} else {
		resp["status"] = "ok"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (a *API) ReportHandler(w http.ResponseWriter, r *http.Request) {
	var req ReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	availJSON := "{}"
	if len(req.AvailableServers) > 0 {
		availJSON = string(req.AvailableServers)
	}

	err := a.repo.UpdateNodeReport(req.ID, req.ExternalIP, req.Engine, req.Protocol, req.OutboundJSON, req.ActiveServer, req.ActiveProvider, availJSON, req.SubURLs, req.ServerProviders)
	if err != nil {
		log.Printf("ERROR: Failed to update node report for %s: %v", req.ID, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Persist the display name set on the device (CLI "Rename Node") so a
	// custom name survives agent restarts and hostname churn. Admin-renamed
	// nodes are never clobbered by the agent.
	if req.Name != "" {
		if err := a.repo.UpdateNodeNameIfUnset(req.ID, req.Name); err != nil {
			log.Printf("ERROR: Failed to persist reported name for %s: %v", req.ID, err)
		}
	}
	// Store the hardware fingerprint so a future reinstall can be matched.
	if req.HardwareHash != "" {
		if err := a.repo.SetNodeHardwareHash(req.ID, req.HardwareHash); err != nil {
			log.Printf("ERROR: Failed to store hardware_hash for %s: %v", req.ID, err)
		}
	}

	// Clear pending command — prevents infinite restart loop
	if err := a.repo.ClearPendingCommand(req.ID); err != nil {
		log.Printf("ERROR: Failed to clear pending command for %s: %v", req.ID, err)
	}

	if req.Status != "" {
		err := a.repo.UpdateNodePipelineStatus(req.ID, req.Status, req.Message)
		if err != nil {
			log.Printf("ERROR: Failed to update node pipeline status for %s: %v", req.ID, err)
		}
	}

	// Persist container logs (docker logs tail) reported by the agent. The
	// value is a JSON map keyed by container name (node-agent/xray-node/singbox-node).
	if req.Logs != "" {
		if err := a.repo.SetNodeLogs(req.ID, req.Logs); err != nil {
			log.Printf("ERROR: Failed to store node logs for %s: %v", req.ID, err)
		}
	}

	w.WriteHeader(http.StatusOK)
}

func (a *API) SendCommandHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermSwitchVPN) {
		return
	}

	vars := mux.Vars(r)
	nodeID := vars["id"]

	var req map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	if _, hasCmd := req["command"]; !hasCmd {
		if _, hasAction := req["action"]; !hasAction {
			http.Error(w, "Bad Request: command or action is required", http.StatusBadRequest)
			return
		}
	}

	// Security hardening: only whitelisted actions are accepted here. The
	// agent's arbitrary shell/script execution (`exec`) and raw config write
	// (`apply_config`, `install_xray`, `install_singbox`) are intentionally
	// forbidden through the dashboard API; a can_switch_vpn credential must
	// never translate into code execution on fleet nodes.
	action, _ := req["action"].(string)
	if action == "" {
		if commandStr, ok := req["command"].(string); ok && strings.HasPrefix(strings.TrimSpace(commandStr), "switch:") {
			action = "switch"
		} else {
			action = "unknown"
		}
	}
	if req["command"] != nil {
		if cmdStr, ok := req["command"].(string); ok && strings.HasPrefix(strings.TrimSpace(cmdStr), "switch:") {
			action = "switch"
		}
	}
	if action == "" || !allowedCommandActions[action] {
		http.Error(w, "Bad Request: action not permitted", http.StatusBadRequest)
		return
	}

	cmdJSON, err := json.Marshal(req)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	messageID := time.Now().Unix()
	if err := a.repo.SetPendingCommand(nodeID, string(cmdJSON), messageID); err != nil {
		log.Printf("ERROR: Failed to set pending command for node %s: %v", nodeID, err)
		if errors.Is(err, repository.ErrNodeNotFound) {
			http.Error(w, "Node not found", http.StatusNotFound)
		} else {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	actionDisplay := action
	if action == "" {
		actionDisplay, _ = req["command"].(string)
	}
	if err := a.repo.UpdateNodePipelineStatus(nodeID, "Queued", actionDisplay); err != nil {
		log.Printf("ERROR: failed to update pipeline status for node %s: %v", nodeID, err)
	}

	actorUser := a.actor(r)
	a.auditLogger.LogFromRequest(r, a.actorName(actorUser), audit.ActionUpdateSettings, nodeID, "Sent command: "+string(cmdJSON))

	log.Printf("Command queued for node %s: %s", nodeID, string(cmdJSON))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"message": "Command queued for node",
	})
}

// allowedCommandActions lists the command actions that may be queued through
// the dashboard's SendCommandHandler. Dangerous agent capabilities (arbitrary
// shell execution, raw config file writes, engine installs) are excluded.
var allowedCommandActions = map[string]bool{
	"switch":              true,
	"update_sub":          true,
	"update_client_files": true,
}

// --- Middleware ---

func (a *API) AgentTokenMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Bad Request: Could not read body", http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewBuffer(body))

		timestampStr := r.Header.Get("X-Fleet-Timestamp")
		signatureStr := r.Header.Get("X-Fleet-Signature")
		if timestampStr == "" || signatureStr == "" {
			http.Error(w, "Forbidden: Missing required headers", http.StatusForbidden)
			return
		}

		timestamp, err := strconv.ParseInt(timestampStr, 10, 64)
		if err != nil {
			http.Error(w, "Forbidden: Invalid timestamp", http.StatusForbidden)
			return
		}
		if time.Now().Unix()-timestamp > 60 || time.Now().Unix()-timestamp < -60 {
			http.Error(w, "Forbidden: Timestamp out of range", http.StatusForbidden)
			return
		}

		mac := hmac.New(sha256.New, []byte(a.config.FleetSecret))
		mac.Write([]byte(timestampStr))
		mac.Write(body)
		expectedMAC := mac.Sum(nil)

		decodedSignature, err := hex.DecodeString(signatureStr)
		if err != nil {
			http.Error(w, "Forbidden: Invalid signature format", http.StatusForbidden)
			return
		}

		if !hmac.Equal(decodedSignature, expectedMAC) {
			http.Error(w, "Forbidden: Invalid signature", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// --- Helper Functions ---

func (a *API) registerOrUpdateNode(req PollRequest) (string, error) {
	subURLs := req.SubURLs
	if len(subURLs) == 0 && req.SubURL != "" {
		// Legacy pre-v1.2.0 agent: single-URL payload.
		subURLs = []string{req.SubURL}
	}
	node := &domain.Node{
		ID:           req.ID,
		Name:         req.Name,
		Hostname:     req.Hostname,
		IPLan:        req.IPLan,
		HardwareHash: req.HardwareHash,
		SubURLs:      subURLs,
	}
	// The agent sends its custom node name; fall back to the OS hostname when
	// no custom name has been configured.
	if node.Name == "" {
		node.Name = req.Hostname
	}
	canonicalID, isNew, err := a.repo.UpsertNode(node)
	if err != nil {
		return canonicalID, err
	}

	// FIRST-TIME registration: fire an instant Telegram onboarding notification
	// so the admin can approve (when the agent already reported sub URLs),
	// set the subscription URLs, or reject the device right from the chat.
	if isNew && a.botManager != nil {
		a.botManager.NotifyNewNode(canonicalID, req.Hostname, req.IPLan, subURLs)
	}
	return canonicalID, nil
}

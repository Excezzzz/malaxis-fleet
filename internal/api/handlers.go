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
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
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
	SubURL       string `json:"sub_url"`
}

type ReportRequest struct {
	ID               string   `json:"id"`
	ExternalIP       string   `json:"external_ip"`
	Engine           string   `json:"engine"`
	Protocol         string   `json:"protocol"`
	OutboundJSON     string   `json:"outbound_json"`
	Status           string   `json:"status,omitempty"`
	Message          string   `json:"message,omitempty"`
	ActiveServer     string   `json:"active_server,omitempty"`
	AvailableServers []string `json:"available_servers,omitempty"`
	SubURL           string   `json:"sub_url,omitempty"`
	Name             string   `json:"name,omitempty"`
	HardwareHash     string   `json:"hardware_hash,omitempty"`
	Logs             string   `json:"logs,omitempty"`
}

type PasswordUpdateRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type SubscriptionValidateRequest struct {
	SubscriptionURL string `json:"subscription_url"`
	NodeID          string `json:"node_id"`
	Hostname        string `json:"hostname"`
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
}

type ErrResponse struct {
	Error string `json:"error"`
}

type ResetPasswordRequest struct {
	NewPassword string `json:"new_password"`
}

type UpdateNodeRequest struct {
	Name   string `json:"name"`
	SubURL string `json:"sub_url"`
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

	// Owner (and the original admin account) implicitly holds every permission.
	if user.Role == domain.RoleOwner || user.Role == domain.RoleAdmin || user.Username == "admin" {
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

	if user.Role == domain.RoleOwner || user.Username == "admin" {
		return true
	}

	http.Error(w, "Forbidden: Only the owner can perform this action", http.StatusForbidden)
	return false
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
	if user.Role == domain.RoleOwner || user.Role == domain.RoleAdmin || user.Username == "admin" {
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

	availJSON := "[]"
	if len(req.AvailableServers) > 0 {
		if b, err := json.Marshal(req.AvailableServers); err == nil {
			availJSON = string(b)
		}
	}

	err := a.repo.UpdateNodeReport(req.ID, req.ExternalIP, req.Engine, req.Protocol, req.OutboundJSON, req.ActiveServer, availJSON, req.SubURL)
	if err != nil {
		log.Printf("ERROR: Failed to update node report for %s: %v", req.ID, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Persist the display name set on the device (CLI "Rename Node") so a
	// custom name survives agent restarts and hostname churn.
	if req.Name != "" {
		if err := a.repo.RenameNode(req.ID, req.Name); err != nil {
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

// --- Client Onboarding & Subscription Validation ---

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
	a.repo.UpdateNodePipelineStatus(nodeID, "Queued", actionDisplay)

	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)
	actorUser, _ := a.repo.GetUserByID(actorID)
	a.auditLogger.LogFromRequest(r, actorUser.Username, audit.ActionUpdateSettings, nodeID, "Sent command: "+string(cmdJSON))

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

// isOwnSubscriptionURL reports whether raw points at one of the fleet's own
// domains, used by the public onboarding endpoint to prove the subscription
// belongs to this server. IP-literals (loopback, metadata, private ranges) are
// rejected outright to keep the onboarding path SSRF-free.
func (a *API) isOwnSubscriptionURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return false
	}
	for _, d := range []string{a.config.ApiDomain, a.config.SubDomain, a.config.DashboardDomain} {
		if d == "" {
			continue
		}
		dl := strings.ToLower(d)
		if host == dl || strings.HasSuffix(host, "."+dl) {
			return true
		}
	}
	return false
}

// validSubscriptionURL enforces scheme + no-userinfo rules on subscription
// URLs supplied by authenticated admins (web / Telegram). URLs with embedded
// credentials (user:pass@host) are rejected because tokens in the path/query
// are the intended pattern and any accidental password-in-URL is refused.
func validSubscriptionURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return false
	}
	return true
}

// verifySubscriptionURLReachable performs a fast HTTP GET (5s timeout) to
// confirm the subscription URL actually exists and serves a valid response
// before it is saved. Any transport error (DNS, connection refused, timeout)
// or an HTTP status >= 400 rejects the URL.
func verifySubscriptionURLReachable(raw string) error {
	raw = strings.TrimSpace(raw)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(raw)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.CopyN(io.Discard, resp.Body, 4096)
	if resp.StatusCode >= http.StatusBadRequest {
		return errors.New("server returned HTTP " + strconv.Itoa(resp.StatusCode))
	}
	return nil
}

// writeInvalidSubURLError rejects an unreachable subscription URL with a
// fixed 400 payload (the web UI matches this message for its error toast).
func writeInvalidSubURLError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(map[string]string{
		"error": "Invalid Subscription URL: Could not connect or server returned an error",
	})
}

func (a *API) ValidateSubscriptionHandler(w http.ResponseWriter, r *http.Request) {
	var req SubscriptionValidateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	if req.SubscriptionURL == "" {
		http.Error(w, "Bad Request: subscription_url is required", http.StatusBadRequest)
		return
	}

	// Validate that the subscription URL points at this server's own domain.
	// The previous substring check could be tricked by embedding our domain
	// inside an attacker-controlled host (e.g. api.yourdomain.com.evil.com),
	// which is exactly the SSRF (server-side autosync http.Get) vector we
	// block here: parse the real host and require an exact/suffix match.
	if !a.isOwnSubscriptionURL(req.SubscriptionURL) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{
			"error":   "validation_failed",
			"message": "You do not belong to this server. Please check your subscription address.",
		})
		return
	}

	// Register or update the node
	log.Printf("Subscription validated for node %s (%s): %s", req.NodeID, req.Hostname, req.SubscriptionURL)

	// Check if node already exists
	existingNode, err := a.repo.GetNodeByID(req.NodeID)
	if err == nil && existingNode != nil {
		existingNode.SubURL = req.SubscriptionURL
		existingNode.Name = req.Hostname
		a.repo.UpdateNode(existingNode)
	} else {
		newNode := &domain.Node{
			ID:       req.NodeID,
			Name:     req.Hostname,
			Hostname: req.Hostname,
			SubURL:   req.SubscriptionURL,
		}
		a.repo.AddNode(newNode)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"message": "Subscription validated and node registered successfully. Please create your dashboard account.",
	})
}

// --- Client-Facing Handlers ---

func (a *API) GetOwnNodesHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(auth.UserContextKey).(int64)
	if !ok || userID == 0 {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Get the user's role
	user, err := a.repo.GetUserByID(userID)
	if err != nil {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var nodes []domain.Node
	if user.Role == domain.RoleClient {
		// Clients can only see their own nodes
		nodes, err = a.repo.GetNodesByUserID(userID)
	} else {
		// Admins/Owners see all nodes
		nodes, err = a.repo.GetAllNodes()
	}

	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		log.Printf("Error getting nodes: %v", err)
		return
	}

	// Return only relevant fields for clients
	type ClientNode struct {
		ID             string `json:"id"`
		Name           string `json:"name"`
		ActiveEngine   string `json:"active_engine"`
		ActiveProto    string `json:"active_proto"`
		ActiveIPExt    string `json:"active_ip_ext"`
		ActiveServer   string `json:"active_server"`
		LastSeen       string `json:"last_seen"`
		PipelineStatus string `json:"pipeline_status"`
		StatusMessage  string `json:"status_message"`
		SOCKSport      string `json:"socks_port"`
		HTTPport       string `json:"http_port"`
	}

	clientNodes := make([]ClientNode, 0)
	for _, n := range nodes {
		cn := ClientNode{
			ID:             n.ID,
			Name:           n.Name,
			ActiveEngine:   n.ActiveEngine,
			ActiveProto:    n.ActiveProto,
			ActiveIPExt:    n.ActiveIPExt,
			ActiveServer:   n.ActiveServer,
			LastSeen:       n.LastSeen.Format(time.RFC3339),
			PipelineStatus: n.PipelineStatus,
			StatusMessage:  n.StatusMessage,
			SOCKSport:      "6357",
			HTTPport:       "6358",
		}
		clientNodes = append(clientNodes, cn)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(clientNodes)
}

func (a *API) UpdateOwnPasswordHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(auth.UserContextKey).(int64)
	if !ok || userID == 0 {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req PasswordUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	if req.NewPassword == "" {
		http.Error(w, "Bad Request: new password is required", http.StatusBadRequest)
		return
	}

	// Verify current password
	user, err := a.repo.GetUserByID(userID)
	if err != nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.CurrentPassword)); err != nil {
		http.Error(w, "Forbidden: current password is incorrect", http.StatusForbidden)
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if err := a.repo.UpdateUserPassword(userID, string(hashedPassword)); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	a.auditLogger.LogFromRequest(r, user.Username, audit.ActionUpdatePassword, user.Username, "User changed their own password")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// --- Web UI Handlers ---

func (a *API) GetNodesHandler(w http.ResponseWriter, r *http.Request) {
	nodes, err := a.repo.GetAllNodes()
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		log.Printf("Error getting all nodes: %v", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(nodes)
}

func (a *API) UpdateNodeHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermEditSub) {
		return
	}

	vars := mux.Vars(r)
	nodeID := vars["id"]

	var req UpdateNodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	node, err := a.repo.GetNodeByID(nodeID)
	if err != nil {
		http.Error(w, "Node not found", http.StatusNotFound)
		return
	}

	if req.Name != "" {
		node.Name = req.Name
	}
	if req.SubURL != "" {
		// Live check: the URL must actually be reachable before it is saved.
		if err := verifySubscriptionURLReachable(req.SubURL); err != nil {
			log.Printf("WARN: Rejected unreachable sub_url %s for node %s: %v", req.SubURL, nodeID, err)
			writeInvalidSubURLError(w)
			return
		}
		node.SubURL = req.SubURL
	}

	if err := a.repo.UpdateNode(node); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)
	actorUser, _ := a.repo.GetUserByID(actorID)
	a.auditLogger.LogFromRequest(r, actorUser.Username, audit.ActionUpdateDevice, nodeID, "Updated node (name/sub_url)")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(node)
}

// UpdateNodeSubHandler updates only the sub_url field for a node. Fixes PostgreSQL UPDATE.
func (a *API) UpdateNodeSubHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermEditSub) {
		return
	}

	vars := mux.Vars(r)
	nodeID := vars["id"]

	var req struct {
		SubURL string `json:"sub_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	if req.SubURL == "" {
		http.Error(w, "Bad Request: sub_url is required", http.StatusBadRequest)
		return
	}

	if !validSubscriptionURL(req.SubURL) {
		http.Error(w, "Bad Request: sub_url must be a valid http(s) URL", http.StatusBadRequest)
		return
	}

	// Live check: the URL must actually be reachable before it is saved.
	if err := verifySubscriptionURLReachable(req.SubURL); err != nil {
		log.Printf("WARN: Rejected unreachable sub_url %s for node %s: %v", req.SubURL, nodeID, err)
		writeInvalidSubURLError(w)
		return
	}

	// Execute direct PostgreSQL UPDATE to ensure sub_url is properly committed
	node, err := a.repo.GetNodeByID(nodeID)
	if err != nil {
		http.Error(w, "Node not found", http.StatusNotFound)
		return
	}

	node.SubURL = req.SubURL
	if err := a.repo.UpdateNode(node); err != nil {
		log.Printf("ERROR: Failed to update sub_url for node %s: %v", nodeID, err)
		http.Error(w, "Internal Server Error: Failed to update subscription URL", http.StatusInternalServerError)
		return
	}

	// Queue an update_sub command so the node fetches the new subscription.
	command := map[string]string{"action": "update_sub", "sub_url": req.SubURL}
	cmdJSON, _ := json.Marshal(command)
	messageID := time.Now().Unix()
	if err := a.repo.SetPendingCommand(nodeID, string(cmdJSON), messageID); err != nil {
		log.Printf("ERROR: Failed to queue update_sub command for node %s: %v", nodeID, err)
	} else {
		a.repo.UpdateNodePipelineStatus(nodeID, "Queued", "update_sub")
	}

	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)
	actorUser, _ := a.repo.GetUserByID(actorID)
	a.auditLogger.LogFromRequest(r, actorUser.Username, audit.ActionUpdateDevice, nodeID, "Updated subscription URL to "+req.SubURL)

	log.Printf("Updated sub_url for node %s: %s", nodeID, req.SubURL)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"message": "Subscription URL updated, node will refresh on next poll",
	})
}

// MassUpdateSubHandler updates the subscription URL for ALL nodes at once and
// queues update commands for every node (online and offline).
func (a *API) MassUpdateSubHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermEditSub) {
		return
	}

	var req struct {
		SubURL string `json:"sub_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	if req.SubURL == "" {
		http.Error(w, "Bad Request: sub_url is required", http.StatusBadRequest)
		return
	}

	if !validSubscriptionURL(req.SubURL) {
		http.Error(w, "Bad Request: sub_url must be a valid http(s) URL", http.StatusBadRequest)
		return
	}

	// Live check: the URL must actually be reachable before it is saved for ALL nodes.
	if err := verifySubscriptionURLReachable(req.SubURL); err != nil {
		log.Printf("WARN: Rejected unreachable sub_url %s for mass update: %v", req.SubURL, err)
		writeInvalidSubURLError(w)
		return
	}

	// Update sub_url for ALL nodes in PostgreSQL
	if err := a.repo.UpdateAllNodesSubURL(req.SubURL); err != nil {
		log.Printf("ERROR: Failed to mass update sub_url: %v", err)
		http.Error(w, "Internal Server Error: Failed to update subscription URLs", http.StatusInternalServerError)
		return
	}

	// Queue update commands for all nodes (online and offline)
	nodes, err := a.repo.GetAllNodes()
	if err != nil {
		log.Printf("ERROR: Failed to get nodes for mass update: %v", err)
		http.Error(w, "Internal Server Error: Failed to queue commands", http.StatusInternalServerError)
		return
	}

	messageID := time.Now().Unix()
	command, _ := json.Marshal(map[string]string{"action": "update_sub", "sub_url": req.SubURL})
	queuedCount := 0
	for _, node := range nodes {
		if err := a.repo.SetPendingCommand(node.ID, string(command), messageID); err != nil {
			log.Printf("ERROR: Failed to queue command for node %s: %v", node.ID, err)
		} else {
			queuedCount++
		}
	}

	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)
	actorUser, _ := a.repo.GetUserByID(actorID)
	a.auditLogger.LogFromRequest(r, actorUser.Username, audit.ActionUpdateSettings, "all_nodes", "Mass updated subscription URL for all nodes")

	log.Printf("Mass updated sub_url for %d nodes, queued commands for %d nodes", len(nodes), queuedCount)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":          "ok",
		"message":         "Subscription URL updated for all nodes",
		"nodes_updated":   len(nodes),
		"commands_queued": queuedCount,
	})
}

// MassUpdateDomainHandler updates only the domain portion of sub_url for ALL nodes.
// Each node keeps its existing path/token and query parameters; only the host changes.
func (a *API) MassUpdateDomainHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermEditSub) {
		return
	}

	var req struct {
		Domain string `json:"domain"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request: Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Domain == "" {
		http.Error(w, "Bad Request: domain is required", http.StatusBadRequest)
		return
	}

	nodes, err := a.repo.GetAllNodes()
	if err != nil {
		log.Printf("ERROR: Failed to get nodes for mass domain update: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	updatedCount := 0
	for _, node := range nodes {
		if node.SubURL == "" {
			continue
		}
		newURL := replaceDomain(node.SubURL, req.Domain)
		if newURL == node.SubURL {
			continue
		}
		node.SubURL = newURL
		if err := a.repo.UpdateNode(&node); err != nil {
			log.Printf("ERROR: Failed to update sub_url for node %s: %v", node.ID, err)
			continue
		}
		updatedCount++
	}

	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)
	actorUser, _ := a.repo.GetUserByID(actorID)
	auditDetails := "Mass updated subscription domain to: " + req.Domain
	a.auditLogger.LogFromRequest(r, actorUser.Username, audit.ActionUpdateSettings, "all_nodes", auditDetails)

	log.Printf("Mass updated domain to %s for %d/%d nodes", req.Domain, updatedCount, len(nodes))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":        "ok",
		"message":       "Subscription domain updated for all nodes",
		"nodes_total":   len(nodes),
		"nodes_updated": updatedCount,
	})
}

// toInt64 converts a value from JSON (float64, int64, string) to *int64.
func toInt64(v interface{}) *int64 {
	switch val := v.(type) {
	case float64:
		n := int64(val)
		return &n
	case int64:
		return &val
	case string:
		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return nil
		}
		return &n
	}
	return nil
}

// replaceDomain replaces the host portion of a URL with the new domain.
func replaceDomain(rawURL, newDomain string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return rawURL
	}
	u.Host = newDomain
	return u.String()
}

func (a *API) DeleteNodeHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermEditSub) {
		return
	}

	vars := mux.Vars(r)
	nodeID := vars["id"]

	if err := a.repo.DeleteNode(nodeID); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)
	actorUser, _ := a.repo.GetUserByID(actorID)
	a.auditLogger.LogFromRequest(r, actorUser.Username, audit.ActionDeleteDevice, nodeID, "Node deleted")

	w.WriteHeader(http.StatusNoContent)
}

// PurgeOfflineNodesHandler deletes nodes that have been offline for more than
// the given number of days (default 7). Used to clean up ghost rows from
// reinstalled/removed machines.
func (a *API) PurgeOfflineNodesHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermPurgeNodes) {
		return
	}

	days := 7
	if q := r.URL.Query().Get("days"); q != "" {
		if v, err := strconv.Atoi(q); err == nil && v > 0 {
			days = v
		}
	}

	deleted, err := a.repo.DeleteOfflineNodes(days)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)
	actorUser, _ := a.repo.GetUserByID(actorID)
	a.auditLogger.LogFromRequest(r, actorUser.Username, audit.ActionDeleteDevice, "all_nodes", "Purged "+strconv.FormatInt(deleted, 10)+" offline nodes (older than "+strconv.Itoa(days)+" days)")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok", "deleted": deleted})
}

func (a *API) AssignNodeToUserHandler(w http.ResponseWriter, r *http.Request) {
	if !a.requireOwner(w, r) {
		return
	}

	vars := mux.Vars(r)
	nodeID := vars["id"]
	userIDStr := vars["userId"]

	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	targetUser, _ := a.repo.GetUserByID(userID)
	if err := a.repo.AssignNodeToUser(nodeID, userID); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)
	actorUser, _ := a.repo.GetUserByID(actorID)
	targetName := userIDStr
	if targetUser != nil {
		targetName = targetUser.Username
	}
	a.auditLogger.LogFromRequest(r, actorUser.Username, audit.ActionUpdateDevice, nodeID, "Assigned node to user "+targetName)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (a *API) GetAuditLogsHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermViewAuditLogs) {
		return
	}

	limit := 100
	offset := 0

	logs, err := a.repo.GetAuditLogs(limit, offset)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		log.Printf("Error getting audit logs: %v", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(logs)
}

func (a *API) GetUsersHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermViewUsers) {
		return
	}

	users, err := a.repo.GetAllUsers()
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		log.Printf("Error getting users: %v", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(users)
}

// --- Owner-Only Handlers ---

type CreateUserRequest struct {
	Username string      `json:"username"`
	Password string      `json:"password"`
	Role     string      `json:"role"`
	RoleID   interface{} `json:"role_id,omitempty"`
	ColorHex string      `json:"color_hex,omitempty"`
}

func (a *API) CreateUserHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermCreateUsers) {
		return
	}

	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)
	actorUser, err := a.repo.GetUserByID(actorID)
	if err != nil || actorUser == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(ErrResponse{Error: "Forbidden: User not found"})
		return
	}

	var req CreateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[CreateUser] JSON decode error: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrResponse{Error: "Bad Request: " + err.Error()})
		return
	}
	log.Printf("[CreateUser] Body: username=%q role=%q role_id=%v", req.Username, req.Role, req.RoleID)

	if req.Username == "" || req.Password == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrResponse{Error: "username and password are required"})
		return
	}

	if req.Role == "" && req.RoleID != nil {
		if rid := toInt64(req.RoleID); rid != nil {
			roleLookup, err := a.repo.GetRoleByID(*rid)
			if err == nil {
				req.Role = roleLookup.Name
				if req.ColorHex == "" {
					req.ColorHex = roleLookup.ColorHex
				}
			}
		} else if roleName, ok := req.RoleID.(string); ok && roleName != "" {
			roleLookup, err := a.repo.GetRoleByName(roleName)
			if err == nil {
				req.Role = roleLookup.Name
				if req.ColorHex == "" {
					req.ColorHex = roleLookup.ColorHex
				}
			}
		}
	}
	if req.Role == "" {
		req.Role = domain.RoleClient
	}

	// ROLE HIERARCHY: an actor may only create users whose role rank is
	// STRICTLY LOWER than their own. Creating a user with an equal or higher
	// rank (e.g. an admin creating another admin, or a client escalating to
	// owner) is forbidden.
	actorRank := a.roleRank(actorUser.Role)
	if a.roleRank(req.Role) >= actorRank {
		a.writeForbidden(w, "Forbidden: Cannot create users with equal or higher role rank")
		return
	}

	// The owner role is reserved for the original seeded admin account. Nobody
	// (not even the owner) may create additional owner accounts.
	if req.Role == domain.RoleOwner {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(ErrResponse{Error: "The owner role is reserved for the original admin account"})
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("[CreateUser] bcrypt error: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrResponse{Error: "Failed to hash password"})
		return
	}

	user := &domain.User{
		Username:     req.Username,
		PasswordHash: string(hashedPassword),
		Role:         req.Role,
		CreatedAt:    time.Now(),
		ColorHex:     req.ColorHex,
	}

	id, err := a.repo.AddUser(user)
	if err != nil {
		log.Printf("[CreateUser] add error: %v", err)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(err.Error(), "23505") {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(ErrResponse{Error: "Username already exists. Please choose a different name."})
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrResponse{Error: err.Error()})
		}
		return
	}
	user.ID = id
	user.RoleName = user.Role
	user.RoleColor = user.ColorHex

	a.auditLogger.LogFromRequest(r, actorUser.Username, audit.ActionCreateUser, user.Username, "Role: "+user.Role)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(user)
}

func (a *API) UpdateUserHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermEditUsers) {
		return
	}

	vars := mux.Vars(r)
	idStr := vars["id"]
	log.Printf("[UpdateUser] Path ID: %s", idStr)

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		log.Printf("[UpdateUser] Invalid ID '%s': %v", idStr, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrResponse{Error: "Invalid user ID: " + idStr})
		return
	}

	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)

	// Nobody may modify their own active session role or lower their own
	// privileges through the management endpoint.
	if actorID == id {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(ErrResponse{Error: "Cannot modify your own account through this endpoint"})
		return
	}

	actorUser, errGet := a.repo.GetUserByID(actorID)
	if errGet != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(ErrResponse{Error: "Forbidden: User not found"})
		return
	}

	var req UpdateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[UpdateUser] JSON decode error: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrResponse{Error: "Bad Request: " + err.Error()})
		return
	}
	log.Printf("[UpdateUser] Body: role=%q role_id=%v color_hex=%q has_password=%v", req.Role, req.RoleID, req.ColorHex, req.Password != "")

	user, err := a.repo.GetUserByID(id)
	if err != nil {
		log.Printf("[UpdateUser] User %d not found: %v", id, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(ErrResponse{Error: "User not found"})
		return
	}

	// ROLE HIERARCHY: an actor may only edit a target whose role rank is
	// STRICTLY LOWER than their own. Editing an equal/higher rank (an admin
	// demoting another admin, or anything touching the owner) is forbidden.
	if a.roleRank(user.Role) >= a.roleRank(actorUser.Role) {
		a.writeForbidden(w, "Forbidden: Cannot modify users with equal or higher role rank")
		return
	}

	if req.RoleID != nil {
		if rid := toInt64(req.RoleID); rid != nil {
			roleLookup, err := a.repo.GetRoleByID(*rid)
			if err == nil {
				req.Role = roleLookup.Name
				if req.ColorHex == "" {
					user.ColorHex = roleLookup.ColorHex
				}
			}
		} else if roleName, ok := req.RoleID.(string); ok && roleName != "" {
			roleLookup, err := a.repo.GetRoleByName(roleName)
			if err == nil {
				req.Role = roleLookup.Name
				if req.ColorHex == "" {
					user.ColorHex = roleLookup.ColorHex
				}
			}
		}
	}
	if req.Role != "" {
		// An actor may not grant a target a role with an equal/higher rank
		// than themselves, even when the target currently ranks lower.
		if a.roleRank(req.Role) >= a.roleRank(actorUser.Role) {
			a.writeForbidden(w, "Forbidden: Cannot assign a role with equal or higher role rank")
			return
		}
		user.Role = req.Role
	}
	if req.ColorHex != "" {
		user.ColorHex = req.ColorHex
	}

	// Owner role protection: the original admin account (seeded by
	// UpsertAdminUser) is the sole owner and cannot be demoted; no other user
	// may ever be assigned the owner role.
	if user.Role == domain.RoleOwner {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(ErrResponse{Error: "The owner role is reserved for the original admin account"})
		return
	}

	if req.Password != "" {
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			log.Printf("[UpdateUser] bcrypt error: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrResponse{Error: "Failed to hash password"})
			return
		}
		if err := a.repo.UpdateUserPassword(id, string(hashedPassword)); err != nil {
			log.Printf("[UpdateUser] password update error: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrResponse{Error: "Failed to update password: " + err.Error()})
			return
		}
	}

	if req.Role != "" || req.ColorHex != "" || req.RoleID != nil {
		if err := a.repo.UpdateUser(user); err != nil {
			log.Printf("[UpdateUser] user update error: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrResponse{Error: "Failed to update user: " + err.Error()})
			return
		}
	}

	updatedUser, _ := a.repo.GetUserByID(id)
	if updatedUser == nil {
		updatedUser = user
	} else {
		updatedUser.PasswordHash = ""
	}

	a.auditLogger.LogFromRequest(r, user.Username, audit.ActionUpdateUser, updatedUser.Username, "Role updated to: "+updatedUser.Role)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(updatedUser)
}

func (a *API) DeleteUserHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermDeleteUsers) {
		return
	}

	vars := mux.Vars(r)
	idStr := vars["id"]
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrResponse{Error: "Invalid user ID: " + idStr})
		return
	}

	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)

	// Nobody may delete their own account through the management endpoint.
	if actorID == id {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(ErrResponse{Error: "Cannot delete your own account"})
		return
	}

	user, err := a.repo.GetUserByID(id)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(ErrResponse{Error: "User not found"})
		return
	}

	// ROLE HIERARCHY: an actor may only delete a user whose role rank is
	// STRICTLY LOWER than their own. This protects the owner account and any
	// equal-or-higher-rank colleague from lower-rank deletion.
	actorUser, errAct := a.repo.GetUserByID(actorID)
	if errAct == nil && a.roleRank(user.Role) >= a.roleRank(actorUser.Role) {
		a.writeForbidden(w, "Forbidden: Cannot delete users with equal or higher role rank")
		return
	}

	if user.Role == domain.RoleOwner {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(ErrResponse{Error: "Cannot delete the Owner account"})
		return
	}

	if err := a.repo.DeleteUser(id); err != nil {
		log.Printf("[DeleteUser] error deleting user %d: %v", id, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrResponse{Error: err.Error()})
		return
	}

	actorUser, _ = a.repo.GetUserByID(actorID)
	a.auditLogger.LogFromRequest(r, actorUser.Username, audit.ActionDeleteUser, user.Username, "User deleted")

	w.WriteHeader(http.StatusNoContent)
}

func (a *API) ResetUserPasswordHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermEditUsers) {
		return
	}

	vars := mux.Vars(r)
	idStr := vars["id"]
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)

	// Nobody may reset their own password through the management endpoint.
	if actorID == id {
		a.writeForbidden(w, "Forbidden: Cannot modify your own account through this endpoint")
		return
	}

	actorUser, err := a.repo.GetUserByID(actorID)
	if err != nil {
		a.writeForbidden(w, "Forbidden: User not found")
		return
	}

	targetUser, err := a.repo.GetUserByID(id)
	if err != nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	// ROLE HIERARCHY: password resets count as modification. An actor may
	// only reset passwords of users ranking strictly lower than themselves.
	if a.roleRank(targetUser.Role) >= a.roleRank(actorUser.Role) {
		a.writeForbidden(w, "Forbidden: Cannot modify users with equal or higher role rank")
		return
	}

	var req ResetPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	if req.NewPassword == "" {
		http.Error(w, "Bad Request: new_password is required", http.StatusBadRequest)
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if err := a.repo.UpdateUserPassword(id, string(hashedPassword)); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	targetName := idStr
	if targetUser != nil {
		targetName = targetUser.Username
	}
	a.auditLogger.LogFromRequest(r, actorUser.Username, audit.ActionUpdatePassword, targetName, "Password reset by "+actorUser.Role)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// --- Custom Roles Handlers ---

func (a *API) CreateCustomRoleHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermManageRoles) {
		return
	}

	var req CustomRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request: Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		http.Error(w, "Bad Request: name is required", http.StatusBadRequest)
		return
	}

	// ROLE RANK ENFORCEMENT: the actor may only create roles ranked STRICTLY
	// LOWER than their own role. A custom role with a rank >= actor rank could
	// be granted to peers, breaking the hierarchy.
	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)
	actorUser, err := a.repo.GetUserByID(actorID)
	if err != nil {
		a.writeForbidden(w, "Forbidden: User not found")
		return
	}
	actorRank := a.roleRank(actorUser.Role)

	if req.Rank < 1 {
		req.Rank = domain.RoleRankViewer
	}
	if req.Rank >= 100 || req.Rank >= actorRank {
		a.writeForbidden(w, "Forbidden: Role rank must be lower than your current rank ("+strconv.Itoa(actorRank)+")")
		return
	}

	// Escalation guard: an actor may never grant permissions they do not
	// hold themselves (owner/admin bypass via enforcePermission).
	if !a.rolePermissionsAllowed(w, r, req.PermissionsJSON) {
		return
	}

	if req.ColorHex == "" {
		req.ColorHex = "#6B7280"
	}
	if req.PermissionsJSON == "" {
		req.PermissionsJSON = "[]"
	}

	role := &domain.CustomRole{
		Name:            req.Name,
		ColorHex:        req.ColorHex,
		OwnerID:         strconv.FormatInt(actorID, 10),
		PermissionsJSON: req.PermissionsJSON,
		Rank:            req.Rank,
		CreatedAt:       time.Now(),
	}

	id, err := a.repo.AddCustomRole(role)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "23505") {
			http.Error(w, "Conflict: Role with this name already exists", http.StatusConflict)
			return
		}
		log.Printf("Error creating role: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	role.ID = id

	a.auditLogger.LogFromRequest(r, actorUser.Username, audit.ActionCreateUser, role.Name, "Created custom role")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(role)
}

func (a *API) GetCustomRolesHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermViewRoles) {
		return
	}

	roles, err := a.repo.GetAllRoles()
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(roles)
}

func (a *API) GetRolesHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermViewRoles) {
		return
	}

	roles, err := a.repo.GetAllRoles()
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		log.Printf("Error getting all roles: %v", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(roles)
}

func (a *API) UpdateCustomRoleHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermManageRoles) {
		return
	}

	vars := mux.Vars(r)
	idStr := vars["id"]
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid custom role ID", http.StatusBadRequest)
		return
	}

	existing, err := a.repo.GetRoleByID(id)
	if err != nil {
		http.Error(w, "Role not found", http.StatusNotFound)
		return
	}

	var req UpdateCustomRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request: Invalid JSON", http.StatusBadRequest)
		return
	}

	// ROLE RANK ENFORCEMENT:
	//  - The owner role (rank 100) is completely immutable: nobody may
	//    re-rank or otherwise modify it.
	//  - An actor may only modify roles ranked STRICTLY LOWER than their own.
	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)
	actorUser, err := a.repo.GetUserByID(actorID)
	if err != nil {
		a.writeForbidden(w, "Forbidden: User not found")
		return
	}
	actorRank := a.roleRank(actorUser.Role)

	existingRank := existing.Rank
	if existingRank < 1 {
		existingRank = a.roleRank(existing.Name)
	}

	if existing.Name == domain.RoleOwner || existing.Rank >= domain.RoleRankOwner {
		a.writeForbidden(w, "Forbidden: The owner role is immutable and cannot be modified")
		return
	}
	if existingRank >= actorRank {
		a.writeForbidden(w, "Forbidden: Cannot modify a role with an equal or higher rank than yours")
		return
	}
	if req.Rank >= 100 || req.Rank >= actorRank {
		a.writeForbidden(w, "Forbidden: Role rank must be lower than your current rank ("+strconv.Itoa(actorRank)+")")
		return
	}

	// Escalation guard: an actor may never grant permissions they do not
	// hold themselves (owner/admin bypass via enforcePermission).
	if req.PermissionsJSON != "" && !a.rolePermissionsAllowed(w, r, req.PermissionsJSON) {
		return
	}

	if req.Name != "" {
		existing.Name = req.Name
	}
	if req.ColorHex != "" {
		existing.ColorHex = req.ColorHex
	}
	if req.PermissionsJSON != "" {
		existing.PermissionsJSON = req.PermissionsJSON
	}
	if req.Rank >= 1 {
		existing.Rank = req.Rank
	}

	if err := a.repo.UpdateCustomRole(existing); err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "23505") {
			http.Error(w, "Conflict: Role with this name already exists", http.StatusConflict)
			return
		}
		log.Printf("Error updating role: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	a.auditLogger.LogFromRequest(r, actorUser.Username, audit.ActionUpdateSettings, existing.Name, "Updated custom role")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(existing)
}

func (a *API) DeleteCustomRoleHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermManageRoles) {
		return
	}

	vars := mux.Vars(r)
	idStr := vars["id"]
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid custom role ID", http.StatusBadRequest)
		return
	}

	// Check if the role exists
	role, err := a.repo.GetRoleByID(id)
	if err != nil {
		http.Error(w, "Role not found", http.StatusNotFound)
		return
	}

	// ROLE RANK ENFORCEMENT: the owner role (rank 100) is immutable and may
	// never be deleted. Every other role - including the built-in admin,
	// client and viewer roles - can be deleted by an actor whose rank is
	// STRICTLY higher, per the mathematical hierarchy.
	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)
	actorUser, err := a.repo.GetUserByID(actorID)
	if err != nil {
		a.writeForbidden(w, "Forbidden: User not found")
		return
	}
	actorRank := a.roleRank(actorUser.Role)

	roleRank := role.Rank
	if roleRank < 1 {
		roleRank = a.roleRank(role.Name)
	}
	if role.Name == domain.RoleOwner || role.Rank >= domain.RoleRankOwner {
		a.writeForbidden(w, "Forbidden: The owner role is immutable and cannot be deleted")
		return
	}
	if roleRank >= actorRank {
		a.writeForbidden(w, "Forbidden: Cannot delete a role with an equal or higher rank than yours")
		return
	}

	// Prevent deletion if users are assigned to this role
	userCount, err := a.repo.CountUsersByRoleName(role.Name)
	if err == nil && userCount > 0 {
		http.Error(w, "Bad Request: Role is assigned to users", http.StatusBadRequest)
		return
	}

	if err := a.repo.DeleteCustomRole(id); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	a.auditLogger.LogFromRequest(r, actorUser.Username, audit.ActionDeleteUser, role.Name, "Deleted custom role")

	w.WriteHeader(http.StatusNoContent)
}

// rolePermissionsAllowed verifies that none of the granted permissions in
// permissionsJSON exceed the actor's own granted permissions. Used to prevent
// role-creation privilege escalation: a manager may only hand out permissions
// they hold themselves.
func (a *API) rolePermissionsAllowed(w http.ResponseWriter, r *http.Request, permissionsJSON string) bool {
	if permissionsJSON == "" {
		return true
	}

	actorID, ok := r.Context().Value(auth.UserContextKey).(int64)
	if !ok || actorID == 0 {
		a.writeForbidden(w, "Forbidden: User not found")
		return false
	}
	actorUser, err := a.repo.GetUserByID(actorID)
	if err != nil {
		a.writeForbidden(w, "Forbidden: User not found")
		return false
	}

	// Owner and admin implicitly hold every permission.
	if actorUser.Role == domain.RoleOwner || actorUser.Username == "admin" {
		return true
	}

	actorPerms := a.permissionsForUser(actorUser)
	want := a.parsePermissionsJSON(permissionsJSON)
	for _, p := range want {
		if !auth.HasPermission(actorPerms, p) {
			a.writeForbidden(w, "Forbidden: Cannot grant permission you do not hold: "+p)
			return false
		}
	}
	return true
}

// --- Bot Settings Handlers ---

func (a *API) UpdateBotSettingsHandler(w http.ResponseWriter, r *http.Request) {
	if !a.requireOwner(w, r) {
		return
	}

	var req BotSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	if req.Token != "" {
		if err := a.repo.SetSetting("tg_bot_token", req.Token); err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}
	if req.ChatID != 0 {
		if err := a.repo.SetSetting("tg_admin_chat_id", strconv.FormatInt(req.ChatID, 10)); err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}
	if err := a.repo.SetSetting("tg_bot_enabled", strconv.FormatBool(req.Enabled)); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)
	actorUser, _ := a.repo.GetUserByID(actorID)
	a.auditLogger.LogFromRequest(r, actorUser.Username, audit.ActionUpdateSettings, "bot", "Bot settings updated")

	if a.botManager != nil {
		if err := a.botManager.Reboot(); err != nil {
			log.Printf("Warning: bot reboot failed: %v", err)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (a *API) GetBotSettingsHandler(w http.ResponseWriter, r *http.Request) {
	if !a.requireOwner(w, r) {
		return
	}

	enabled, _ := a.repo.GetSetting("tg_bot_enabled")
	token, _ := a.repo.GetSetting("tg_bot_token")
	chatIDStr, _ := a.repo.GetSetting("tg_admin_chat_id")

	// Fall back to the env-configured values when the DB keys are empty, so the
	// dashboard always reflects the token/chat_id the bot actually runs with.
	if token == "" {
		token = a.config.BotToken
	}
	if chatIDStr == "" {
		if a.config.AdminChatID != 0 {
			chatIDStr = strconv.FormatInt(a.config.AdminChatID, 10)
		}
	}

	chatID, _ := strconv.ParseInt(chatIDStr, 10, 64)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"enabled": enabled == "true",
		"token":   token,
		"chat_id": chatID,
	})
}

// GetUserPreferencesHandler returns the current user's personalization
// settings (accent color, theme mode, language, bot emoji rendering).
func (a *API) GetUserPreferencesHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(auth.UserContextKey).(int64)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	prefs, err := a.repo.GetUserPreferences(userID)
	if err != nil {
		log.Printf("Failed to load preferences for user %d: %v", userID, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(prefs)
}

// UpdateUserPreferencesHandler updates the current user's personalization
// settings. Invalid values are silently replaced with the defaults.
func (a *API) UpdateUserPreferencesHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(auth.UserContextKey).(int64)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req domain.UserPreferences
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	if !strSliceContains(domain.AllowedAccentColors, req.AccentColor) {
		req.AccentColor = "indigo"
	}
	if !strSliceContains(domain.AllowedThemeModes, req.ThemeMode) {
		req.ThemeMode = "obsidian"
	}
	if !strSliceContains(domain.AllowedLanguages, req.Language) {
		req.Language = "ru"
	}

	if err := a.repo.UpdateUserPreferences(userID, req); err != nil {
		log.Printf("Failed to save preferences for user %d: %v", userID, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	updated, _ := a.repo.GetUserPreferences(userID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(updated)
}

func strSliceContains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

// ResetBotAvatarHandler re-uploads the embedded default profile photo of the
// Telegram bot on demand.
func (a *API) ResetBotAvatarHandler(w http.ResponseWriter, r *http.Request) {
	if !a.requireOwner(w, r) {
		return
	}

	if a.botManager == nil {
		http.Error(w, "Bot is not configured", http.StatusBadRequest)
		return
	}

	if err := a.botManager.SetDefaultAvatar(); err != nil {
		log.Printf("Bot: avatar restore failed: %v", err)
		http.Error(w, "Failed to update bot profile photo: "+err.Error(), http.StatusInternalServerError)
		return
	}

	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)
	actorUser, _ := a.repo.GetUserByID(actorID)
	a.auditLogger.LogFromRequest(r, actorUser.Username, audit.ActionUpdateSettings, "bot", "Bot profile photo reset to default")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// SetBotAvatarHandler applies one of the five themed avatar colors to the
// Telegram bot's profile photo (owner only). The chosen color is persisted in
// the settings table and re-applied automatically on every bot start.
func (a *API) SetBotAvatarHandler(w http.ResponseWriter, r *http.Request) {
	if !a.requireOwner(w, r) {
		return
	}

	var req struct {
		Color string `json:"color"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request: Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Color == "" {
		http.Error(w, "Bad Request: color is required", http.StatusBadRequest)
		return
	}

	if a.botManager == nil {
		http.Error(w, "Bot is not configured", http.StatusBadRequest)
		return
	}

	if err := a.botManager.SetAvatarColor(req.Color); err != nil {
		log.Printf("Bot: avatar color set failed: %v", err)
		http.Error(w, "Failed to update bot profile photo: "+err.Error(), http.StatusInternalServerError)
		return
	}

	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)
	actorUser, _ := a.repo.GetUserByID(actorID)
	a.auditLogger.LogFromRequest(r, actorUser.Username, audit.ActionUpdateSettings, "bot", "Bot profile photo set to color "+req.Color)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"color":  req.Color,
	})
}

func (a *API) TestTelegramBotHandler(w http.ResponseWriter, r *http.Request) {
	if !a.requireOwner(w, r) {
		return
	}

	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request: Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Token == "" {
		http.Error(w, "Bad Request: token is required", http.StatusBadRequest)
		return
	}

	tgResp, err := http.Get("https://api.telegram.org/bot" + req.Token + "/getMe")
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}
	defer tgResp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(tgResp.Body).Decode(&result); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Invalid response from Telegram API",
		})
		return
	}

	if ok, _ := result["ok"].(bool); !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   result["description"],
		})
		return
	}

	botUser, _ := result["result"].(map[string]interface{})
	botName, _ := botUser["username"].(string)
	botID, _ := botUser["id"].(float64)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"bot_name": botName,
		"bot_id":   botID,
	})
}

// --- General Settings Handler ---

func (a *API) GetSettingsHandler(w http.ResponseWriter, r *http.Request) {
	if !a.requireOwner(w, r) {
		return
	}

	autosyncInterval, _ := a.repo.GetSetting("autosync_interval_minutes")
	botEnabled, _ := a.repo.GetSetting("tg_bot_enabled")
	botToken, _ := a.repo.GetSetting("tg_bot_token")
	botChatIDStr, _ := a.repo.GetSetting("tg_admin_chat_id")
	backupToLocal, _ := a.repo.GetSetting("backup_to_local")
	backupToTelegram, _ := a.repo.GetSetting("backup_to_telegram")
	backupIntervalStr, _ := a.repo.GetSetting("backup_interval_hours")
	botAvatarColor, _ := a.repo.GetSetting("bot_avatar_color")
	lowRAMMode := a.config.LowRAMMode

	backupIntervalHours := 24
	if v, err := strconv.Atoi(backupIntervalStr); err == nil && v > 0 {
		backupIntervalHours = v
	}

	// Fall back to env-configured values so the settings page always reflects
	// the token/chat_id the bot actually runs with.
	if botToken == "" {
		botToken = a.config.BotToken
	}
	if botChatIDStr == "" {
		if a.config.AdminChatID != 0 {
			botChatIDStr = strconv.FormatInt(a.config.AdminChatID, 10)
		}
	}

	botChatID, _ := strconv.ParseInt(botChatIDStr, 10, 64)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"autosync_interval_minutes": autosyncInterval,
		"tg_bot_enabled":            botEnabled == "true",
		"tg_bot_token":              botToken,
		"tg_admin_chat_id":          botChatID,
		"backup_to_local":           backupToLocal != "false",
		"backup_to_telegram":        backupToTelegram == "true",
		"backup_interval_hours":     backupIntervalHours,
		"bot_avatar_color":          botAvatarColor,
		"low_ram_mode":              lowRAMMode,
		"join_domain":               a.config.JoinDomain,
		"api_domain":                a.config.ApiDomain,
		"sub_domain":                a.config.SubDomain,
		"dashboard_domain":          a.config.DashboardDomain,
	})
}

// UpdateBackupSettingsHandler updates the automated-backup settings: routing
// (local / Telegram) and the backup interval in hours (owner only). The values
// are persisted in the settings table and consumed by the backup scheduler
// inside the bot, which re-reads them on every cycle.
func (a *API) UpdateBackupSettingsHandler(w http.ResponseWriter, r *http.Request) {
	if !a.requireOwner(w, r) {
		return
	}

	var req struct {
		BackupToLocal       *bool `json:"backup_to_local"`
		BackupToTelegram    *bool `json:"backup_to_telegram"`
		BackupIntervalHours *int  `json:"backup_interval_hours"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	if req.BackupToLocal != nil {
		if err := a.repo.SetSetting("backup_to_local", strconv.FormatBool(*req.BackupToLocal)); err != nil {
			log.Printf("Error saving backup_to_local: %v", err)
			http.Error(w, "Failed to save settings", http.StatusInternalServerError)
			return
		}
	}
	if req.BackupToTelegram != nil {
		if err := a.repo.SetSetting("backup_to_telegram", strconv.FormatBool(*req.BackupToTelegram)); err != nil {
			log.Printf("Error saving backup_to_telegram: %v", err)
			http.Error(w, "Failed to save settings", http.StatusInternalServerError)
			return
		}
	}
	if req.BackupIntervalHours != nil {
		if *req.BackupIntervalHours < 1 || *req.BackupIntervalHours > 24*31 {
			http.Error(w, "backup_interval_hours must be between 1 and 744", http.StatusBadRequest)
			return
		}
		if err := a.repo.SetSetting("backup_interval_hours", strconv.Itoa(*req.BackupIntervalHours)); err != nil {
			log.Printf("Error saving backup_interval_hours: %v", err)
			http.Error(w, "Failed to save settings", http.StatusInternalServerError)
			return
		}
	}

	localVal, _ := a.repo.GetSetting("backup_to_local")
	tgVal, _ := a.repo.GetSetting("backup_to_telegram")
	intervalVal, _ := a.repo.GetSetting("backup_interval_hours")
	intervalHours := 24
	if v, err := strconv.Atoi(intervalVal); err == nil && v > 0 {
		intervalHours = v
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":                "ok",
		"backup_to_local":       localVal != "false",
		"backup_to_telegram":    tgVal == "true",
		"backup_interval_hours": intervalHours,
	})
}

func (a *API) DownloadBackupHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermExportBackups) {
		return
	}

	backupPath, err := a.backupEngine.CreateBackup()
	if err != nil {
		http.Error(w, "Failed to create backup", http.StatusInternalServerError)
		log.Printf("Error creating backup: %v", err)
		return
	}

	w.Header().Set("Content-Disposition", "attachment; filename="+filepath.Base(backupPath))
	w.Header().Set("Content-Type", "application/zip")
	http.ServeFile(w, r, backupPath)
}

// --- Public Handlers ---

// HealthHandler answers with a deliberately generic probe-friendly response.
// No application identity, database status, or bot state is disclosed.
func (a *API) HealthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// serveDockerCompose serves the docker-compose.yml template with the active SECRET_TOKEN injected.
func (a *API) serveDockerCompose(w http.ResponseWriter, r *http.Request) {
	fileBytes, err := deployFS.ReadFile("deploy/client-docker-compose.yml")
	if err != nil {
		http.Error(w, "Internal Server Error: File not found", http.StatusInternalServerError)
		log.Printf("Error reading embedded docker-compose file: %v", err)
		return
	}

	// Get the active fleet secret from config (auto-generated or user-set)
	secret := a.config.FleetSecret
	if secret == "" {
		// Fallback: try to read from database
		dbSecret, _ := a.repo.GetSetting("fleet_secret")
		if dbSecret != "" {
			secret = dbSecret
		} else {
			secret = "changeme_fleet_secret"
		}
	}

	// Replace the SECRET_TOKEN placeholder with the actual active secret
	content := string(fileBytes)
	content = strings.ReplaceAll(content, "${FLEET_SECRET:-changeme_fleet_secret}", secret)
	content = strings.ReplaceAll(content, "FLEET_SECRET", "FLEET_SECRET") // keep env var name
	content = a.applyDomainPlaceholders(content)

	w.Header().Set("Content-Type", "application/x-yaml")
	w.Write([]byte(content))
}

// templateFiles is the whitelist of client template files editable via the web UI.
var templateFiles = []string{"node_agent.py", "fleet-cli.sh", "requirements.txt", "Dockerfile.client", "entrypoint.sh"}

// isTemplateFile reports whether name is one of the hardcoded whitelisted
// client template files. Path traversal is impossible: the check is an exact
// match and any name containing path separators, "." or ".." is rejected
// outright (defense in depth, even though the exact-match already blocks it).
func isTemplateFile(name string) bool {
	if name == "" || strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return false
	}
	for _, n := range templateFiles {
		if n == name {
			return true
		}
	}
	return false
}

// templateKey returns the settings key under which a template override is stored.
func templateKey(name string) string {
	return "template:" + name
}

// readTemplate returns the template content, preferring a web-edited override
// stored in the database over the embedded copy.
func (a *API) readTemplate(name string) (string, bool) {
	if isTemplateFile(name) {
		if override, err := a.repo.GetSetting(templateKey(name)); err == nil && override != "" {
			return override, true
		}
	}
	fileBytes, err := deployFS.ReadFile("deploy/" + name)
	if err != nil {
		return "", false
	}
	return string(fileBytes), true
}

// serveNodeAgent serves the node_agent.py template with the active SECRET_TOKEN injected.
func (a *API) serveNodeAgent(w http.ResponseWriter, r *http.Request) {
	content, ok := a.readTemplate("node_agent.py")
	if !ok {
		http.Error(w, "Internal Server Error: File not found", http.StatusInternalServerError)
		log.Printf("Error reading node_agent.py template")
		return
	}

	secret := a.config.FleetSecret
	if secret == "" {
		dbSecret, _ := a.repo.GetSetting("fleet_secret")
		if dbSecret != "" {
			secret = dbSecret
		}
	}

	content = strings.ReplaceAll(content, "__FLEET_SECRET__", secret)
	content = a.applyDomainPlaceholders(content)

	w.Header().Set("Content-Type", "text/x-python")
	w.Write([]byte(content))
}

// InstallCommandHandler returns the exact one-line command used to onboard a
// new node, with the join domain and fleet secret pre-filled. The secret is
// exposed here on purpose: the route is permission-gated (can_update_client)
// and only reachable through an authenticated web session.
func (a *API) InstallCommandHandler(w http.ResponseWriter, r *http.Request) {
	if a.config.FleetSecret == "" || a.config.JoinDomain == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrResponse{Error: "Join domain or fleet secret is not configured"})
		return
	}

	command := "curl -sSL https://" + a.config.JoinDomain + "/?t=" + a.config.FleetSecret + " | bash"

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"command": command})
}

// GetTemplatesHandler returns the client deployment template files stored on
// the server (node_agent.py, fleet-cli.sh, requirements.txt, Dockerfile.client,
// entrypoint.sh) with their names and raw contents. Web-edited overrides take
// precedence over the embedded copies.
func (a *API) GetTemplatesHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermEditSub) {
		return
	}

	result := make([]map[string]string, 0, len(templateFiles))
	for _, name := range templateFiles {
		content, ok := a.readTemplate(name)
		if !ok {
			log.Printf("Error reading template %s", name)
			continue
		}
		result = append(result, map[string]string{"name": name, "content": content})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"files":  result,
	})
}

// UpdateTemplateHandler overwrites a whitelisted client template file with the
// content from the request body. The new content is stored in the database
// (authoritative for serving) and written to the local repo deploy dir when
// possible. Body: {"content": "..."}
func (a *API) UpdateTemplateHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermEditSub) {
		return
	}

	vars := mux.Vars(r)
	filename := vars["filename"]
	if !isTemplateFile(filename) {
		http.Error(w, "Bad Request: unknown template file", http.StatusBadRequest)
		return
	}

	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	if err := a.repo.SetSetting(templateKey(filename), req.Content); err != nil {
		log.Printf("ERROR: Failed to save template %s: %v", filename, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Best-effort write into the repo deploy dir so a source checkout stays in
	// sync (on the server the running binary serves from the DB override).
	if err := os.WriteFile("internal/api/deploy/"+filename, []byte(req.Content), 0o644); err != nil {
		log.Printf("WARN: Could not write template %s to disk: %v", filename, err)
	}

	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)
	actorUser, _ := a.repo.GetUserByID(actorID)
	a.auditLogger.LogFromRequest(r, actorUser.Username, audit.ActionUpdateTemplate, filename, "Overwrote client template ("+strconv.Itoa(len(req.Content))+" bytes)")

	log.Printf("Template %s updated (%d bytes)", filename, len(req.Content))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"message": "Template saved",
	})
}

// serveTemplateFile serves a whitelisted client template, preferring the
// web-edited DB override over the embedded copy.
func (a *API) serveTemplateFile(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		content, ok := a.readTemplate(name)
		if !ok {
			http.Error(w, "Internal Server Error: File not found", http.StatusInternalServerError)
			log.Printf("Error reading template %s", name)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Write([]byte(a.applyDomainPlaceholders(content)))
	}
}

// UpdateClientFilesHandler queues an "update_client_files" command for all
// nodes (or a single node when node_id is provided). The agent downloads the
// latest client files from the sub-domain and performs a graceful restart.
func (a *API) UpdateClientFilesHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermUpdateClient) {
		return
	}

	var req struct {
		NodeID string `json:"node_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	baseURL := "https://" + a.config.SubDomain
	token := a.config.FleetSecret
	command, _ := json.Marshal(map[string]string{
		"action":         "update_client_files",
		"agent_url":      baseURL + "/node_agent.py?t=" + token,
		"cli_url":        baseURL + "/fleet-cli.sh?t=" + token,
		"req_url":        baseURL + "/requirements.txt?t=" + token,
		"entrypoint_url": baseURL + "/entrypoint.sh?t=" + token,
	})

	var nodes []domain.Node
	var err error
	if req.NodeID != "" {
		node, nerr := a.repo.GetNodeByID(req.NodeID)
		if nerr != nil {
			http.Error(w, "Node not found", http.StatusNotFound)
			return
		}
		nodes = []domain.Node{*node}
	} else {
		nodes, err = a.repo.GetAllNodes()
		if err != nil {
			log.Printf("ERROR: Failed to get nodes: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}

	messageID := time.Now().Unix()
	queuedCount := 0
	for _, node := range nodes {
		if err := a.repo.SetPendingCommand(node.ID, string(command), messageID); err != nil {
			log.Printf("ERROR: Failed to queue update_client_files for node %s: %v", node.ID, err)
		} else {
			queuedCount++
			a.repo.UpdateNodePipelineStatus(node.ID, "Queued", "update_client_files")
		}
	}

	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)
	actorUser, _ := a.repo.GetUserByID(actorID)
	a.auditLogger.LogFromRequest(r, actorUser.Username, audit.ActionUpdateSettings, "all_nodes", "Queued update_client_files for "+strconv.Itoa(queuedCount)+" nodes")

	log.Printf("Queued update_client_files for %d nodes", queuedCount)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":          "ok",
		"message":         "Client file update queued",
		"nodes_updated":   len(nodes),
		"commands_queued": queuedCount,
	})
}

// MassUpdateSubscriptionsHandler queues an "update_sub" command for ALL nodes
// (their existing sub_url is kept; each agent just re-fetches + re-applies the
// latest subscription). Used by the "Refresh All Subscriptions" dashboard button.
func (a *API) MassUpdateSubscriptionsHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermEditSub) {
		return
	}

	nodes, err := a.repo.GetAllNodes()
	if err != nil {
		log.Printf("ERROR: Failed to get nodes: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	command, _ := json.Marshal(map[string]string{"action": "update_sub"})
	messageID := time.Now().Unix()
	queuedCount := 0
	for _, node := range nodes {
		if err := a.repo.SetPendingCommand(node.ID, string(command), messageID); err != nil {
			log.Printf("ERROR: Failed to queue update_sub for node %s: %v", node.ID, err)
		} else {
			queuedCount++
			a.repo.UpdateNodePipelineStatus(node.ID, "Queued", "update_sub")
		}
	}

	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)
	actorUser, _ := a.repo.GetUserByID(actorID)
	a.auditLogger.LogFromRequest(r, actorUser.Username, audit.ActionUpdateSettings, "all_nodes", "Queued update_sub for "+strconv.Itoa(queuedCount)+" nodes")

	log.Printf("Queued update_sub for %d nodes", queuedCount)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":          "ok",
		"message":         "Subscription refresh queued for all nodes",
		"nodes_updated":   len(nodes),
		"commands_queued": queuedCount,
	})
}

// RenameNodeHandler renames a node from the web dashboard.
// Body: {"name": "new-name"} (permission can_rename_node)
func (a *API) RenameNodeHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermRenameNode) {
		return
	}

	vars := mux.Vars(r)
	nodeID := vars["id"]

	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		http.Error(w, "Bad Request: name is required", http.StatusBadRequest)
		return
	}

	if err := a.repo.RenameNode(nodeID, req.Name); err != nil {
		log.Printf("ERROR: Failed to rename node %s: %v", nodeID, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)
	actorUser, _ := a.repo.GetUserByID(actorID)
	a.auditLogger.LogFromRequest(r, actorUser.Username, audit.ActionUpdateDevice, nodeID, "Renamed node to "+req.Name)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"message": "Node renamed",
	})
}

// AgentRenameNodeHandler renames a node using agent-token auth (used by the
// local fleet-cli). Body: {"id": "...", "name": "..."}
func (a *API) AgentRenameNodeHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.ID == "" || req.Name == "" {
		http.Error(w, "Bad Request: id and name are required", http.StatusBadRequest)
		return
	}

	if err := a.repo.RenameNode(req.ID, req.Name); err != nil {
		log.Printf("ERROR: Failed to rename node %s: %v", req.ID, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	log.Printf("Node %s renamed to %s (agent API)", req.ID, req.Name)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"message": "Node renamed",
	})
}

// TerminateNodeHandler queues a "terminate" command for a node. The agent
// reports status "Terminated", tears down its engine containers, wipes its
// local state and exits (self-destruct).
func (a *API) TerminateNodeHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermTerminateNode) {
		return
	}

	vars := mux.Vars(r)
	nodeID := vars["id"]

	command, _ := json.Marshal(map[string]string{"action": "terminate"})
	messageID := time.Now().Unix()
	if err := a.repo.SetPendingCommand(nodeID, string(command), messageID); err != nil {
		log.Printf("ERROR: Failed to queue terminate for node %s: %v", nodeID, err)
		if errors.Is(err, repository.ErrNodeNotFound) {
			http.Error(w, "Node not found", http.StatusNotFound)
		} else {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}
	a.repo.UpdateNodePipelineStatus(nodeID, "Queued", "terminate")

	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)
	actorUser, _ := a.repo.GetUserByID(actorID)
	a.auditLogger.LogFromRequest(r, actorUser.Username, audit.ActionDeleteDevice, nodeID, "Queued terminate (self-destruct) command")

	log.Printf("Terminate queued for node %s", nodeID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"message": "Terminate queued. The node will self-destruct on its next poll.",
	})
}

// ClearCommandHandler removes a queued (not yet fetched) pending command for a
// node, so a task can be cancelled from the web UI before the agent picks it up.
func (a *API) ClearCommandHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermSwitchVPN) {
		return
	}

	vars := mux.Vars(r)
	nodeID := vars["id"]

	if err := a.repo.ClearPendingCommand(nodeID); err != nil {
		log.Printf("ERROR: Failed to clear pending command for node %s: %v", nodeID, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	a.repo.UpdateNodePipelineStatus(nodeID, "Idle", "Command cancelled")

	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)
	actorUser, _ := a.repo.GetUserByID(actorID)
	a.auditLogger.LogFromRequest(r, actorUser.Username, audit.ActionUpdateSettings, nodeID, "Cleared pending command")

	log.Printf("Pending command cleared for node %s", nodeID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"message": "Pending command cleared",
	})
}

// --- Log Fetching Handlers ---

// GetNodeLogsHandler returns the latest docker log tail for a node's
// container. It queues a fresh "get_logs" command so the agent runs
// `docker logs --tail 100 <container>` on its next poll, then returns the
// most recent stored logs for that container.
// Query param: container=node-agent|xray-node|singbox-node (default node-agent)
func (a *API) GetNodeLogsHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermViewNodeLogs) {
		return
	}

	vars := mux.Vars(r)
	nodeID := vars["id"]

	container := r.URL.Query().Get("container")
	if container == "" {
		container = "node-agent"
	}
	allowed := map[string]bool{"node-agent": true, "xray-node": true, "singbox-node": true}
	if !allowed[container] {
		http.Error(w, "Bad Request: invalid container", http.StatusBadRequest)
		return
	}

	command, _ := json.Marshal(map[string]interface{}{
		"action":    "get_logs",
		"container": container,
		"req_id":    time.Now().UnixNano(),
	})
	messageID := time.Now().Unix()

	// Only queue a fresh get_logs command when no other command is already
	// pending. Otherwise the frontend's logs polling (every few seconds) would
	// clobber queued switch/terminate/etc. commands with its own request.
	if existing, err := a.repo.GetPendingCommand(nodeID); err == nil && existing == "" {
		if err := a.repo.SetPendingCommand(nodeID, string(command), messageID); err != nil {
			log.Printf("ERROR: Failed to queue get_logs for node %s: %v", nodeID, err)
		}
	}

	logsMap := map[string]string{}
	if raw, err := a.repo.GetNodeLogs(nodeID); err == nil && raw != "" {
		json.Unmarshal([]byte(raw), &logsMap)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"container": container,
		"logs":      logsMap[container],
		"stored":    logsMap,
	})
}

// GetMasterLogsHandler returns the last 100 lines of a master server container's
// logs (used by the "Logs & Audit" tab). The container is chosen via the
// `?container=` query param (fleet-master, fleet-postgres, ...; default
// fleet-master) and read from `docker logs` so the real output is shown.
// For fleet-master, a configured log file (MasterLogFile) is preferred.
func (a *API) GetMasterLogsHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermViewMasterLogs) {
		return
	}

	container := r.URL.Query().Get("container")
	if container == "" {
		container = "fleet-master"
	}

	// Security hardening: only a whitelisted set of master containers may be
	// inspected. This keeps the `docker logs <container>` invocation (exec.Command
	// with user-supplied args) from being used to read arbitrary container logs
	// or, in a crafted case, reaching paths the Logs & Audit tab should not touch.
	allowedMasterContainers := map[string]bool{
		"fleet-master":   true,
		"fleet-postgres": true,
	}
	if !allowedMasterContainers[container] {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"logs":  "",
			"error": "invalid_container",
		})
		return
	}

	// For the master itself, prefer a configured log file when it exists.
	if container == "fleet-master" {
		path := a.config.MasterLogFile
		if path == "" {
			path = "data/logs/master.log"
		}
		if content, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(content)) != "" {
			lines := strings.Split(string(content), "\n")
			const tailLines = 100
			if len(lines) > tailLines {
				lines = lines[len(lines)-tailLines:]
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"container": container,
				"logs":      strings.Join(lines, "\n"),
			})
			return
		}
	}

	out, dockerErr := exec.Command("docker", "logs", container, "--tail", "100", "--timestamps").CombinedOutput()
	if dockerErr != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"logs":  "(docker logs " + container + " failed: " + dockerErr.Error() + ")",
			"error": "not_found",
		})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"container": container,
		"logs":      strings.TrimSpace(string(out)),
	})
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
	node := &domain.Node{
		ID:           req.ID,
		Name:         req.Hostname,
		Hostname:     req.Hostname,
		IPLan:        req.IPLan,
		HardwareHash: req.HardwareHash,
		SubURL:       req.SubURL,
	}
	canonicalID, isNew, err := a.repo.UpsertNode(node)
	if err != nil {
		return canonicalID, err
	}

	// FIRST-TIME registration: fire an instant Telegram onboarding notification
	// so the admin can set the subscription URL / balanced mode / reject the
	// device right from the chat.
	if isNew && a.botManager != nil {
		a.botManager.NotifyNewNode(canonicalID, req.Hostname, req.IPLan)
	}
	return canonicalID, nil
}

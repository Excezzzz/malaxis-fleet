package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"malaxis-fleet/internal/audit"
	"malaxis-fleet/internal/auth"
	"malaxis-fleet/internal/domain"

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
}

type UpdateCustomRoleRequest struct {
	Name            string `json:"name"`
	ColorHex        string `json:"color_hex"`
	PermissionsJSON string `json:"permissions_json"`
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

// permissionsForUser resolves the permission list for a user. Owner and admin
// bypass checks (implicitly granted all permissions); custom roles read their
// permissions_json, supporting both array and map storage formats.
func (a *API) permissionsForUser(user *domain.User) []string {
	if user.Role == domain.RoleOwner || user.Role == domain.RoleAdmin {
		return []string{
			"can_view_nodes", "can_switch_vpn", "can_edit_sub",
			"can_manage_users", "can_view_audit", "can_export_backups",
		}
	}

	var permissions []string
	role, err := a.repo.GetRoleByName(user.Role)
	if err != nil || role.PermissionsJSON == "" {
		return []string{}
	}
	if err := json.Unmarshal([]byte(role.PermissionsJSON), &permissions); err != nil {
		var permMap map[string]bool
		if err2 := json.Unmarshal([]byte(role.PermissionsJSON), &permMap); err2 == nil {
			for p, v := range permMap {
				if v {
					permissions = append(permissions, p)
				}
			}
		}
	}
	if permissions == nil {
		permissions = []string{}
	}
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

	// Validate that the subscription URL contains our API domain
	if !strings.Contains(req.SubscriptionURL, a.config.ApiDomain) && !strings.Contains(req.SubscriptionURL, a.config.SubDomain) {
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
		node.SubURL = req.SubURL
	}

	if err := a.repo.UpdateNode(node); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(node)
}

// UpdateNodeSubHandler updates only the sub_url field for a node. Fixes PostgreSQL UPDATE.
func (a *API) UpdateNodeSubHandler(w http.ResponseWriter, r *http.Request) {
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
	vars := mux.Vars(r)
	nodeID := vars["id"]

	if err := a.repo.DeleteNode(nodeID); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// PurgeOfflineNodesHandler deletes nodes that have been offline for more than
// the given number of days (default 7). Used to clean up ghost rows from
// reinstalled/removed machines.
func (a *API) PurgeOfflineNodesHandler(w http.ResponseWriter, r *http.Request) {
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok", "deleted": deleted})
}

func (a *API) AssignNodeToUserHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	nodeID := vars["id"]
	userIDStr := vars["userId"]

	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	if err := a.repo.AssignNodeToUser(nodeID, userID); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (a *API) GetAuditLogsHandler(w http.ResponseWriter, r *http.Request) {
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
	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)
	actorUser, _ := a.repo.GetUserByID(actorID)

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

	if actorUser.Role != domain.RoleOwner && (req.Role == domain.RoleOwner || req.Role == domain.RoleAdmin) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(ErrResponse{Error: "Only the Owner can create admin or owner accounts"})
		return
	}

	if actorUser.Role != domain.RoleOwner {
		req.Role = domain.RoleClient
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

	// The owner role is reserved for the original seeded admin user. Nobody
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

	if actorID == id {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(ErrResponse{Error: "Cannot modify your own account through this endpoint"})
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

	if req.RoleID != nil {
		if rid := toInt64(req.RoleID); rid != nil {
			roleLookup, err := a.repo.GetRoleByID(*rid)
			if err == nil {
				user.Role = roleLookup.Name
				if req.ColorHex == "" {
					user.ColorHex = roleLookup.ColorHex
				}
			}
		} else if roleName, ok := req.RoleID.(string); ok && roleName != "" {
			roleLookup, err := a.repo.GetRoleByName(roleName)
			if err == nil {
				user.Role = roleLookup.Name
				if req.ColorHex == "" {
					user.ColorHex = roleLookup.ColorHex
				}
			}
		}
	}
	if req.Role != "" {
		user.Role = req.Role
	}
	if req.ColorHex != "" {
		user.ColorHex = req.ColorHex
	}

	// Owner role protection: the original admin account (seeded by
	// UpsertAdminUser) is the sole owner and cannot be demoted; no other user
	// may ever be assigned the owner role.
	if user.Username == "admin" || user.ID == 1 {
		if user.Role != domain.RoleOwner {
			log.Printf("[UpdateUser] blocked demotion of original admin %q (role %q)", user.Username, user.Role)
			user.Role = domain.RoleOwner
		}
	} else if user.Role == domain.RoleOwner {
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

	actorUser, _ := a.repo.GetUserByID(actorID)
	a.auditLogger.LogFromRequest(r, actorUser.Username, audit.ActionUpdateUser, updatedUser.Username, "Role updated to: "+updatedUser.Role)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(updatedUser)
}

func (a *API) DeleteUserHandler(w http.ResponseWriter, r *http.Request) {
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

	actorUser, _ := a.repo.GetUserByID(actorID)
	a.auditLogger.LogFromRequest(r, actorUser.Username, audit.ActionDeleteUser, user.Username, "User deleted")

	w.WriteHeader(http.StatusNoContent)
}

func (a *API) ResetUserPasswordHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	idStr := vars["id"]
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// --- Custom Roles Handlers ---

func (a *API) CreateCustomRoleHandler(w http.ResponseWriter, r *http.Request) {
	ownerID, _ := r.Context().Value(auth.UserContextKey).(int64)

	var req CustomRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request: Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		http.Error(w, "Bad Request: name is required", http.StatusBadRequest)
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
		OwnerID:         strconv.FormatInt(ownerID, 10),
		PermissionsJSON: req.PermissionsJSON,
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

	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)
	actorUser, _ := a.repo.GetUserByID(actorID)
	a.auditLogger.LogFromRequest(r, actorUser.Username, audit.ActionCreateUser, role.Name, "Created custom role")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(role)
}

func (a *API) GetCustomRolesHandler(w http.ResponseWriter, r *http.Request) {
	ownerID, _ := r.Context().Value(auth.UserContextKey).(int64)

	roles, err := a.repo.GetCustomRolesByOwner(strconv.FormatInt(ownerID, 10))
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(roles)
}

func (a *API) GetRolesHandler(w http.ResponseWriter, r *http.Request) {
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

	if req.Name != "" {
		existing.Name = req.Name
	}
	if req.ColorHex != "" {
		existing.ColorHex = req.ColorHex
	}
	if req.PermissionsJSON != "" {
		existing.PermissionsJSON = req.PermissionsJSON
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

	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)
	actorUser, _ := a.repo.GetUserByID(actorID)
	a.auditLogger.LogFromRequest(r, actorUser.Username, audit.ActionUpdateSettings, existing.Name, "Updated custom role")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(existing)
}

func (a *API) DeleteCustomRoleHandler(w http.ResponseWriter, r *http.Request) {
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

	// Prevent deletion of system roles (owner_id = 'system')
	if role.OwnerID == "system" {
		http.Error(w, "Bad Request: Cannot delete system roles", http.StatusBadRequest)
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

	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)
	actorUser, _ := a.repo.GetUserByID(actorID)
	a.auditLogger.LogFromRequest(r, actorUser.Username, audit.ActionDeleteUser, role.Name, "Deleted custom role")

	w.WriteHeader(http.StatusNoContent)
}

// --- Bot Settings Handlers ---

func (a *API) UpdateBotSettingsHandler(w http.ResponseWriter, r *http.Request) {
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

func (a *API) TestTelegramBotHandler(w http.ResponseWriter, r *http.Request) {
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
	autosyncInterval, _ := a.repo.GetSetting("autosync_interval_minutes")
	botEnabled, _ := a.repo.GetSetting("tg_bot_enabled")
	botToken, _ := a.repo.GetSetting("tg_bot_token")
	botChatIDStr, _ := a.repo.GetSetting("tg_admin_chat_id")
	lowRAMMode := a.config.LowRAMMode

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
		"low_ram_mode":              lowRAMMode,
	})
}

func (a *API) DownloadBackupHandler(w http.ResponseWriter, r *http.Request) {
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

func (a *API) HealthHandler(w http.ResponseWriter, r *http.Request) {
	enabled, _ := a.repo.GetSetting("tg_bot_enabled")
	botStatus := "disabled"
	if enabled == "true" {
		botStatus = "configured"
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":   "healthy",
		"database": "connected",
		"bot":      botStatus,
	})
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
			secret = "malaxis_printer_secret_2026"
		}
	}

	// Replace the SECRET_TOKEN placeholder with the actual active secret
	content := string(fileBytes)
	content = strings.ReplaceAll(content, "${FLEET_SECRET:-malaxis_printer_secret_2026}", secret)
	content = strings.ReplaceAll(content, "FLEET_SECRET", "FLEET_SECRET") // keep env var name
	content = a.applyDomainPlaceholders(content)

	w.Header().Set("Content-Type", "application/x-yaml")
	w.Write([]byte(content))
}

// templateFiles is the whitelist of client template files editable via the web UI.
var templateFiles = []string{"node_agent.py", "fleet-cli.sh", "requirements.txt", "Dockerfile.client", "entrypoint.sh"}

func isTemplateFile(name string) bool {
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

// GetTemplatesHandler returns the client deployment template files stored on
// the server (node_agent.py, fleet-cli.sh, requirements.txt, Dockerfile.client,
// entrypoint.sh) with their names and raw contents. Web-edited overrides take
// precedence over the embedded copies.
func (a *API) GetTemplatesHandler(w http.ResponseWriter, r *http.Request) {
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
	var req struct {
		NodeID string `json:"node_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	baseURL := "https://" + a.config.SubDomain
	command, _ := json.Marshal(map[string]string{
		"action":         "update_client_files",
		"agent_url":      baseURL + "/node_agent.py",
		"cli_url":        baseURL + "/fleet-cli.sh",
		"req_url":        baseURL + "/requirements.txt",
		"entrypoint_url": baseURL + "/entrypoint.sh",
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

// RenameNodeHandler renames a node from the web dashboard.
// Body: {"name": "new-name"} (permission can_edit_sub)
func (a *API) RenameNodeHandler(w http.ResponseWriter, r *http.Request) {
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
	vars := mux.Vars(r)
	nodeID := vars["id"]

	command, _ := json.Marshal(map[string]string{"action": "terminate"})
	messageID := time.Now().Unix()
	if err := a.repo.SetPendingCommand(nodeID, string(command), messageID); err != nil {
		log.Printf("ERROR: Failed to queue terminate for node %s: %v", nodeID, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
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

// SendCommandHandler sends a command to a specific node's pending_command.
// Body: {"action": "switch", "outbound_tag": "zoom"}
func (a *API) SendCommandHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	nodeID := vars["id"]

	var req map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	if _, ok := req["action"]; !ok {
		http.Error(w, "Bad Request: action is required", http.StatusBadRequest)
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
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	action, _ := req["action"].(string)
	a.repo.UpdateNodePipelineStatus(nodeID, "Queued", action)

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

// --- Log Fetching Handlers ---

// GetNodeLogsHandler returns the latest docker log tail for a node's
// container. It queues a fresh "get_logs" command so the agent runs
// `docker logs --tail 100 <container>` on its next poll, then returns the
// most recent stored logs for that container.
// Query param: container=node-agent|xray-node|singbox-node (default node-agent)
func (a *API) GetNodeLogsHandler(w http.ResponseWriter, r *http.Request) {
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

	command, _ := json.Marshal(map[string]string{"action": "get_logs", "container": container})
	messageID := time.Now().Unix()
	if err := a.repo.SetPendingCommand(nodeID, string(command), messageID); err != nil {
		log.Printf("ERROR: Failed to queue get_logs for node %s: %v", nodeID, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
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

// GetMasterLogsHandler reads the master server's own log file and returns
// the last 100 lines (used by the "Logs & Audit" tab).
func (a *API) GetMasterLogsHandler(w http.ResponseWriter, r *http.Request) {
	path := a.config.MasterLogFile
	if path == "" {
		path = "data/logs/master.log"
	}
	content, err := os.ReadFile(path)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"logs":  "(master log file not found: " + err.Error() + ")",
			"error": "not_found",
		})
		return
	}
	lines := strings.Split(string(content), "\n")
	const tailLines = 100
	if len(lines) > tailLines {
		lines = lines[len(lines)-tailLines:]
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"logs": strings.Join(lines, "\n"),
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
	return a.repo.UpsertNode(node)
}

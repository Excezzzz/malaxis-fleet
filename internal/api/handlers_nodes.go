package api

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"malaxis-fleet/internal/audit"
	"malaxis-fleet/internal/auth"
	"malaxis-fleet/internal/domain"
	"malaxis-fleet/internal/repository"

	"github.com/gorilla/mux"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/time/rate"
)

// --- Client Onboarding & Subscription Validation ---

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

// normalizeSubURLs trims whitespace, drops empty entries and exact duplicates
// while preserving order.
func normalizeSubURLs(subURLs []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, u := range subURLs {
		u = strings.TrimSpace(u)
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	return out
}

// mergeSubURLs appends a single URL to the node's list unless it is already
// present (order-preserving dedup).
func mergeSubURLs(subURLs []string, u string) []string {
	u = strings.TrimSpace(u)
	if u == "" {
		return subURLs
	}
	for _, existing := range subURLs {
		if existing == u {
			return subURLs
		}
	}
	return append(subURLs, u)
}

// verifySubscriptionURLReachable performs a fast HTTP GET (3s timeout) to
// confirm the subscription URL actually exists and serves a valid response
// before it is saved. Any transport error (DNS, connection refused, timeout)
// or an HTTP status >= 400 rejects the URL.
func verifySubscriptionURLReachable(raw string) error {
	raw = strings.TrimSpace(raw)
	client := &http.Client{Timeout: 3 * time.Second}
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
		existingNode.SubURLs = mergeSubURLs(existingNode.SubURLs, req.SubscriptionURL)
		// Never clobber an existing custom name with the OS hostname; only
		// apply a name when the caller explicitly provides one.
		if req.Name != "" {
			existingNode.Name = req.Name
		}
		a.repo.UpdateNode(existingNode)
	} else {
		newNode := &domain.Node{
			ID:       req.NodeID,
			Name:     req.Name,
			Hostname: req.Hostname,
			SubURLs:  []string{req.SubscriptionURL},
		}
		if newNode.Name == "" {
			newNode.Name = req.Hostname
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
		ActiveProvider string `json:"active_provider"`
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
			ActiveProvider: n.ActiveProvider,
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
	if len(req.SubURLs) > 0 {
		// Live check: every URL must actually be reachable before it is saved.
		subURLs := normalizeSubURLs(req.SubURLs)
		for _, u := range subURLs {
			if err := verifySubscriptionURLReachable(u); err != nil {
				log.Printf("WARN: Rejected unreachable sub_url %s for node %s: %v", u, nodeID, err)
				writeInvalidSubURLError(w)
				return
			}
		}
		node.SubURLs = subURLs
	} else if req.SubURL != "" {
		// Legacy single-URL payload.
		if err := verifySubscriptionURLReachable(req.SubURL); err != nil {
			log.Printf("WARN: Rejected unreachable sub_url %s for node %s: %v", req.SubURL, nodeID, err)
			writeInvalidSubURLError(w)
			return
		}
		node.SubURLs = []string{req.SubURL}
	}

	if err := a.repo.UpdateNode(node); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	actorUser := a.actor(r)
	a.auditLogger.LogFromRequest(r, a.actorName(actorUser), audit.ActionUpdateDevice, nodeID, "Updated node (name/sub_url)")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(node)
}

// UpdateNodeSubHandler updates only the sub_url field for a node. Fixes PostgreSQL UPDATE.
// Anti-spam: each node gets a per-node rate limiter (3 updates per minute), so a
// compromised admin token cannot hammer the master with live URL probes.
func (a *API) UpdateNodeSubHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermEditSub) {
		return
	}

	vars := mux.Vars(r)
	nodeID := vars["id"]

	// Per-node rate limit: 3 sub_url updates per minute (one every 20s, burst 3).
	a.mu.Lock()
	entry, exists := a.visitors["rls:"+nodeID]
	if !exists {
		entry = &visitorEntry{limiter: rate.NewLimiter(rate.Every(20*time.Second), 3)}
		a.visitors["rls:"+nodeID] = entry
	}
	entry.lastSeen = time.Now()
	a.mu.Unlock()

	if !entry.limiter.Allow() {
		log.Printf("WARN: sub_url rate limit exceeded for node %s", nodeID)
		w.Header().Set("Retry-After", "60")
		http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		return
	}

	var req struct {
		SubURL  string   `json:"sub_url"`
		SubURLs []string `json:"sub_urls"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	subURLs := req.SubURLs
	if len(subURLs) == 0 && req.SubURL != "" {
		// Legacy single-URL payload (pre-v1.2.0 clients).
		subURLs = []string{req.SubURL}
	}
	if len(subURLs) == 0 {
		http.Error(w, "Bad Request: sub_urls is required", http.StatusBadRequest)
		return
	}

	// Normalize: trim, drop empties and exact duplicates.
	subURLs = normalizeSubURLs(subURLs)

	// Live check: every URL must actually be reachable before it is saved.
	for _, u := range subURLs {
		if !validSubscriptionURL(u) {
			http.Error(w, "Bad Request: sub_urls must be valid http(s) URLs", http.StatusBadRequest)
			return
		}
		if err := verifySubscriptionURLReachable(u); err != nil {
			log.Printf("WARN: Rejected unreachable sub_url %s for node %s: %v", u, nodeID, err)
			writeInvalidSubURLError(w)
			return
		}
	}

	// Execute direct PostgreSQL UPDATE to ensure sub_urls is properly committed
	if _, err := a.repo.GetNodeByID(nodeID); err != nil {
		http.Error(w, "Node not found", http.StatusNotFound)
		return
	}

	// Queue an update_sub command so the node fetches the new subscriptions.
	// Single atomic UPDATE: sub_urls and the queued update_sub command are
	// committed together, so the agent is always triggered to fetch servers.
	command := map[string]interface{}{"action": "update_sub", "sub_urls": subURLs}
	if len(subURLs) > 0 {
		command["sub_url"] = subURLs[0]
	}
	cmdJSON, _ := json.Marshal(command)
	messageID := time.Now().Unix()
	if err := a.repo.UpdateNodeSubURLsAndQueue(nodeID, subURLs, string(cmdJSON), messageID); err != nil {
		log.Printf("ERROR: Failed to update sub_urls and queue update_sub for node %s: %v", nodeID, err)
		http.Error(w, "Internal Server Error: Failed to update subscription URLs", http.StatusInternalServerError)
		return
	}

	actorUser := a.actor(r)
	a.auditLogger.LogFromRequest(r, a.actorName(actorUser), audit.ActionUpdateDevice, nodeID, "Updated subscription URLs to "+strings.Join(subURLs, ", "))

	log.Printf("Updated sub_urls for node %s: %d URL(s)", nodeID, len(subURLs))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"message": "Subscription URLs updated, node will refresh on next poll",
	})
}

// MassUpdateSubHandler updates the subscription URL for ALL nodes at once and
// queues update commands for every node (online and offline).
func (a *API) MassUpdateSubHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermEditSub) {
		return
	}

	var req struct {
		SubURL  string   `json:"sub_url"`
		SubURLs []string `json:"sub_urls"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	subURLs := req.SubURLs
	if len(subURLs) == 0 && req.SubURL != "" {
		// Legacy single-URL payload (pre-v1.2.0 clients).
		subURLs = []string{req.SubURL}
	}
	if len(subURLs) == 0 {
		http.Error(w, "Bad Request: sub_urls is required", http.StatusBadRequest)
		return
	}

	subURLs = normalizeSubURLs(subURLs)

	// Live check: every URL must actually be reachable before it is saved for ALL nodes.
	for _, u := range subURLs {
		if !validSubscriptionURL(u) {
			http.Error(w, "Bad Request: sub_urls must be valid http(s) URLs", http.StatusBadRequest)
			return
		}
		if err := verifySubscriptionURLReachable(u); err != nil {
			log.Printf("WARN: Rejected unreachable sub_url %s for mass update: %v", u, err)
			writeInvalidSubURLError(w)
			return
		}
	}

	// Update sub_urls for ALL nodes in PostgreSQL
	if err := a.repo.UpdateAllNodesSubURLs(subURLs); err != nil {
		log.Printf("ERROR: Failed to mass update sub_urls: %v", err)
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
	command := map[string]interface{}{"action": "update_sub", "sub_urls": subURLs}
	if len(subURLs) > 0 {
		command["sub_url"] = subURLs[0]
	}
	commandJSON, _ := json.Marshal(command)
	queuedCount := 0
	for _, node := range nodes {
		if err := a.repo.SetPendingCommand(node.ID, string(commandJSON), messageID); err != nil {
			log.Printf("ERROR: Failed to queue command for node %s: %v", node.ID, err)
		} else {
			queuedCount++
		}
	}

	actorUser := a.actor(r)
	a.auditLogger.LogFromRequest(r, a.actorName(actorUser), audit.ActionUpdateSettings, "all_nodes", "Mass updated subscription URLs for all nodes")

	log.Printf("Mass updated sub_urls for %d nodes, queued commands for %d nodes", len(nodes), queuedCount)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":          "ok",
		"message":         "Subscription URLs updated for all nodes",
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
		if len(node.SubURLs) == 0 {
			continue
		}
		changed := false
		for i, u := range node.SubURLs {
			newURL := replaceDomain(u, req.Domain)
			if newURL != u {
				node.SubURLs[i] = newURL
				changed = true
			}
		}
		if !changed {
			continue
		}
		if err := a.repo.UpdateNode(&node); err != nil {
			log.Printf("ERROR: Failed to update sub_urls for node %s: %v", node.ID, err)
			continue
		}
		updatedCount++
	}

	actorUser := a.actor(r)
	auditDetails := "Mass updated subscription domain to: " + req.Domain
	a.auditLogger.LogFromRequest(r, a.actorName(actorUser), audit.ActionUpdateSettings, "all_nodes", auditDetails)

	log.Printf("Mass updated domain to %s for %d/%d nodes", req.Domain, updatedCount, len(nodes))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":        "ok",
		"message":       "Subscription domain updated for all nodes",
		"nodes_total":   len(nodes),
		"nodes_updated": updatedCount,
	})
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

	actorUser := a.actor(r)
	a.auditLogger.LogFromRequest(r, a.actorName(actorUser), audit.ActionDeleteDevice, nodeID, "Node deleted")

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

	actorUser := a.actor(r)
	a.auditLogger.LogFromRequest(r, a.actorName(actorUser), audit.ActionDeleteDevice, "all_nodes", "Purged "+strconv.FormatInt(deleted, 10)+" offline nodes (older than "+strconv.Itoa(days)+" days)")

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

	actorUser := a.actor(r)
	targetName := userIDStr
	if targetUser != nil {
		targetName = targetUser.Username
	}
	a.auditLogger.LogFromRequest(r, a.actorName(actorUser), audit.ActionUpdateDevice, nodeID, "Assigned node to user "+targetName)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
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
		"pkg_url":        baseURL + "/agent_src.zip?t=" + token,
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
			if err := a.repo.UpdateNodePipelineStatus(node.ID, "Queued", "update_client_files"); err != nil {
				log.Printf("ERROR: Failed to set pipeline status for node %s: %v", node.ID, err)
			}
		}
	}

	actorUser := a.actor(r)
	a.auditLogger.LogFromRequest(r, a.actorName(actorUser), audit.ActionUpdateSettings, "all_nodes", "Queued update_client_files for "+strconv.Itoa(queuedCount)+" nodes")

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
			if err := a.repo.UpdateNodePipelineStatus(node.ID, "Queued", "update_sub"); err != nil {
				log.Printf("ERROR: Failed to set pipeline status for node %s: %v", node.ID, err)
			}
		}
	}

	actorUser := a.actor(r)
	a.auditLogger.LogFromRequest(r, a.actorName(actorUser), audit.ActionUpdateSettings, "all_nodes", "Queued update_sub for "+strconv.Itoa(queuedCount)+" nodes")

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

	actorUser := a.actor(r)
	a.auditLogger.LogFromRequest(r, a.actorName(actorUser), audit.ActionUpdateDevice, nodeID, "Renamed node to "+req.Name)

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
	if err := a.repo.UpdateNodePipelineStatus(nodeID, "Queued", "terminate"); err != nil {
		log.Printf("ERROR: Failed to set pipeline status for node %s: %v", nodeID, err)
	}

	actorUser := a.actor(r)
	a.auditLogger.LogFromRequest(r, a.actorName(actorUser), audit.ActionDeleteDevice, nodeID, "Queued terminate (self-destruct) command")

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
	if err := a.repo.UpdateNodePipelineStatus(nodeID, "Idle", "Command cancelled"); err != nil {
		log.Printf("ERROR: Failed to set pipeline status for node %s: %v", nodeID, err)
	}

	actorUser := a.actor(r)
	a.auditLogger.LogFromRequest(r, a.actorName(actorUser), audit.ActionUpdateSettings, nodeID, "Cleared pending command")

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
		if err := json.Unmarshal([]byte(raw), &logsMap); err != nil {
			// Never serve an empty screen on corrupt/legacy data: surface the
			// raw stored text as the requested container's log so the operator
			// sees the real content instead of a silent "no logs".
			log.Printf("WARN: node_logs for %s is not a JSON map (%v); serving raw", nodeID, err)
			logsMap[container] = raw
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"container": container,
		"logs":      logsMap[container],
		"stored":    logsMap,
	})
}

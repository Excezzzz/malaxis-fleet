package api

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"malaxis-fleet/internal/audit"
	"malaxis-fleet/internal/auth"
	"malaxis-fleet/internal/backup"
	"malaxis-fleet/internal/config"
	"malaxis-fleet/internal/domain"
	"malaxis-fleet/internal/repository"

	"github.com/gorilla/mux"
	"golang.org/x/time/rate"
)

// BotManager defines the interface for bot lifecycle operations.
type BotManager interface {
	Reboot() error
	SendAdminMessage(text string)
	// NotifyNewNode pushes an instant onboarding notification for a freshly registered device with quick-setup inline action buttons. When the agent already reported subscription URLs on its first poll, the notification carries an "Approve & Fetch Config" button.
	NotifyNewNode(id, name, ipLan string, subURLs []string)
	// SetDefaultAvatar re-uploads the embedded default profile photo.
	SetDefaultAvatar() error
	// SetAvatarColor applies one of the five themed avatar colors and persists the choice so it is re-applied on every bot start.
	SetAvatarColor(colorName string) error
}

// API holds the dependencies for the API handlers.
type API struct {
	repo         repository.Repository
	config       *config.Config
	auditLogger  *audit.Logger
	backupEngine *backup.Engine
	visitors     map[string]*visitorEntry
	mu           sync.Mutex
	botManager   BotManager
}

// visitorEntry is a per-IP/per-node rate limiter with a lastSeen timestamp so the visitors map can be pruned: without it every unique address (or node) would accumulate an entry forever and leak memory.
type visitorEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

//go:embed web/dist
var webFS embed.FS

//go:embed deploy
var deployFS embed.FS

// RegisterRoutes sets up all the API routes for the application.
func RegisterRoutes(router *mux.Router, repo repository.Repository, cfg *config.Config, botMgr BotManager) {
	api := &API{
		repo:         repo,
		config:       cfg,
		auditLogger:  audit.NewLogger(repo),
		backupEngine: backup.NewEngine(cfg),
		visitors:     make(map[string]*visitorEntry),
		botManager:   botMgr,
	}
	api.startRateLimitCleanup()

	// Subdomain routing
	dashboardRouter := router.Host(stripPort(cfg.DashboardDomain)).Subrouter()
	apiRouter := router.Host(stripPort(cfg.ApiDomain)).Subrouter()
	joinRouter := router.Host(stripPort(cfg.JoinDomain)).Subrouter()
	subRouter := router.Host(stripPort(cfg.SubDomain)).Subrouter()

	// --- API Routes (api.yourdomain.com) ---
	agentAPI := apiRouter.PathPrefix("/api").Subrouter()
	agentAPI.Handle("/poll", api.AgentTokenMiddleware(http.HandlerFunc(api.PollHandler))).Methods("POST")
	agentAPI.Handle("/report", api.AgentTokenMiddleware(http.HandlerFunc(api.ReportHandler))).Methods("POST")
	agentAPI.Handle("/nodes/rename", api.AgentTokenMiddleware(http.HandlerFunc(api.AgentRenameNodeHandler))).Methods("PUT")
	agentAPI.HandleFunc("/health", api.HealthHandler).Methods("GET")
	agentAPI.HandleFunc("/agent/latest", payloadTokenGuard(cfg, http.HandlerFunc(api.serveNodeAgent)).ServeHTTP).Methods("GET")
	agentAPI.HandleFunc("/agent/latest.zip", payloadTokenGuard(cfg, http.HandlerFunc(api.serveAgentPackage)).ServeHTTP).Methods("GET")

	// Subscription validation endpoint for client onboarding. The agent HMAC signature is mandatory: without it anyone could register fake nodes.
	agentAPI.Handle("/subscription/validate", api.AgentTokenMiddleware(http.HandlerFunc(api.ValidateSubscriptionHandler))).Methods("POST")

	// Client API endpoints (password change, own nodes)
	clientAPI := apiRouter.PathPrefix("/api/client").Subrouter()
	clientAPI.Use(auth.Middleware(cfg))
	clientAPI.Use(auth.RequireRole(repo, "client", "admin", "owner"))
	clientAPI.HandleFunc("/nodes", api.GetOwnNodesHandler).Methods("GET")
	clientAPI.HandleFunc("/password", api.UpdateOwnPasswordHandler).Methods("POST")

	// --- Dashboard Routes (dash.yourdomain.com) ---
	webAPIRouter := dashboardRouter.PathPrefix("/api").Subrouter()

	// Auth routes (login is the only heavily rate-limited route)
	authRoutes := webAPIRouter.PathPrefix("/auth").Subrouter()
	authRoutes.Handle("/login", api.RateLimit(float64(cfg.LoginRateLimit)/60.0, cfg.LoginRateLimit, http.HandlerFunc(api.LoginHandler))).Methods("POST")
	authRoutes.HandleFunc("/logout", api.LogoutHandler).Methods("POST")
	// /me MUST run inside auth.Middleware so the user context (and thus the role/permissions in the response) is populated for the Vue dashboard.
	authRoutes.Handle("/me", auth.Middleware(cfg)(http.HandlerFunc(api.MeHandler))).Methods("GET")

	// Web UI routes - Vue 3 frontend compatible endpoints All /api/web/* routes are authenticated + RBAC enforced

	// GET /api/web/devices and /api/web/nodes - list all nodes (permission can_view_nodes, admin/owner bypass)
	webAPIRouter.Handle("/web/devices", auth.Middleware(cfg)(auth.RequirePermission(repo, "can_view_nodes")(http.HandlerFunc(api.GetNodesHandler)))).Methods("GET")
	webAPIRouter.Handle("/web/nodes", auth.Middleware(cfg)(auth.RequirePermission(repo, "can_view_nodes")(http.HandlerFunc(api.GetNodesHandler)))).Methods("GET")

	// PUT /api/web/nodes/{id}/sub - update node subscription URL (permission can_edit_sub)
	webAPIRouter.Handle("/web/nodes/{id}/sub", auth.Middleware(cfg)(auth.RequirePermission(repo, "can_edit_sub")(http.HandlerFunc(api.UpdateNodeSubHandler)))).Methods("PUT")

	// POST /api/web/devices/mass-update-sub - mass update subscription URL for all nodes (permission can_edit_sub)
	webAPIRouter.Handle("/web/devices/mass-update-sub", auth.Middleware(cfg)(auth.RequirePermission(repo, "can_edit_sub")(http.HandlerFunc(api.MassUpdateSubHandler)))).Methods("POST")

	// DELETE /api/web/devices/{id} - delete a node (permission can_edit_sub)
	webAPIRouter.Handle("/web/devices/{id}", auth.Middleware(cfg)(auth.RequirePermission(repo, "can_edit_sub")(http.HandlerFunc(api.DeleteNodeHandler)))).Methods("DELETE")

	// POST /api/web/nodes/purge-offline - delete ghost nodes offline for N days (permission can_purge_nodes)
	webAPIRouter.Handle("/web/nodes/purge-offline", auth.Middleware(cfg)(auth.RequirePermission(repo, domain.PermPurgeNodes)(http.HandlerFunc(api.PurgeOfflineNodesHandler)))).Methods("POST")

	// GET /api/web/templates - list client deployment template files (permission can_edit_sub)
	webAPIRouter.Handle("/web/templates", auth.Middleware(cfg)(auth.RequirePermission(repo, "can_edit_sub")(http.HandlerFunc(api.GetTemplatesHandler)))).Methods("GET")

	// GET /api/web/install-command - tokenized node onboarding command (permission can_update_client)
	webAPIRouter.Handle("/web/install-command", auth.Middleware(cfg)(auth.RequirePermission(repo, domain.PermUpdateClient)(http.HandlerFunc(api.InstallCommandHandler)))).Methods("GET")

	// PUT /api/web/templates/{filename} - overwrite a client template file (permission can_edit_sub)
	webAPIRouter.Handle("/web/templates/{filename}", auth.Middleware(cfg)(auth.RequirePermission(repo, "can_edit_sub")(http.HandlerFunc(api.UpdateTemplateHandler)))).Methods("PUT")

	// POST /api/web/nodes/update-client-files - queue update_client_files for all/one node (permission can_update_client)
	webAPIRouter.Handle("/web/nodes/update-client-files", auth.Middleware(cfg)(auth.RequirePermission(repo, domain.PermUpdateClient)(http.HandlerFunc(api.UpdateClientFilesHandler)))).Methods("POST")

	// POST /api/web/nodes/mass-update-client - OTA alias: queue update_client_files for ALL nodes (permission can_update_client)
	webAPIRouter.Handle("/web/nodes/mass-update-client", auth.Middleware(cfg)(auth.RequirePermission(repo, domain.PermUpdateClient)(http.HandlerFunc(api.UpdateClientFilesHandler)))).Methods("POST")
	// POST /api/web/nodes/mass-update-sub - queue update_sub for ALL nodes (permission can_edit_sub)
	webAPIRouter.Handle("/web/nodes/mass-update-sub", auth.Middleware(cfg)(auth.RequirePermission(repo, "can_edit_sub")(http.HandlerFunc(api.MassUpdateSubscriptionsHandler)))).Methods("POST")

	// PUT /api/web/nodes/{id}/rename - rename a node (permission can_rename_node)
	webAPIRouter.Handle("/web/nodes/{id}/rename", auth.Middleware(cfg)(auth.RequirePermission(repo, domain.PermRenameNode)(http.HandlerFunc(api.RenameNodeHandler)))).Methods("PUT")

	// POST /api/web/nodes/{id}/terminate - queue self-destruct for a node (permission can_terminate_node)
	webAPIRouter.Handle("/web/nodes/{id}/terminate", auth.Middleware(cfg)(auth.RequirePermission(repo, domain.PermTerminateNode)(http.HandlerFunc(api.TerminateNodeHandler)))).Methods("POST")

	// POST /api/web/devices/mass-update-domain - mass update only the domain part of sub_urls (permission can_edit_sub)
	webAPIRouter.Handle("/web/devices/mass-update-domain", auth.Middleware(cfg)(auth.RequirePermission(repo, "can_edit_sub")(http.HandlerFunc(api.MassUpdateDomainHandler)))).Methods("POST")

	// GET /api/web/providers - list subscription providers (permission can_view_nodes)
	webAPIRouter.Handle("/web/providers", auth.Middleware(cfg)(auth.RequirePermission(repo, domain.PermViewNodes)(http.HandlerFunc(api.GetProvidersHandler)))).Methods("GET")

	// POST /api/web/providers - create/update a subscription provider (permission can_manage_providers)
	webAPIRouter.Handle("/web/providers", auth.Middleware(cfg)(auth.RequirePermission(repo, domain.PermManageProviders)(http.HandlerFunc(api.UpsertProviderHandler)))).Methods("POST")

	// PUT /api/web/providers/{domain} - update a subscription provider (permission can_manage_providers)
	webAPIRouter.Handle("/web/providers/{domain}", auth.Middleware(cfg)(auth.RequirePermission(repo, domain.PermManageProviders)(http.HandlerFunc(api.UpsertProviderHandler)))).Methods("PUT")

	// DELETE /api/web/providers/{domain} - delete a subscription provider (permission can_manage_providers)
	webAPIRouter.Handle("/web/providers/{domain}", auth.Middleware(cfg)(auth.RequirePermission(repo, domain.PermManageProviders)(http.HandlerFunc(api.DeleteProviderHandler)))).Methods("DELETE")

	// GET /api/web/roles - list all roles (permission can_view_roles)
	webAPIRouter.Handle("/web/roles", auth.Middleware(cfg)(auth.RequirePermission(repo, domain.PermViewRoles)(http.HandlerFunc(api.GetRolesHandler)))).Methods("GET")

	// POST /api/web/roles - create custom role (permission can_manage_roles)
	webAPIRouter.Handle("/web/roles", auth.Middleware(cfg)(auth.RequirePermission(repo, domain.PermManageRoles)(http.HandlerFunc(api.CreateCustomRoleHandler)))).Methods("POST")

	// PUT /api/web/roles/{id} - update custom role (permission can_manage_roles)
	webAPIRouter.Handle("/web/roles/{id}", auth.Middleware(cfg)(auth.RequirePermission(repo, domain.PermManageRoles)(http.HandlerFunc(api.UpdateCustomRoleHandler)))).Methods("PUT")

	// DELETE /api/web/roles/{id} - delete custom role (permission can_manage_roles)
	webAPIRouter.Handle("/web/roles/{id}", auth.Middleware(cfg)(auth.RequirePermission(repo, domain.PermManageRoles)(http.HandlerFunc(api.DeleteCustomRoleHandler)))).Methods("DELETE")

	// GET /api/web/users - list users (permission can_view_users)
	webAPIRouter.Handle("/web/users", auth.Middleware(cfg)(auth.RequirePermission(repo, domain.PermViewUsers)(http.HandlerFunc(api.GetUsersHandler)))).Methods("GET")

	// POST /api/web/users - create user (permission can_create_users)
	webAPIRouter.Handle("/web/users", auth.Middleware(cfg)(auth.RequirePermission(repo, domain.PermCreateUsers)(http.HandlerFunc(api.CreateUserHandler)))).Methods("POST")

	// PUT /api/web/users/{id} - update user (permission can_edit_users)
	webAPIRouter.Handle("/web/users/{id}", auth.Middleware(cfg)(auth.RequirePermission(repo, domain.PermEditUsers)(http.HandlerFunc(api.UpdateUserHandler)))).Methods("PUT")

	// DELETE /api/web/users/{id} - delete user (permission can_delete_users)
	webAPIRouter.Handle("/web/users/{id}", auth.Middleware(cfg)(auth.RequirePermission(repo, domain.PermDeleteUsers)(http.HandlerFunc(api.DeleteUserHandler)))).Methods("DELETE")

	// GET /api/web/audit - audit logs (permission can_view_audit_logs)
	webAPIRouter.Handle("/web/audit", auth.Middleware(cfg)(auth.RequirePermission(repo, domain.PermViewAuditLogs)(http.HandlerFunc(api.GetAuditLogsHandler)))).Methods("GET")

	// GET /api/web/nodes/{id}/logs - node container logs (permission can_view_node_logs)
	webAPIRouter.Handle("/web/nodes/{id}/logs", auth.Middleware(cfg)(auth.RequirePermission(repo, domain.PermViewNodeLogs)(http.HandlerFunc(api.GetNodeLogsHandler)))).Methods("GET")

	// GET /api/web/logs/master - master server logs (permission can_view_master_logs)
	webAPIRouter.Handle("/web/logs/master", auth.Middleware(cfg)(auth.RequirePermission(repo, domain.PermViewMasterLogs)(http.HandlerFunc(api.GetMasterLogsHandler)))).Methods("GET")

	// GET /api/web/backup/download - backup download (owner only; DB backups contain user hashes, session secrets and tokens)
	webAPIRouter.Handle("/web/backup/download", auth.Middleware(cfg)(auth.RequireOwnerWithMessage(repo, "Forbidden: Backups are strictly restricted to the system Owner.")(http.HandlerFunc(api.DownloadBackupHandler)))).Methods("GET")

	// POST /api/web/devices/{id}/command - send a command to a specific node (permission can_switch_vpn)
	webAPIRouter.Handle("/web/devices/{id}/command", auth.Middleware(cfg)(auth.RequirePermission(repo, "can_switch_vpn")(http.HandlerFunc(api.SendCommandHandler)))).Methods("POST")
	// POST /api/web/nodes/{id}/command - send a command to a specific node (permission can_switch_vpn)
	webAPIRouter.Handle("/web/nodes/{id}/command", auth.Middleware(cfg)(auth.RequirePermission(repo, "can_switch_vpn")(http.HandlerFunc(api.SendCommandHandler)))).Methods("POST")
	// PUT /api/web/nodes/{id}/clear-command - cancel a queued command (permission can_switch_vpn)
	webAPIRouter.Handle("/web/nodes/{id}/clear-command", auth.Middleware(cfg)(auth.RequirePermission(repo, "can_switch_vpn")(http.HandlerFunc(api.ClearCommandHandler)))).Methods("PUT")

	// Admin/Owner routes (alternative paths) - node management only
	adminAPIRoutes := webAPIRouter.PathPrefix("/admin").Subrouter()
	adminAPIRoutes.Use(auth.Middleware(cfg))
	adminAPIRoutes.Use(auth.RequireAdminOrOwner(repo))
	adminAPIRoutes.HandleFunc("/nodes", api.GetNodesHandler).Methods("GET")
	adminAPIRoutes.HandleFunc("/nodes/{id}", api.UpdateNodeHandler).Methods("PUT")
	adminAPIRoutes.HandleFunc("/nodes/{id}", api.DeleteNodeHandler).Methods("DELETE")

	// Owner-only admin routes: user management and node->user assignment. The owner role is the ONLY role that can manage other users or roles.
	ownerAdminRoutes := webAPIRouter.PathPrefix("/admin").Subrouter()
	ownerAdminRoutes.Use(auth.Middleware(cfg))
	ownerAdminRoutes.Use(auth.RequireOwner(repo))
	ownerAdminRoutes.HandleFunc("/nodes/{id}/assign/{userId}", api.AssignNodeToUserHandler).Methods("POST")
	ownerAdminRoutes.HandleFunc("/users", api.GetUsersHandler).Methods("GET")
	ownerAdminRoutes.HandleFunc("/users/{id}/reset-password", api.ResetUserPasswordHandler).Methods("POST")

	// Owner-only routes
	ownerAPIRoutes := webAPIRouter.PathPrefix("/owner").Subrouter()
	ownerAPIRoutes.Use(auth.Middleware(cfg))
	ownerAPIRoutes.Use(auth.RequireOwner(repo))
	ownerAPIRoutes.HandleFunc("/users", api.CreateUserHandler).Methods("POST")
	ownerAPIRoutes.HandleFunc("/users/{id}", api.DeleteUserHandler).Methods("DELETE")
	ownerAPIRoutes.HandleFunc("/users/{id}", api.UpdateUserHandler).Methods("PUT")
	ownerAPIRoutes.HandleFunc("/settings/bot", api.UpdateBotSettingsHandler).Methods("POST")
	ownerAPIRoutes.HandleFunc("/backup/download", api.DownloadBackupHandler).Methods("GET")
	ownerAPIRoutes.HandleFunc("/custom-roles", api.CreateCustomRoleHandler).Methods("POST")
	ownerAPIRoutes.HandleFunc("/custom-roles", api.GetCustomRolesHandler).Methods("GET")
	ownerAPIRoutes.HandleFunc("/custom-roles/{id}", api.DeleteCustomRoleHandler).Methods("DELETE")

	// Web UI settings (Owner only)
	webAPIRouter.Handle("/web/settings", auth.Middleware(cfg)(auth.RequireOwner(repo)(http.HandlerFunc(api.GetSettingsHandler)))).Methods("GET")
	webAPIRouter.Handle("/web/settings/backup", auth.Middleware(cfg)(auth.RequireOwnerWithMessage(repo, "Forbidden: Backups are strictly restricted to the system Owner.")(http.HandlerFunc(api.UpdateBackupSettingsHandler)))).Methods("PUT")
	settingsWeb := webAPIRouter.PathPrefix("/settings").Subrouter()
	settingsWeb.Use(auth.Middleware(cfg))
	settingsWeb.Use(auth.RequireOwner(repo))
	settingsWeb.HandleFunc("/bot", api.GetBotSettingsHandler).Methods("GET")

	// Bot settings update + test from web UI
	webAPIRouter.Handle("/web/settings/bot", auth.Middleware(cfg)(auth.RequireOwner(repo)(http.HandlerFunc(api.UpdateBotSettingsHandler)))).Methods("PUT")
	webAPIRouter.Handle("/web/settings/bot/test", auth.Middleware(cfg)(auth.RequireOwner(repo)(http.HandlerFunc(api.TestTelegramBotHandler)))).Methods("POST")
	webAPIRouter.Handle("/web/settings/bot/reset-avatar", auth.Middleware(cfg)(auth.RequireOwner(repo)(http.HandlerFunc(api.ResetBotAvatarHandler)))).Methods("POST")
	webAPIRouter.Handle("/web/settings/bot/avatar", auth.Middleware(cfg)(auth.RequireOwner(repo)(http.HandlerFunc(api.SetBotAvatarHandler)))).Methods("POST")
	// POST /api/web/settings/revoke-sessions - force-logout every active session (owner only)
	webAPIRouter.Handle("/web/settings/revoke-sessions", auth.Middleware(cfg)(auth.RequireOwner(repo)(http.HandlerFunc(api.RevokeSessionsHandler)))).Methods("POST")
	webAPIRouter.Handle("/web/user/preferences", auth.Middleware(cfg)(http.HandlerFunc(api.GetUserPreferencesHandler))).Methods("GET")
	webAPIRouter.Handle("/web/user/preferences", auth.Middleware(cfg)(http.HandlerFunc(api.UpdateUserPreferencesHandler))).Methods("PUT")

	// Serve the embedded Vue.js dashboard (skip in Low-RAM mode)
	if !cfg.LowRAMMode {
		distFS, err := fs.Sub(webFS, "web/dist")
		if err != nil {
			log.Printf("Warning: web/dist not found, dashboard will not be served: %v", err)
		} else {
			dashboardRouter.PathPrefix("/").Handler(serveDashboard(distFS))
		}
	} else {
		log.Println("Low-RAM mode: Web UI rendering disabled. API endpoints remain active.")
		// In low-RAM mode, serve a minimal landing page
		dashboardRouter.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"status":  "running",
				"mode":    "low-ram",
				"message": "Web dashboard disabled in Low-RAM mode. Use Telegram Bot or API.",
			})
		})
	}

	// --- Public Bootstrap Routes (token-gated payload delivery) --- All payload endpoints require ?t=<SECRET_TOKEN>; unauthenticated requests receive a generic nginx 404 page.
	joinRouter.HandleFunc("/", payloadTokenGuard(cfg, http.HandlerFunc(api.serveFile(deployFS, "deploy/join.sh", "application/x-shellscript"))).ServeHTTP).Methods("GET")
	joinRouter.HandleFunc("/join.sh", payloadTokenGuard(cfg, http.HandlerFunc(api.serveFile(deployFS, "deploy/join.sh", "application/x-shellscript"))).ServeHTTP).Methods("GET")
	joinRouter.HandleFunc("/join.ps1", payloadTokenGuard(cfg, http.HandlerFunc(api.serveFile(deployFS, "deploy/join.ps1", "text/x-powershell"))).ServeHTTP).Methods("GET")
	joinRouter.HandleFunc("/fleet-cli", payloadTokenGuard(cfg, http.HandlerFunc(api.serveTemplateFile("fleet-cli.sh", "application/x-shellscript"))).ServeHTTP).Methods("GET")
	joinRouter.HandleFunc("/fleet-cli.ps1", payloadTokenGuard(cfg, http.HandlerFunc(api.serveTemplateFile("fleet-cli.ps1", "text/x-powershell"))).ServeHTTP).Methods("GET")
	joinRouter.HandleFunc("/fleet-agent.service", payloadTokenGuard(cfg, http.HandlerFunc(api.serveFile(deployFS, "deploy/fleet-agent.service", "text/plain"))).ServeHTTP).Methods("GET")
	subRouter.HandleFunc("/docker-compose.yml", payloadTokenGuard(cfg, http.HandlerFunc(api.serveDockerCompose)).ServeHTTP).Methods("GET")
	subRouter.HandleFunc("/Dockerfile.client", payloadTokenGuard(cfg, http.HandlerFunc(api.serveTemplateFile("Dockerfile.client", "text/plain"))).ServeHTTP).Methods("GET")
	subRouter.HandleFunc("/requirements.txt", payloadTokenGuard(cfg, http.HandlerFunc(api.serveTemplateFile("requirements.txt", "text/plain"))).ServeHTTP).Methods("GET")
	subRouter.HandleFunc("/entrypoint.sh", payloadTokenGuard(cfg, http.HandlerFunc(api.serveTemplateFile("entrypoint.sh", "application/x-shellscript"))).ServeHTTP).Methods("GET")
	subRouter.HandleFunc("/node_agent.py", payloadTokenGuard(cfg, http.HandlerFunc(api.serveNodeAgent)).ServeHTTP).Methods("GET")
	// /agent_src.zip is the OTA package endpoint: UpdateClientFilesHandler commands every agent to download the zip from the sub-domain (<SUB_DOMAIN>/agent_src.zip?t=<secret>). The route was only ever registered on the API domain (/api/agent/latest.zip), so agents fetched a 404 page instead of the archive and every OTA update failed with "BadZipFile". Registered here alongside the other client payloads.
	subRouter.HandleFunc("/agent_src.zip", payloadTokenGuard(cfg, http.HandlerFunc(api.serveAgentPackage)).ServeHTTP).Methods("GET")
	subRouter.HandleFunc("/fleet-cli.sh", payloadTokenGuard(cfg, http.HandlerFunc(api.serveTemplateFile("fleet-cli.sh", "application/x-shellscript"))).ServeHTTP).Methods("GET")
	subRouter.HandleFunc("/configs/xray_config.json", payloadTokenGuard(cfg, http.HandlerFunc(api.serveFile(deployFS, "deploy/configs/xray_config.json", "application/json"))).ServeHTTP).Methods("GET")
	subRouter.HandleFunc("/configs/singbox_config.json", payloadTokenGuard(cfg, http.HandlerFunc(api.serveFile(deployFS, "deploy/configs/singbox_config.json", "application/json"))).ServeHTTP).Methods("GET")
}

// applyDomainPlaceholders substitutes the client-facing domain placeholders (used in deploy templates) with the configured domains from .env.
func (a *API) applyDomainPlaceholders(content string) string {
	content = strings.ReplaceAll(content, "https://__API_DOMAIN__", "https://"+a.config.ApiDomain)
	content = strings.ReplaceAll(content, "https://__SUB_DOMAIN__", "https://"+a.config.SubDomain)
	content = strings.ReplaceAll(content, "https://__JOIN_DOMAIN__", "https://"+a.config.JoinDomain)
	content = strings.ReplaceAll(content, "https://__DASH_DOMAIN__", "https://"+a.config.DashboardDomain)
	content = strings.ReplaceAll(content, "__API_DOMAIN__", a.config.ApiDomain)
	content = strings.ReplaceAll(content, "__SUB_DOMAIN__", a.config.SubDomain)
	content = strings.ReplaceAll(content, "__JOIN_DOMAIN__", a.config.JoinDomain)
	content = strings.ReplaceAll(content, "__DASH_DOMAIN__", a.config.DashboardDomain)
	// The fleet secret is injected so bootstrap scripts can authenticate their subsequent payload downloads with ?t=<SECRET_TOKEN>.
	content = strings.ReplaceAll(content, "__SECRET_TOKEN__", a.config.FleetSecret)
	// The build version from the root VERSION file (used e.g. by the CLI banner).
	content = strings.ReplaceAll(content, "__VERSION__", a.config.AppVersion)
	return content
}

// fakeNginx404 is served for unauthenticated payload requests. It is byte-identical to a stock nginx 404 page so active probes cannot tell the payload endpoints apart from any other dead path on the host.
const fakeNginx404 = `<html>
<head><title>404 Not Found</title></head>
<body>
<center><h1>404 Not Found</h1></center>
<hr><center>nginx/1.24.0</center>
</body>
</html>
`

// payloadTokenGuard requires the ?t=<SECRET_TOKEN> query parameter on payload delivery routes (join bootstrap script, client templates, engine configs). Requests without a matching token receive the generic nginx 404 above and never reach the payload handler.
func payloadTokenGuard(cfg *config.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cfg.FleetSecret != "" && r.URL.Query().Get("t") == cfg.FleetSecret {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(fakeNginx404))
	})
}

// serveFile is a helper to serve an embedded file. The Content-Type always carries charset=utf-8 so PowerShell's Invoke-RestMethod decodes localized (e.g. Cyrillic) text as UTF-8 instead of the ANSI codepage, and any UTF-8 BOM is stripped so `irm ... | iex` does not parse "ï»¿#" as garbage.
func (a *API) serveFile(efs embed.FS, path, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fileBytes, err := efs.ReadFile(path)
		if err != nil {
			http.Error(w, "Internal Server Error: File not found", http.StatusInternalServerError)
			log.Printf("Error reading embedded file %s: %v", path, err)
			return
		}
		fileBytes = stripUTF8BOM(fileBytes)
		if isShellScript(path) {
			fileBytes = stripCRLF(fileBytes)
		}
		w.Header().Set("Content-Type", contentType+"; charset=utf-8")
		w.Write([]byte(a.applyDomainPlaceholders(string(fileBytes))))
	}
}

// serveDashboard serves the embedded Vue.js SPA. It must stay completely public — no auth, no token guard, no stealth 404 — or the login page can never load. Only the /api/web/* routes are authenticated. Paths that are not real files (Vue Router deep links such as /login) fall back to index.html so direct navigation renders the SPA instead of a 404.
func serveDashboard(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if f, err := fsys.Open(p); err != nil {
				r.URL.Path = "/"
			} else {
				f.Close()
			}
		}
		fileServer.ServeHTTP(w, r)
	})
}

// RateLimit returns middleware with configurable requests per duration. Uses sliding window via golang.org/x/time/rate.
func (a *API) RateLimit(rps float64, burst int, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}

		a.mu.Lock()
		entry, exists := a.visitors["rl:"+ip]
		if !exists {
			entry = &visitorEntry{limiter: rate.NewLimiter(rate.Limit(rps), burst)}
			a.visitors["rl:"+ip] = entry
		}
		entry.lastSeen = time.Now()
		a.mu.Unlock()

		if !entry.limiter.Allow() {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// startRateLimitCleanup prunes rate-limit entries that have not been touched for 15 minutes. Without it, the visitors map would grow unboundedly: every unique IP (login attempts) and node id (sub_url updates) would leave a permanent entry and eventually exhaust memory on a busy master.
func (a *API) startRateLimitCleanup() {
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			cutoff := time.Now().Add(-15 * time.Minute)
			a.mu.Lock()
			for key, entry := range a.visitors {
				if entry.lastSeen.Before(cutoff) {
					delete(a.visitors, key)
				}
			}
			a.mu.Unlock()
		}
	}()
}

// stripPort removes the port from a domain if it exists (e.g. localhost:8080 -> localhost, [::1]:8080 -> [::1]). A bare hostname without a port is returned unchanged.
func stripPort(domain string) string {
	if host, _, err := net.SplitHostPort(domain); err == nil {
		return host
	}
	return domain
}

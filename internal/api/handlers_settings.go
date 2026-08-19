package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"malaxis-fleet/internal/audit"
	"malaxis-fleet/internal/auth"
	"malaxis-fleet/internal/domain"
)

// generateRandomHex returns n random bytes encoded as hex. Used to rotate the
// global session version.
func generateRandomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// RevokeSessionsHandler force-logs-out every user by rotating the global
// session_version in the database and in memory. All existing cookies become
// invalid immediately; everyone (including the caller) must log in again.
func (a *API) RevokeSessionsHandler(w http.ResponseWriter, r *http.Request) {
	if !a.requireOwner(w, r) {
		return
	}

	newVersion := generateRandomHex(16)
	if newVersion == "" {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if err := a.repo.SetSetting("session_version", newVersion); err != nil {
		log.Printf("ERROR: failed to persist new session_version: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	auth.SetSessionVersion(newVersion)

	actorUser := a.actor(r)
	a.auditLogger.LogFromRequest(r, a.actorName(actorUser), audit.ActionUpdateSettings, "sessions", "All sessions revoked (global session version rotated)")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
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

	actorUser := a.actor(r)
	a.auditLogger.LogFromRequest(r, a.actorName(actorUser), audit.ActionUpdateSettings, "bot", "Bot settings updated")

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

	// Fall back to the env-configured values when the DB keys are empty, so the dashboard always reflects the token/chat_id the bot actually runs with.
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

// GetUserPreferencesHandler returns the current user's personalization settings (accent color, theme mode, language, bot emoji rendering).
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

// UpdateUserPreferencesHandler updates the current user's personalization settings. Invalid values are silently replaced with the defaults.
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

// ResetBotAvatarHandler re-uploads the embedded default profile photo of the Telegram bot on demand.
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

	actorUser := a.actor(r)
	a.auditLogger.LogFromRequest(r, a.actorName(actorUser), audit.ActionUpdateSettings, "bot", "Bot profile photo reset to default")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// SetBotAvatarHandler applies one of the five themed avatar colors to the Telegram bot's profile photo (owner only). The chosen color is persisted in the settings table and re-applied automatically on every bot start.
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

	actorUser := a.actor(r)
	a.auditLogger.LogFromRequest(r, a.actorName(actorUser), audit.ActionUpdateSettings, "bot", "Bot profile photo set to color "+req.Color)

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

	// Fall back to env-configured values so the settings page always reflects the token/chat_id the bot actually runs with.
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

// UpdateBackupSettingsHandler updates the automated-backup settings: routing (local / Telegram) and the backup interval in hours (owner only). The values are persisted in the settings table and consumed by the backup scheduler inside the bot, which re-reads them on every cycle.
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
	// Defense-in-depth: DB backups contain user hashes, session secrets and tokens, so access is hardcoded to the owner role regardless of any permission list. The router additionally enforces RequireOwner.
	userID, ok := r.Context().Value(auth.UserContextKey).(int64)
	if !ok || userID == 0 {
		http.Error(w, "Unauthorized: Not authenticated", http.StatusUnauthorized)
		return
	}
	user, err := a.repo.GetUserByID(userID)
	if err != nil {
		http.Error(w, "Forbidden: User not found", http.StatusForbidden)
		return
	}
	if user.Role != domain.RoleOwner {
		http.Error(w, "Forbidden: Backups are strictly restricted to the system Owner.", http.StatusForbidden)
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

// --- Audit & Master Logs Handlers ---

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

// GetMasterLogsHandler returns the last 100 lines of a master server container's logs (used by the "Logs & Audit" tab). The container is chosen via the `?container=` query param (fleet-master, fleet-postgres, ...; default fleet-master) and read from `docker logs` so the real output is shown. For fleet-master, a configured log file (MasterLogFile) is preferred.
func (a *API) GetMasterLogsHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermViewMasterLogs) {
		return
	}

	container := r.URL.Query().Get("container")
	if container == "" {
		container = "fleet-master"
	}

	// Security hardening: only a whitelisted set of master containers may be inspected. This keeps the `docker logs <container>` invocation (exec.Command with user-supplied args) from being used to read arbitrary container logs or, in a crafted case, reaching paths the Logs & Audit tab should not touch.
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

package bot

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"malaxis-fleet/internal/backup"
	"malaxis-fleet/internal/config"
	"malaxis-fleet/internal/domain"
	"malaxis-fleet/internal/repository"
	"malaxis-fleet/internal/service"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type userState struct {
	Step   string
	NodeID string
	Node   domain.Node
	User   domain.User
}

type Bot struct {
	api          *tgbotapi.BotAPI
	repo         repository.Repository
	cfg          *config.Config
	autoSync     *service.AutoSyncService
	backupEngine *backup.Engine
	userStates   map[int64]*userState
	mainMenuID   map[int64]int
	backupTicker *time.Ticker
	stop         chan bool
	mu           sync.Mutex
	token        string
	chatID       int64
	running      bool
}

func NewBot(cfg *config.Config, repo repository.Repository, autoSync *service.AutoSyncService) *Bot {
	return &Bot{
		repo:         repo,
		cfg:          cfg,
		autoSync:     autoSync,
		backupEngine: backup.NewEngine(cfg),
		userStates:   make(map[int64]*userState),
		mainMenuID:   make(map[int64]int),
		stop:         make(chan bool),
		token:        cfg.BotToken,
		chatID:       cfg.AdminChatID,
	}
}

func (b *Bot) loadSettingsFromDB() {
	enabled, _ := b.repo.GetSetting("tg_bot_enabled")
	if enabled == "" {
		enabled, _ = b.repo.GetSetting("bot_enabled")
	}
	token, _ := b.repo.GetSetting("tg_bot_token")
	if token == "" {
		token, _ = b.repo.GetSetting("bot_token")
	}
	chatIDStr, _ := b.repo.GetSetting("tg_admin_chat_id")
	if chatIDStr == "" {
		chatIDStr, _ = b.repo.GetSetting("bot_chat_id")
	}

	b.mu.Lock()
	if token != "" {
		b.token = token
	}
	if chatIDStr != "" {
		if cid, err := strconv.ParseInt(chatIDStr, 10, 64); err == nil {
			b.chatID = cid
		}
	}
	b.mu.Unlock()

	if enabled == "true" && b.token != "" && b.chatID != 0 {
		log.Println("Bot settings loaded from DB: enabled, token present, chat_id set")
	}
}

// enabledFromDB reports whether the bot should run according to the database
// setting tg_bot_enabled. Only an explicit "false" or "0" disables the bot;
// any other value (including an unset key) keeps the legacy behaviour of
// starting when a token is configured.
func (b *Bot) enabledFromDB() bool {
	enabled, _ := b.repo.GetSetting("tg_bot_enabled")
	if enabled == "" {
		enabled, _ = b.repo.GetSetting("bot_enabled")
	}
	return enabled != "false" && enabled != "0"
}

func (b *Bot) Start() error {
	b.loadSettingsFromDB()

	if !b.enabledFromDB() {
		log.Println("Telegram bot disabled in settings (tg_bot_enabled != true), polling loop NOT started")
		return nil
	}

	b.mu.Lock()
	if b.token == "" || b.chatID == 0 {
		b.mu.Unlock()
		log.Println("Telegram bot not started: token or chat_id not configured")
		return fmt.Errorf("bot token or chat_id not configured")
	}
	token := b.token
	chatID := b.chatID
	b.mu.Unlock()

	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return fmt.Errorf("failed to create bot API: %w", err)
	}

	b.mu.Lock()
	b.api = api
	b.chatID = chatID
	b.running = true
	b.mu.Unlock()

	b.backupTicker = time.NewTicker(24 * time.Hour)
	go b.runBackupScheduler()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := b.api.GetUpdatesChan(u)

	log.Println("Telegram bot started")

	for update := range updates {
		select {
		case <-b.stop:
			return nil
		default:
		}

		b.mu.Lock()
		running := b.running
		adminChatID := b.chatID
		b.mu.Unlock()

		if !running {
			return nil
		}

		if update.Message != nil {
			if update.Message.Chat.ID != adminChatID {
				continue
			}
			if update.Message.IsCommand() {
				switch update.Message.Command() {
				case "start":
					b.handleStartCommand(update.Message.Chat.ID)
				default:
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Unknown command. Use /start to see the main menu.")
					b.api.Send(msg)
				}
			}
		} else if update.CallbackQuery != nil {
			if update.CallbackQuery.Message.Chat.ID != adminChatID {
				continue
			}
			b.handleCallbackQuery(update.CallbackQuery)
		}
	}
	return nil
}

func (b *Bot) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.backupTicker != nil {
		b.backupTicker.Stop()
		b.backupTicker = nil
	}
	b.running = false
	select {
	case b.stop <- true:
	default:
	}
	// tgbotapi's StopReceivingUpdates closes the updates channel but does NOT
	// nil it out, so calling it twice (e.g. Reboot after a settings save when
	// the bot is disabled) would panic with "close of closed channel". Drop the
	// reference so a second Stop is a no-op. Start() always builds a fresh API.
	if b.api != nil {
		b.api.StopReceivingUpdates()
		b.api = nil
	}
}

func (b *Bot) Reboot() error {
	b.Stop()
	time.Sleep(500 * time.Millisecond)
	b.stop = make(chan bool)
	return b.Start()
}

func (b *Bot) runBackupScheduler() {
	for {
		b.mu.Lock()
		ticker := b.backupTicker
		stopCh := b.stop
		b.mu.Unlock()

		if ticker == nil || stopCh == nil {
			return
		}

		select {
		case <-ticker.C:
			log.Println("Running scheduled backup...")
			b.handleBackup(b.chatID, 0)
		case <-stopCh:
			return
		}
	}
}

func (b *Bot) SendAdminMessage(text string) {
	b.mu.Lock()
	api := b.api
	chatID := b.chatID
	b.mu.Unlock()

	if api == nil {
		return
	}
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	api.Send(msg)
}

func (b *Bot) SendCriticalErrorAlert(errorMessage string) {
	text := "<b>🚨 [CRITICAL ERROR] Master Server Failure</b>\n\n<pre>" + errorMessage + "</pre>"
	b.SendAdminMessage(text)
}

func (b *Bot) handleCallbackQuery(q *tgbotapi.CallbackQuery) {
	callback := tgbotapi.NewCallback(q.ID, "")
	b.api.Request(callback)

	chatID := q.Message.Chat.ID
	messageID := q.Message.MessageID

	switch {
	case q.Data == "start":
		b.updateMainMenu(chatID, messageID)
	case q.Data == "nodes_list":
		b.handleNodeList(chatID, messageID)
	case q.Data == "settings":
		b.handleSettings(chatID, messageID)
	case q.Data == "settings_autosync":
		b.handleAutoSyncSettings(chatID, messageID)
	case strings.HasPrefix(q.Data, "set_autosync_"):
		b.handleSetAutoSync(q)
	case q.Data == "settings_backup":
		b.handleBackup(chatID, messageID)
	case q.Data == "settings_admins":
		b.handleAdminManagement(chatID, messageID)
	case q.Data == "admins_list":
		b.handleAdminList(chatID, messageID)
	case q.Data == "admins_add":
		b.handleAdminAddWIP(chatID, messageID)
	case strings.HasPrefix(q.Data, "admin_delete_"):
		b.handleDeleteAdmin(q)
	}
}

// editMessage updates the text (and optionally the inline keyboard) of an
// existing message, keeping the entire bot interaction inside a single message.
func (b *Bot) editMessage(chatID int64, messageID int, text string, markup *tgbotapi.InlineKeyboardMarkup) {
	msg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.ReplyMarkup = markup
	b.api.Send(msg)
}

func (b *Bot) sendMainMenu(chatID int64) {
	text, markup := b.getMainMenuContent()
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.ReplyMarkup = markup

	sentMsg, err := b.api.Send(msg)
	if err == nil {
		b.mainMenuID[chatID] = sentMsg.MessageID
	}
}

func (b *Bot) updateMainMenu(chatID int64, messageID int) {
	text, markup := b.getMainMenuContent()
	msg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.ReplyMarkup = &markup
	b.api.Send(msg)
}

func (b *Bot) getMainMenuContent() (string, tgbotapi.InlineKeyboardMarkup) {
	nodes, _ := b.repo.GetAllNodes()
	onlineCount := 0
	for _, n := range nodes {
		if time.Since(n.LastSeen).Seconds() < 90 {
			onlineCount++
		}
	}

	text := "<b>Malaxis Fleet Manager</b>\n\n<b>Dashboard:</b> https://" + b.cfg.DashboardDomain + "\n<b>Status:</b> " + strconv.Itoa(onlineCount) + " / " + strconv.Itoa(len(nodes)) + " nodes online."

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🎛 Nodes", "nodes_list"),
			tgbotapi.NewInlineKeyboardButtonData("⚙️ Settings", "settings"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 Refresh Status", "start"),
		),
	)
	return text, markup
}

func (b *Bot) handleStartCommand(chatID int64) {
	if mid, ok := b.mainMenuID[chatID]; ok {
		b.updateMainMenu(chatID, mid)
	} else {
		b.sendMainMenu(chatID)
	}
}

func (b *Bot) handleNodeList(chatID int64, messageID int) {
	nodes, err := b.repo.GetAllNodes()
	if err != nil {
		log.Printf("Error getting nodes for bot: %v", err)
		return
	}

	var text strings.Builder
	text.WriteString("<b>Nodes Overview</b>\n\n")

	if len(nodes) == 0 {
		text.WriteString("No nodes are registered yet.")
	}

	for _, n := range nodes {
		var statusIcon string
		if time.Since(n.LastSeen).Seconds() < 90 {
			statusIcon = "🟢"
		} else {
			statusIcon = "🔴"
		}
		text.WriteString(fmt.Sprintf("%s <b>%s</b>\n", statusIcon, n.Name))
		text.WriteString(fmt.Sprintf("    <pre>IP: %s</pre>\n", n.IPLan))
		text.WriteString(fmt.Sprintf("    <pre>Server: %s</pre>\n", n.ActiveServer))

		if n.PipelineStatus != "" {
			text.WriteString(fmt.Sprintf("    <i>Status: %s %s</i>\n", b.getPipelineStatusIcon(n.PipelineStatus), n.PipelineStatus))
			if n.StatusMessage != "" {
				text.WriteString(fmt.Sprintf("    <pre>  ↳ %s</pre>\n", n.StatusMessage))
			}
		}
	}

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅️ Back to Main Menu", "start"),
		),
	)

	msg := tgbotapi.NewEditMessageText(chatID, messageID, text.String())
	msg.ParseMode = tgbotapi.ModeHTML
	msg.ReplyMarkup = &markup
	b.api.Send(msg)
}

func (b *Bot) getPipelineStatusIcon(status string) string {
	switch status {
	case "Queued":
		return "⏳"
	case "Fetched":
		return "📡"
	case "Engine Restarting":
		return "⚙️"
	case "Verified & Active":
		return "✅"
	case "Rollback Executed":
		return "❌"
	default:
		return "❔"
	}
}

func (b *Bot) handleSettings(chatID int64, messageID int) {
	botEnabledStr, _ := b.repo.GetSetting("tg_bot_enabled")
	botEnabled := botEnabledStr == "true"

	text := "<b>⚙️ Settings</b>\n\nSelect a category to manage."
	if botEnabled {
		text += "\n\nBot Status: <b>🟢 Enabled</b>"
	} else {
		text += "\n\nBot Status: <b>🔴 Disabled</b>"
	}

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Auto-Sync", "settings_autosync"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📩 Backup", "settings_backup"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Admin Users", "settings_admins"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅️ Back to Main Menu", "start"),
		),
	)
	msg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.ReplyMarkup = &markup
	b.api.Send(msg)
}

func (b *Bot) handleAutoSyncSettings(chatID int64, messageID int) {
	intervalStr, _ := b.repo.GetSetting("autosync_interval_minutes")
	interval, _ := strconv.Atoi(intervalStr)
	if interval == 0 {
		interval = 60
	}
	text := fmt.Sprintf("<b>Auto-Sync Settings</b>\n\nCurrent Interval: <b>%d minutes</b>\n\nSelect a new interval:", interval)
	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("1h", "set_autosync_60"),
			tgbotapi.NewInlineKeyboardButtonData("3h", "set_autosync_180"),
			tgbotapi.NewInlineKeyboardButtonData("6h", "set_autosync_360"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("12h", "set_autosync_720"),
			tgbotapi.NewInlineKeyboardButtonData("24h", "set_autosync_1440"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅️ Back to Settings", "settings"),
		),
	)
	msg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.ReplyMarkup = &markup
	b.api.Send(msg)
}

func (b *Bot) handleSetAutoSync(q *tgbotapi.CallbackQuery) {
	minutesStr := strings.TrimPrefix(q.Data, "set_autosync_")
	minutes, _ := strconv.Atoi(minutesStr)

	if err := b.autoSync.SetSyncInterval(minutes); err != nil {
		log.Printf("Failed to set sync interval: %v", err)
		return
	}
	b.handleAutoSyncSettings(q.Message.Chat.ID, q.Message.MessageID)
}

func (b *Bot) handleAdminAddWIP(chatID int64, messageID int) {
	text := "<b>Add Admin User</b>\n\nAdding users from the bot is not supported yet.\nUse the web dashboard (Fleet Users tab) instead."
	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("List Admins", "admins_list"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Back to Admin Management", "settings_admins"),
		),
	)
	b.editMessage(chatID, messageID, text, &markup)
}

func (b *Bot) handleBackup(chatID int64, messageID int) {
	if messageID > 0 {
		b.editMessage(chatID, messageID, "<b>Creating database backup...</b>", nil)
	} else {
		b.SendAdminMessage("<b>Creating database backup...</b>")
	}

	backupPath, err := b.backupEngine.CreateBackup()
	if err != nil {
		log.Printf("Error creating backup via bot: %v", err)
		if messageID > 0 {
			b.editMessage(chatID, messageID, "<b>Failed to create backup.</b>\n\nUse the Settings menu to try again.", nil)
		} else {
			b.SendAdminMessage("<b>Failed to create backup.</b>")
		}
		return
	}

	fileBytes, err := os.ReadFile(backupPath)
	if err != nil {
		log.Printf("Error reading backup file for bot: %v", err)
		if messageID > 0 {
			b.editMessage(chatID, messageID, "<b>Failed to read backup file.</b>", nil)
		} else {
			b.SendAdminMessage("<b>Failed to read backup file.</b>")
		}
		return
	}

	file := tgbotapi.FileBytes{Name: filepath.Base(backupPath), Bytes: fileBytes}
	doc := tgbotapi.NewDocument(chatID, file)
	doc.Caption = "Database backup complete."
	b.api.Send(doc)

	if messageID > 0 {
		b.editMessage(chatID, messageID, "<b>Database backup complete.</b>", nil)
		b.handleSettings(chatID, messageID)
	}
}

func (b *Bot) handleAdminManagement(chatID int64, messageID int) {
	text := "<b>Admin User Management</b>"
	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("List Admins", "admins_list"),
			tgbotapi.NewInlineKeyboardButtonData("Add Admin (WIP)", "admins_add"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅️ Back to Settings", "settings"),
		),
	)
	msg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.ReplyMarkup = &markup
	b.api.Send(msg)
}

func (b *Bot) handleAdminListWithNote(chatID int64, messageID int, note string) {
	users, _ := b.repo.GetAllUsers()
	var text strings.Builder
	text.WriteString("<b>Admin Users</b>\n\n")

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, u := range users {
		text.WriteString(fmt.Sprintf("• <b>%s</b> (Role: %s)\n", u.Username, u.Role))
		if len(users) > 1 {
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("Delete %s", u.Username), "admin_delete_"+strconv.FormatInt(u.ID, 10)),
			))
		}
	}
	if note != "" {
		text.WriteString("\n<i>" + note + "</i>")
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("⬅️ Back to Admin Management", "settings_admins"),
	))
	markup := tgbotapi.NewInlineKeyboardMarkup(rows...)
	b.editMessage(chatID, messageID, text.String(), &markup)
}

func (b *Bot) handleAdminList(chatID int64, messageID int) {
	b.handleAdminListWithNote(chatID, messageID, "")
}

func (b *Bot) handleDeleteAdmin(q *tgbotapi.CallbackQuery) {
	adminIDStr := strings.TrimPrefix(q.Data, "admin_delete_")
	adminID, _ := strconv.ParseInt(adminIDStr, 10, 64)

	users, _ := b.repo.GetAllUsers()
	if len(users) <= 1 {
		b.handleAdminListWithNote(q.Message.Chat.ID, q.Message.MessageID, "Cannot delete the last admin user.")
		return
	}

	err := b.repo.DeleteUser(adminID)
	if err != nil {
		log.Printf("Error deleting admin: %v", err)
		b.handleAdminListWithNote(q.Message.Chat.ID, q.Message.MessageID, "Failed to delete user.")
		return
	}

	b.handleAdminListWithNote(q.Message.Chat.ID, q.Message.MessageID, "User deleted.")
}

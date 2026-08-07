package bot

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"malaxis-fleet/internal/audit"
	"malaxis-fleet/internal/backup"
	"malaxis-fleet/internal/config"
	"malaxis-fleet/internal/domain"
	"malaxis-fleet/internal/repository"
	"malaxis-fleet/internal/service"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

//go:embed default_avatar.png
var defaultAvatarPNG []byte

//go:embed avatars/*.png
var avatarsFS embed.FS

// avatarColors maps the five dashboard accent colors to their embedded avatar
// files. The bot's profile photo can be switched to match the site theme.
var avatarColors = map[string]string{
	"indigo":   "avatar_indigo.png",
	"emerald":  "avatar_emerald.png",
	"amber":    "avatar_amber.png",
	"rose":     "avatar_rose.png",
	"cyan":     "avatar_cyan.png",
}

// onlineWindow is how recent a LastSeen timestamp must be for a node to count
// as online (mirrors the web dashboard).
const onlineWindow = 90 * time.Second

// userState is the in-memory text-input session for the admin. The bot shows
// exactly ONE dynamic bot message; when a feature needs free-form text input
// (sub URL, rename, TERMINATE, password, role rank/name, create-user creds, ...)
// the conversation moves into a state machine that consumes the next text
// message: prompt -> capture -> process -> delete -> return to the menu. The
// Username/Password/RoleName fields carry in-progress create flows and
// TargetID/TargetRoleID identify the DB row for the active CRUD operation.
type userState struct {
	Step   string // "", "rename_node", "set_sub", "set_sub_url", "terminate_confirm", "add_user_creds", "add_user_role", "add_role_name", "add_role_rank", "user_pw", "role_rename", "role_rank"
	NodeID string
	Note   string
	// User/role creation flow fields.
	Username     string
	Password     string
	RoleName     string
	TargetID     int64 // target user or role id for in-progress CRUD operations
	TargetRoleID int64 // target role id for the in-progress change-user-role flow
}

type Bot struct {
	api          *tgbotapi.BotAPI
	repo         repository.Repository
	cfg          *config.Config
	autoSync     *service.AutoSyncService
	backupEngine *backup.Engine
	audit        *audit.Logger
	userStates   map[int64]*userState
	mainMenuID   map[int64]int
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
		audit:        audit.NewLogger(repo),
		userStates:   make(map[int64]*userState),
		mainMenuID:   make(map[int64]int),
		stop:         make(chan bool),
		token:        cfg.BotToken,
		chatID:       cfg.AdminChatID,
	}
}

// --- Settings loading ---

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
// setting tg_bot_enabled. Only an explicit "false" or "0" disables the bot.
func (b *Bot) enabledFromDB() bool {
	enabled, _ := b.repo.GetSetting("tg_bot_enabled")
	if enabled == "" {
		enabled, _ = b.repo.GetSetting("bot_enabled")
	}
	return enabled != "false" && enabled != "0"
}

// --- Lifecycle ---

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

	go b.runBackupScheduler()

	updates := make(chan tgbotapi.Update, 100)
	go b.fetchUpdates(updates)
	go b.pollUpdates(updates)

	log.Println("Telegram bot started")

	go func() {
		time.Sleep(2 * time.Second)
		avatarColor, _ := b.repo.GetSetting("bot_avatar_color")
		if avatarColor != "" {
			if err := b.SetAvatarColor(avatarColor); err != nil {
				log.Printf("Bot: failed to set profile photo: %v", err)
			} else {
				log.Printf("Bot: profile photo set (color=%s)", avatarColor)
			}
			return
		}
		if err := b.SetDefaultAvatar(); err != nil {
			log.Printf("Bot: failed to set default profile photo: %v", err)
		} else {
			log.Println("Bot: default profile photo set")
		}
	}()

	return nil
}

// SetDefaultAvatar uploads the embedded default avatar to the Telegram bot via
// setMyProfilePhoto (Bot API 7.0+ format: the photo field carries the JSON
// input profile photo descriptor, the bytes travel in the attach:// file part).
// It also clears any previously stored bot_avatar_color preference so the
// default avatar is not re-applied on the next bot start.
func (b *Bot) SetDefaultAvatar() error {
	b.repo.SetSetting("bot_avatar_color", "")
	return b.uploadAvatar(defaultAvatarPNG)
}

// SetAvatarColor applies one of the five themed avatar colors (indigo,
// emerald, amber, rose, cyan) and persists the choice in the settings table so
// it is automatically re-applied on every bot start.
func (b *Bot) SetAvatarColor(colorName string) error {
	file, ok := avatarColors[colorName]
	if !ok {
		return fmt.Errorf("unknown avatar color: %s", colorName)
	}
	avatarBytes, err := avatarsFS.ReadFile("avatars/" + file)
	if err != nil {
		return fmt.Errorf("embedded avatar %s not found: %w", file, err)
	}
	if err := b.uploadAvatar(avatarBytes); err != nil {
		return err
	}
	return b.repo.SetSetting("bot_avatar_color", colorName)
}

// uploadAvatar sends the given PNG bytes as the bot's profile photo.
func (b *Bot) uploadAvatar(avatarBytes []byte) error {
	token := b.token
	if token == "" {
		return fmt.Errorf("bot token not configured")
	}

	endpoint := "https://api.telegram.org/bot" + token + "/setMyProfilePhoto"

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("photo", `{"type":"static","photo":"attach://avatar_data"}`); err != nil {
		return err
	}
	part, err := writer.CreatePart(map[string][]string{
		"Content-Disposition": {`form-data; name="avatar_data"; filename="avatar.png"`},
		"Content-Type":        {"image/png"},
	})
	if err != nil {
		return err
	}
	if _, err := part.Write(avatarBytes); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("invalid telegram response: %w", err)
	}
	if !result.OK {
		if strings.Contains(strings.ToLower(result.Description), "no need") {
			return nil
		}
		return fmt.Errorf("telegram rejected photo: %s", result.Description)
	}
	return nil
}

// fetchUpdates polls the Telegram API for updates with an explicit offset and
// forwards them into the updates channel. A 409 "Conflict: terminated by other
// getUpdates request" means another process is polling with the same token
// (or a webhook is set); instead of hammering the API we back off gracefully
// and resume as soon as the conflicting consumer releases the poll.
func (b *Bot) fetchUpdates(updates chan<- tgbotapi.Update) {
	offset := 0
	for {
		select {
		case <-b.stop:
			return
		default:
		}

		b.mu.Lock()
		running := b.running
		api := b.api
		b.mu.Unlock()
		if !running || api == nil {
			return
		}

		cfg := tgbotapi.NewUpdate(offset)
		cfg.Timeout = 60
		got, err := api.GetUpdates(cfg)
		if err != nil {
			conflict := strings.Contains(err.Error(), "Conflict") || strings.Contains(err.Error(), "409")
			backoff := 5 * time.Second
			if conflict {
				log.Printf("Bot: getUpdates conflict (another instance polling), retrying in 30s")
				backoff = 30 * time.Second
			} else {
				log.Printf("Bot: getUpdates error: %v (retrying in %s)", err, backoff)
			}
			select {
			case <-time.After(backoff):
			case <-b.stop:
				return
			}
			continue
		}

		for _, upd := range got {
			if upd.UpdateID >= offset {
				offset = upd.UpdateID + 1
			}
			select {
			case updates <- upd:
			case <-b.stop:
				return
			}
		}
	}
}

// pollUpdates consumes Telegram updates until the bot is flagged as not
// running. STRICT SECURITY: every message and callback that does not originate
// from the admin chat id (read from the database) is ignored completely.
func (b *Bot) pollUpdates(updates <-chan tgbotapi.Update) {
	for update := range updates {
		select {
		case <-b.stop:
			return
		default:
		}

		b.mu.Lock()
		running := b.running
		adminChatID := b.chatID
		b.mu.Unlock()

		if !running {
			return
		}

		if update.Message != nil {
			if update.Message.Chat.ID != adminChatID {
				continue
			}
			// COMMAND PURGE: the admin's text message is deleted immediately
			// after processing, keeping the chat to ONE dynamic bot message.
			b.deleteMessage(adminChatID, update.Message.MessageID)

			if update.Message.IsCommand() {
				switch update.Message.Command() {
				case "start":
					b.showMainMenuFresh(adminChatID)
				default:
					b.showMainMenu(adminChatID)
				}
				continue
			}

			b.handleTextInput(adminChatID, update.Message.Text)
		} else if update.CallbackQuery != nil {
			if update.CallbackQuery.Message.Chat.ID != adminChatID {
				continue
			}
			b.handleCallbackQuery(update.CallbackQuery)
		}
	}
}

func (b *Bot) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.running = false
	select {
	case b.stop <- true:
	default:
	}
}

func (b *Bot) Reboot() error {
	b.Stop()
	time.Sleep(500 * time.Millisecond)
	b.stop = make(chan bool)
	return b.Start()
}

// backupIntervalHours returns the configured automatic-backup interval in
// hours (default 24, clamped to a sane range) from the settings table.
func (b *Bot) backupIntervalHours() int {
	raw, _ := b.repo.GetSetting("backup_interval_hours")
	hours, err := strconv.Atoi(raw)
	if err != nil || hours <= 0 {
		return 24
	}
	if hours > 24*31 {
		hours = 24 * 31
	}
	return hours
}

func (b *Bot) runBackupScheduler() {
	for {
		b.mu.Lock()
		stopCh := b.stop
		b.mu.Unlock()

		if stopCh == nil {
			return
		}

		interval := time.Duration(b.backupIntervalHours()) * time.Hour
		timer := time.NewTimer(interval)

		select {
		case <-timer.C:
			log.Printf("Running scheduled backup (interval %d h)...", b.backupIntervalHours())
			toLocal, _ := b.repo.GetSetting("backup_to_local")
			toTelegram, _ := b.repo.GetSetting("backup_to_telegram")
			localEnabled := toLocal != "false"
			telegramEnabled := toTelegram == "true"

			switch {
			case telegramEnabled:
				// sendBackupDocument also stores the file locally.
				b.sendBackupDocument(b.chatID, "⏰ Scheduled backup")
			case localEnabled:
				if _, err := b.backupEngine.CreateGzipBackup(); err != nil {
					log.Printf("Bot: scheduled backup failed: %v", err)
				} else {
					log.Println("Scheduled backup saved to local backups directory")
				}
			default:
				log.Println("Scheduled backup skipped: both backup destinations are disabled")
			}
		case <-stopCh:
			timer.Stop()
			return
		}
	}
}

// --- Public API used by the web layer (BotManager interface) ---

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

// NotifyNewNode sends an instant onboarding notification to the admin the
// moment a brand-new device registers for the first time. The message carries
// quick-setup inline buttons: set sub URL, balanced mode, or reject/delete.
func (b *Bot) NotifyNewNode(id, name, ipLan string) {
	b.mu.Lock()
	api := b.api
	chatID := b.chatID
	b.mu.Unlock()

	if api == nil {
		return
	}

	name = emptyDash(name)
	ipLan = emptyDash(ipLan)

	text := fmt.Sprintf("<b>🖥️ NEW DEVICE CONNECTED!</b>\n\n"+
		"<b>Name:</b> %s\n"+
		"<b>LAN IP:</b> %s\n"+
		"<b>Node ID:</b> <code>%s</code>\n\n"+
		"Status: <b>Registered &amp; Waiting for Configuration.</b>",
		name, ipLan, id)

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔗 Set Sub URL", "node:set_sub:"+id),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⚖️ Set Balanced", "node:switch:"+id+":balanced"),
			tgbotapi.NewInlineKeyboardButtonData("❌ Reject & Delete", "node:delete:"+id),
		),
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.ReplyMarkup = &markup
	if _, err := api.Send(msg); err != nil {
		log.Printf("Bot: failed to send new-node onboarding notification: %v", err)
	}
}

// --- State helpers ---

func (b *Bot) getState(chatID int64) *userState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.userStates[chatID]
}

func (b *Bot) setState(chatID int64, st *userState) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.userStates[chatID] = st
}

func (b *Bot) clearState(chatID int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.userStates, chatID)
}

func (b *Bot) getMainMenuID(chatID int64) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.mainMenuID[chatID]
}

// --- Message helpers (single-message UI) ---

// editMessage updates the text and inline keyboard of the conversation's
// single dynamic bot message and records it as the current main menu id.
func (b *Bot) editMessage(chatID int64, messageID int, text string, markup *tgbotapi.InlineKeyboardMarkup) {
	if messageID <= 0 {
		return
	}
	msg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.ReplyMarkup = markup
	b.api.Send(msg)

	b.mu.Lock()
	b.mainMenuID[chatID] = messageID
	b.mu.Unlock()
}

func (b *Bot) deleteMessage(chatID int64, messageID int) {
	if messageID <= 0 {
		return
	}
	deleteCfg := tgbotapi.NewDeleteMessage(chatID, messageID)
	if _, err := b.api.Request(deleteCfg); err != nil {
		log.Printf("Bot: failed to delete message %d: %v", messageID, err)
	}
}

func (b *Bot) showMainMenu(chatID int64) {
	text, markup := b.getMainMenuContent()

	if mid := b.getMainMenuID(chatID); mid > 0 {
		b.editMessage(chatID, mid, text, &markup)
		return
	}

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.ReplyMarkup = &markup
	sent, err := b.api.Send(msg)
	if err != nil {
		log.Printf("Bot: failed to send main menu: %v", err)
		return
	}
	b.mu.Lock()
	b.mainMenuID[chatID] = sent.MessageID
	b.mu.Unlock()
}

// showMainMenuFresh is used for the /start command: it always sends a BRAND
// NEW main menu message (instead of editing the previous one) so the user can
// summon a fresh menu at the bottom of the chat. Any previous menu message is
// deleted to keep the chat to one dynamic bot message.
func (b *Bot) showMainMenuFresh(chatID int64) {
	if old := b.getMainMenuID(chatID); old > 0 {
		b.deleteMessage(chatID, old)
		b.mu.Lock()
		delete(b.mainMenuID, chatID)
		b.mu.Unlock()
	}

	text, markup := b.getMainMenuContent()
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.ReplyMarkup = &markup
	sent, err := b.api.Send(msg)
	if err != nil {
		log.Printf("Bot: failed to send fresh main menu: %v", err)
		return
	}
	b.mu.Lock()
	b.mainMenuID[chatID] = sent.MessageID
	b.mu.Unlock()
}

// botPrefs loads the admin user's personalization settings used to render the
// bot menu (language and emoji rendering).
func (b *Bot) botPrefs() (lang string, emojis bool) {
	lang = "ru"
	emojis = true
	user, err := b.repo.GetUserByUsername(b.cfg.AdminUser)
	if err != nil {
		return
	}
	prefs, err := b.repo.GetUserPreferences(user.ID)
	if err != nil {
		return
	}
	if prefs.Language == "en" {
		lang = "en"
	}
	emojis = prefs.BotEmojisEnabled
	return
}

// tr returns the Russian or English string matching the stored bot language.
func (b *Bot) tr(ru, en string) string {
	lang, _ := b.botPrefs()
	if lang == "en" {
		return en
	}
	return ru
}

// emoji returns the label with its leading emoji stripped when emoji rendering
// is disabled for the bot.
func (b *Bot) emoji(label string) string {
	_, enabled := b.botPrefs()
	if !enabled {
		label = strings.TrimLeftFunc(label, func(r rune) bool {
			return r >= 0x1F000 && r <= 0x1FAFF || r >= 0x2600 && r <= 0x27BF || r == 0xFE0F ||
				r >= 0x2B00 && r <= 0x2BFF || r >= 0x1F1E6 && r <= 0x1F1FF || r == 0x200D
		})
		return strings.TrimSpace(label)
	}
	return label
}

// toggleBotLanguage cycles the stored bot language between RU and EN.
func (b *Bot) toggleBotLanguage() {
	lang, _ := b.botPrefs()
	next := "ru"
	if lang == "ru" {
		next = "en"
	}
	b.saveBotPrefs(next, -1)
}

// toggleBotEmojis flips the stored emoji rendering flag for the bot.
func (b *Bot) toggleBotEmojis() {
	_, emojis := b.botPrefs()
	next := 0
	if !emojis {
		next = 1
	}
	b.saveBotPrefs("", next)
}

func (b *Bot) saveBotPrefs(lang string, emojis int) {
	user, err := b.repo.GetUserByUsername(b.cfg.AdminUser)
	if err != nil {
		return
	}
	prefs, err := b.repo.GetUserPreferences(user.ID)
	if err != nil {
		return
	}
	if lang != "" {
		prefs.Language = lang
	}
	if emojis >= 0 {
		prefs.BotEmojisEnabled = emojis == 1
	}
	_ = b.repo.UpdateUserPreferences(user.ID, *prefs)
}

// getMainMenuContent builds the main menu: node online/offline counters and
// the fleet action buttons.
func (b *Bot) getMainMenuContent() (string, tgbotapi.InlineKeyboardMarkup) {
	nodes, _ := b.repo.GetAllNodes()
	onlineCount := 0
	for _, n := range nodes {
		if time.Since(n.LastSeen) < onlineWindow {
			onlineCount++
		}
	}
	offlineCount := len(nodes) - onlineCount

	_, emojis := b.botPrefs()
	emojiState := b.tr("ВКЛ", "ON")
	if !emojis {
		emojiState = b.tr("ВЫКЛ", "OFF")
	}

	text := fmt.Sprintf("<b>🌐 Malaxis Fleet v1.0.0</b>\n\n%s: 🟢 %d %s | 🔴 %d %s",
		b.tr("Узлы", "Nodes"), onlineCount, b.tr("онлайн", "Online"), offlineCount, b.tr("офлайн", "Offline"))

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.emoji(b.tr("➕ Добавить устройство", "➕ Add New Device")), "join:command"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.emoji(b.tr("💻 Управление узлами", "💻 Manage Nodes")), "nodes:list"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.emoji(b.tr("🚀 Отправить файлы клиентов (OTA)", "🚀 Push Client Files (OTA)")), "ota:all"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.emoji(b.tr("🔄 Обновить подписки", "🔄 Refresh Subscriptions")), "task:refresh_subs"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.emoji(b.tr("🧹 Очистить офлайн (>3д)", "🧹 Purge Offline (>3d)")), "purge:go"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.emoji(b.tr("📦 Скачать бэкап БД", "📦 Download DB Backup")), "backup:download"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.emoji(b.tr("⏱️ Частота бэкапов", "⏱️ Backup Interval")), "backup:interval"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.emoji(b.tr("👥 Пользователи", "👥 Manage Users")), "users:menu"),
			tgbotapi.NewInlineKeyboardButtonData(b.emoji(b.tr("🛡️ Роли", "🛡️ Manage Roles")), "roles:menu"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.emoji(b.tr("🎨 Выбрать цвет аватарки", "🎨 Select Bot Avatar Color")), "bot:avatar_menu"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.emoji(b.tr("🌐 Язык: RU", "🌐 Language: EN")), "prefs:lang"),
			tgbotapi.NewInlineKeyboardButtonData(b.emoji("😃 "+b.tr("Эмодзи: "+emojiState, "Emojis: "+emojiState)), "prefs:emoji"),
		),
	)
	return text, markup
}

// --- Text input handling (in-memory state machine) ---

func (b *Bot) handleTextInput(chatID int64, text string) {
	state := b.getState(chatID)

	if state == nil || state.Step == "" {
		// No pending operation: silently return to the main menu.
		b.showMainMenu(chatID)
		return
	}

	// Cancel button callback clears the state; plain text continues it.
	switch state.Step {
	case "rename_node":
		b.processRenameText(chatID, text)
	case "set_sub", "set_sub_url":
		b.processSetSubText(chatID, text)
	case "terminate_confirm":
		b.processTerminateText(chatID, text)
	case "add_user_creds":
		b.processAddUserCredsText(chatID, text)
	case "add_user_role":
		// Role selection happens via the inline keyboard; stray text is
		// ignored and the role picker is re-shown.
		b.showRoleSelection(chatID, b.getMainMenuID(chatID), state.Username, "Tap a role below to finish creating the user:")
	case "add_role_name":
		b.processAddRoleNameText(chatID, text)
	case "add_role_rank":
		b.processAddRoleRankText(chatID, text)
	case "user_pw":
		b.processChangeUserPwText(chatID, text)
	case "role_rename":
		b.processRenameRoleText(chatID, text)
	case "role_rank":
		b.processChangeRoleRankText(chatID, text)
	default:
		b.clearState(chatID)
		b.showMainMenu(chatID)
	}
}

func (b *Bot) processRenameText(chatID int64, text string) {
	state := b.getState(chatID)
	b.clearState(chatID)

	newName := strings.TrimSpace(text)
	if newName == "" {
		b.editMessage(chatID, b.getMainMenuID(chatID), "<b>✏️ Rename</b>\n\nName cannot be empty. Send a name or press Cancel.", b.cancelMarkup())
		return
	}

	if err := b.repo.RenameNode(state.NodeID, newName); err != nil {
		log.Printf("Bot: failed to rename node %s: %v", state.NodeID, err)
		b.editMessage(chatID, b.getMainMenuID(chatID), "<b>❌ Failed to rename node.</b>", b.cancelMarkup())
		return
	}

	b.audit.Log("telegram_bot", audit.ActionUpdateDevice, state.NodeID, "Renamed node to "+newName+" (via Telegram bot)")
	b.showNodeDetail(chatID, b.getMainMenuID(chatID), state.NodeID, "✅ Renamed to "+newName)
}

func (b *Bot) processSetSubText(chatID int64, text string) {
	state := b.getState(chatID)
	b.clearState(chatID)

	subURL := strings.TrimSpace(text)
	if subURL == "" {
		b.editMessage(chatID, b.getMainMenuID(chatID), "<b>🔗 Set Sub URL</b>\n\nURL cannot be empty. Send a URL or press Cancel.", b.cancelMarkup())
		return
	}

	if !subURLReachable(subURL) {
		b.editMessage(chatID, b.getMainMenuID(chatID),
			"<b>❌ Invalid Subscription URL!</b>\n\nCould not connect to the link or the server returned an error. Check that the subscription address is working and try again.",
			b.cancelMarkup())
		return
	}

	node, err := b.repo.GetNodeByID(state.NodeID)
	if err != nil {
		log.Printf("Bot: node %s not found for sub update: %v", state.NodeID, err)
		b.showNodeDetail(chatID, b.getMainMenuID(chatID), state.NodeID, "❌ Node not found")
		return
	}

	node.SubURL = subURL
	if err := b.repo.UpdateNode(node); err != nil {
		log.Printf("Bot: failed to update sub_url for node %s: %v", state.NodeID, err)
		b.editMessage(chatID, b.getMainMenuID(chatID), "<b>❌ Failed to update subscription URL.</b>", b.cancelMarkup())
		return
	}

	command := map[string]string{"action": "update_sub", "sub_url": subURL}
	cmdJSON, _ := json.Marshal(command)
	messageID := time.Now().Unix()
	if err := b.repo.SetPendingCommand(state.NodeID, string(cmdJSON), messageID); err != nil {
		log.Printf("Bot: failed to queue update_sub for node %s: %v", state.NodeID, err)
	} else {
		b.repo.UpdateNodePipelineStatus(state.NodeID, "Queued", "update_sub")
	}

	b.audit.Log("telegram_bot", audit.ActionUpdateDevice, state.NodeID, "Updated subscription URL to "+subURL+" (via Telegram bot)")

	// Onboarding-style success: keep the message compact and offer a path back
	// to the full node detail view.
	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💻 View Node Details", "node:detail:"+state.NodeID),
		),
	)
	b.editMessage(chatID, b.getMainMenuID(chatID),
		fmt.Sprintf("<b>✅ Subscription URL Set!</b>\n\nDevice <b>%s</b> is now fetching configs...", b.nodeLabel(state.NodeID)),
		&markup)
}

// subURLReachable checks that a subscription URL is well-formed (http/https,
// no embedded credentials) and actually reachable (5s timeout, HTTP < 400)
// before the bot saves it.
func subURLReachable(raw string) bool {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return false
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(raw)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.CopyN(io.Discard, resp.Body, 4096)
	return resp.StatusCode < http.StatusBadRequest
}

func (b *Bot) processTerminateText(chatID int64, text string) {
	state := b.getState(chatID)
	if strings.TrimSpace(text) != "TERMINATE" {
		b.editMessage(chatID, b.getMainMenuID(chatID),
			"<b>💥 Terminate & Self-Destruct</b>\n\n<code>TERMINATE</code> not received. Type the exact word <code>TERMINATE</code> to confirm (or press Cancel).",
			b.cancelMarkup())
		return
	}

	b.clearState(chatID)

	command, _ := json.Marshal(map[string]string{"action": "terminate"})
	messageID := time.Now().Unix()
	if err := b.repo.SetPendingCommand(state.NodeID, string(command), messageID); err != nil {
		log.Printf("Bot: failed to queue terminate for node %s: %v", state.NodeID, err)
		b.showNodeDetail(chatID, b.getMainMenuID(chatID), state.NodeID, "❌ Failed to queue terminate")
		return
	}
	b.repo.UpdateNodePipelineStatus(state.NodeID, "Queued", "terminate")

	b.audit.Log("telegram_bot", audit.ActionDeleteDevice, state.NodeID, "Queued terminate (self-destruct) command (via Telegram bot)")
	b.showNodeDetail(chatID, b.getMainMenuID(chatID), state.NodeID, "💥 Terminate queued. The node will self-destruct on its next poll.")
}

// handleJoinCommand shows the tokenized node onboarding command. The curl line
// is rendered as a <code> block so Telegram lets the admin copy it on tap.
func (b *Bot) handleJoinCommand(chatID int64, messageID int) {
	if b.cfg.JoinDomain == "" || b.cfg.FleetSecret == "" {
		b.editMessage(chatID, messageID, "<b>❌ Join domain or fleet secret is not configured.</b>", b.backMenuMarkup())
		return
	}

	command := "curl -sSL https://" + b.cfg.JoinDomain + "/?t=" + b.cfg.FleetSecret + " | bash"

	text := "<b>➕ " + b.tr("Добавить новое устройство", "Add New Device") + "</b>\n\n" +
		b.tr("Чтобы добавить новое устройство, выполните эту команду на клиентской машине:", "To add a new device, run this command on the client machine:") + "\n\n" +
		"<code>" + command + "</code>\n\n" +
		b.tr("Нажмите на команду, чтобы скопировать её, затем выполните в терминале любого хоста с Docker.", "Tap the command to copy it, then run it in the terminal of any host with Docker.")

	b.editMessage(chatID, messageID, text, b.backMenuMarkup())
}

// handlePrefsCallback answers callbacks for the preference toggles (language,
// emoji rendering, backup interval) with a visible toast and re-renders the
// main menu.
func (b *Bot) handlePrefsCallback(q *tgbotapi.CallbackQuery, data string) {
	switch data {
	case "prefs:lang":
		b.toggleBotLanguage()
		b.api.Request(tgbotapi.NewCallback(q.ID, "✅ "+b.tr("Язык обновлён", "Language updated")))
	case "prefs:emoji":
		b.toggleBotEmojis()
		b.api.Request(tgbotapi.NewCallback(q.ID, "✅ "+b.tr("Настройка обновлена", "Setting updated")))
	case "backup:interval":
		b.showBackupIntervalPicker(q.Message.Chat.ID, q.Message.MessageID)
		return
	case "backup:download":
		b.handleBackup(q.Message.Chat.ID, q.Message.MessageID)
		return
	}
	if strings.HasPrefix(data, "backup:set:") {
		hoursStr := strings.TrimPrefix(data, "backup:set:")
		hours, err := strconv.Atoi(hoursStr)
		if err != nil || hours <= 0 {
			b.api.Request(tgbotapi.NewCallback(q.ID, "❌ "+b.tr("Некорректный интервал", "Invalid interval")))
			b.showMainMenu(q.Message.Chat.ID)
			return
		}
		if err := b.repo.SetSetting("backup_interval_hours", strconv.Itoa(hours)); err != nil {
			b.api.Request(tgbotapi.NewCallback(q.ID, "❌ "+b.tr("Ошибка сохранения", "Failed to save")))
			log.Printf("Bot: failed to save backup interval: %v", err)
			b.showMainMenu(q.Message.Chat.ID)
			return
		}
		b.api.Request(tgbotapi.NewCallback(q.ID, "✅ "+b.tr("Частота бэкапов: ", "Backup interval: ")+hoursStr+b.tr(" ч", " h")))
		b.showMainMenu(q.Message.Chat.ID)
		return
	}
	b.showMainMenu(q.Message.Chat.ID)
}

// showBackupIntervalPicker renders the backup-frequency picker keyboard.
func (b *Bot) showBackupIntervalPicker(chatID int64, messageID int) {
	interval := b.backupIntervalHours()
	text := fmt.Sprintf("<b>⏱️ %s</b>\n\n%s: <b>%d %s</b>",
		b.tr("Частота бэкапов", "Backup Interval"),
		b.tr("Текущий интервал", "Current interval"),
		interval, b.tr("ч", "h"))

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.emoji("🕐 6 "+b.tr("ч", "h")), "backup:set:6"),
			tgbotapi.NewInlineKeyboardButtonData(b.emoji("🕜 12 "+b.tr("ч", "h")), "backup:set:12"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.emoji("🕐 24 "+b.tr("ч", "h")+" ("+b.tr("1 раз в сутки", "daily")+")"), "backup:set:24"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.emoji("📅 168 "+b.tr("ч", "h")+" ("+b.tr("1 раз в неделю", "weekly")+")"), "backup:set:168"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.emoji("⬅️ "+b.tr("Главное меню", "Main Menu")), "menu:main"),
		),
	)

	b.editMessage(chatID, messageID, text, &markup)
}

// avatarColorDisplay maps the lowercase color keys to their display names.
var avatarColorDisplay = map[string]string{
	"indigo":   "Indigo",
	"emerald":  "Emerald",
	"amber":    "Amber",
	"rose":     "Rose",
	"cyan":     "Cyan",
}

// showAvatarMenu renders the bot avatar color picker sub-menu. The five
// color options match the dashboard accent palette (avatarColors).
func (b *Bot) showAvatarMenu(chatID int64, messageID int) {
	text := "<b>🎨 " + b.tr("Выбор цвета аватарки", "Select Bot Avatar Color") + "</b>\n\n" +
		b.tr("Выберите цвет, соответствующий вашей теме:", "Choose a color matching your theme:")

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.emoji("🟣 Indigo"), "bot:avatar:indigo"),
			tgbotapi.NewInlineKeyboardButtonData(b.emoji("🟢 Emerald"), "bot:avatar:emerald"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.emoji("🟠 Amber"), "bot:avatar:amber"),
			tgbotapi.NewInlineKeyboardButtonData(b.emoji("🔴 Rose"), "bot:avatar:rose"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.emoji("🔵 Cyan"), "bot:avatar:cyan"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.emoji("🔙 "+b.tr("В главное меню", "Back to Main Menu")), "menu:main"),
		),
	)

	b.editMessage(chatID, messageID, text, &markup)
}

// handleAvatarColorSelection applies the chosen avatar color to the bot's
// profile photo (persisting bot_avatar_color) and confirms with a success
// screen offering to pick another color.
func (b *Bot) handleAvatarColorSelection(q *tgbotapi.CallbackQuery, color string) {
	if _, ok := avatarColors[color]; !ok {
		b.api.Request(tgbotapi.NewCallback(q.ID, "❌ "+b.tr("Неизвестный цвет", "Unknown color")))
		return
	}
	if err := b.SetAvatarColor(color); err != nil {
		b.api.Request(tgbotapi.NewCallback(q.ID, "❌ "+b.tr("Не удалось применить аватар", "Could not apply avatar")))
		log.Printf("Bot: failed to apply avatar color %s: %v", color, err)
		return
	}
	b.api.Request(tgbotapi.NewCallback(q.ID, "✅ "+b.tr("Цвет аватарки: ", "Avatar color: ")+color))

	text := fmt.Sprintf("<b>✅ %s</b>\n\n%s <b>%s</b>.",
		b.tr("Фото профиля бота обновлено!", "Bot Profile Photo Updated!"),
		b.tr("Аватар изменён на", "Avatar changed to"),
		avatarColorDisplay[color])
	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.emoji("🔙 "+b.tr("К выбору цвета", "Back to Menu")), "bot:avatar_menu"),
		),
	)
	b.editMessage(q.Message.Chat.ID, q.Message.MessageID, text, &markup)
}

func (b *Bot) cancelMarkup() *tgbotapi.InlineKeyboardMarkup {
	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.emoji("❌ "+b.tr("Отмена", "Cancel")), "state:cancel"),
		),
	)
	return &markup
}

// backMenuMarkup is the standard "← Main Menu" footer row.
func (b *Bot) backMenuMarkup() *tgbotapi.InlineKeyboardMarkup {
	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.emoji("⬅️ "+b.tr("Главное меню", "Main Menu")), "menu:main"),
		),
	)
	return &markup
}

// --- Callback handling ---

func (b *Bot) handleCallbackQuery(q *tgbotapi.CallbackQuery) {
	data := q.Data

	// The preference toggles answer their callbacks with a visible toast, so
	// they are handled before the generic empty acknowledgment below.
	if data == "prefs:lang" || data == "prefs:emoji" || data == "backup:interval" || data == "backup:download" || strings.HasPrefix(data, "backup:set:") {
		b.handlePrefsCallback(q, data)
		return
	}

	callback := tgbotapi.NewCallback(q.ID, "")
	b.api.Request(callback)

	chatID := q.Message.Chat.ID
	messageID := q.Message.MessageID

	switch {
	case data == "state:cancel":
		b.clearState(chatID)
		b.showMainMenu(chatID)
	case data == "menu:main" || data == "start":
		b.showMainMenu(chatID)
	case data == "join:command":
		b.handleJoinCommand(chatID, messageID)
	case data == "bot:avatar_menu":
		b.showAvatarMenu(chatID, messageID)
	case strings.HasPrefix(data, "bot:avatar:"):
		b.handleAvatarColorSelection(q, strings.TrimPrefix(data, "bot:avatar:"))
	case data == "nodes:list":
		b.handleNodeList(chatID, messageID)
	case data == "ota:all":
		b.handleOtaAll(chatID, messageID)
	case data == "task:refresh_subs":
		b.handleRefreshAllSubs(chatID, messageID)
	case data == "purge:go":
		b.handlePurge(chatID, messageID)
	case data == "users:menu":
		b.handleUsersMenu(chatID, messageID)
	case data == "users:add":
		b.setState(chatID, &userState{Step: "add_user_creds"})
		b.editMessage(chatID, messageID,
			"<b>👥 Add User</b>\n\nSend username and password separated by space (e.g., <code>john secret123</code>).\n\nThe message will be deleted after processing.",
			b.cancelMarkup())
	case strings.HasPrefix(data, "user:create:"):
		b.handleUserCreate(chatID, messageID, strings.TrimPrefix(data, "user:create:"))
	case strings.HasPrefix(data, "user:detail:"):
		if id, err := strconv.ParseInt(strings.TrimPrefix(data, "user:detail:"), 10, 64); err == nil {
			b.showUserDetail(chatID, messageID, id, "")
		}
	case strings.HasPrefix(data, "user:role:"):
		if id, err := strconv.ParseInt(strings.TrimPrefix(data, "user:role:"), 10, 64); err == nil {
			b.showUserRolePicker(chatID, messageID, id)
		}
	case strings.HasPrefix(data, "user:setrole:"):
		rest := strings.TrimPrefix(data, "user:setrole:")
		idx := strings.Index(rest, ":")
		if idx >= 0 {
			if uid, err1 := strconv.ParseInt(rest[:idx], 10, 64); err1 == nil {
				if rid, err2 := strconv.ParseInt(rest[idx+1:], 10, 64); err2 == nil {
					b.handleUserChangeRole(chatID, messageID, uid, rid)
				}
			}
		}
	case strings.HasPrefix(data, "user:pw:"):
		if id, err := strconv.ParseInt(strings.TrimPrefix(data, "user:pw:"), 10, 64); err == nil {
			b.handleUserPwRequest(chatID, messageID, id)
		}
	case strings.HasPrefix(data, "user:del:"):
		if id, err := strconv.ParseInt(strings.TrimPrefix(data, "user:del:"), 10, 64); err == nil {
			b.handleUserDeleteRequest(chatID, messageID, id)
		}
	case strings.HasPrefix(data, "user:delconfirm:"):
		if id, err := strconv.ParseInt(strings.TrimPrefix(data, "user:delconfirm:"), 10, 64); err == nil {
			b.handleUserDeleteConfirm(chatID, messageID, id)
		}
	case data == "roles:menu":
		b.handleRolesMenu(chatID, messageID)
	case data == "roles:add":
		b.setState(chatID, &userState{Step: "add_role_name"})
		b.editMessage(chatID, messageID,
			"<b>🛡️ Add Role</b>\n\nSend the role name (e.g., <code>manager</code>):",
			b.cancelMarkup())
	case strings.HasPrefix(data, "role:detail:"):
		if id, err := strconv.ParseInt(strings.TrimPrefix(data, "role:detail:"), 10, 64); err == nil {
			b.showRoleDetail(chatID, messageID, id, "")
		}
	case strings.HasPrefix(data, "role:rename:"):
		if id, err := strconv.ParseInt(strings.TrimPrefix(data, "role:rename:"), 10, 64); err == nil {
			b.handleRoleRenameRequest(chatID, messageID, id)
		}
	case strings.HasPrefix(data, "role:rank:"):
		if id, err := strconv.ParseInt(strings.TrimPrefix(data, "role:rank:"), 10, 64); err == nil {
			b.handleRoleRankRequest(chatID, messageID, id)
		}
	case strings.HasPrefix(data, "role:del:"):
		if id, err := strconv.ParseInt(strings.TrimPrefix(data, "role:del:"), 10, 64); err == nil {
			b.handleRoleDeleteRequest(chatID, messageID, id)
		}
	case strings.HasPrefix(data, "role:delconfirm:"):
		if id, err := strconv.ParseInt(strings.TrimPrefix(data, "role:delconfirm:"), 10, 64); err == nil {
			b.handleRoleDeleteConfirm(chatID, messageID, id)
		}
	case strings.HasPrefix(data, "node:detail:"):
		b.showNodeDetail(chatID, messageID, strings.TrimPrefix(data, "node:detail:"), "")
	case strings.HasPrefix(data, "node:vpn:"):
		b.handleVpnMenu(chatID, messageID, strings.TrimPrefix(data, "node:vpn:"))
	case strings.HasPrefix(data, "node:logs:"):
		b.handleLogsMenu(chatID, messageID, strings.TrimPrefix(data, "node:logs:"))
	case strings.HasPrefix(data, "node:logs_refresh:"):
		rest := strings.TrimPrefix(data, "node:logs_refresh:")
		idx := strings.Index(rest, ":")
		if idx < 0 {
			return
		}
		b.showContainerLogs(chatID, messageID, rest[:idx], rest[idx+1:])
	case strings.HasPrefix(data, "node:logs_show:"):
		rest := strings.TrimPrefix(data, "node:logs_show:")
		idx := strings.Index(rest, ":")
		if idx < 0 {
			return
		}
		b.showContainerLogs(chatID, messageID, rest[:idx], rest[idx+1:])
	case strings.HasPrefix(data, "node:queue:"):
		b.handleQueueMenu(chatID, messageID, strings.TrimPrefix(data, "node:queue:"))
	case strings.HasPrefix(data, "node:queue_cancel:"):
		b.handleQueueCancel(chatID, messageID, strings.TrimPrefix(data, "node:queue_cancel:"))
	case strings.HasPrefix(data, "node:rename:"):
		nodeID := strings.TrimPrefix(data, "node:rename:")
		b.setState(chatID, &userState{Step: "rename_node", NodeID: nodeID})
		b.editMessage(chatID, messageID,
			fmt.Sprintf("<b>✏️ Rename</b>\n\nSend the new name for node <b>%s</b>:", b.nodeLabel(nodeID)),
			b.cancelMarkup())
	case strings.HasPrefix(data, "node:sub:"):
		nodeID := strings.TrimPrefix(data, "node:sub:")
		b.setState(chatID, &userState{Step: "set_sub", NodeID: nodeID})
		b.editMessage(chatID, messageID,
			fmt.Sprintf("<b>🔗 Set Sub URL</b>\n\nSend the new subscription URL for node <b>%s</b>:", b.nodeLabel(nodeID)),
			b.cancelMarkup())
	case strings.HasPrefix(data, "node:set_sub:"):
		// Onboarding quick-action: prompt for the subscription URL inside the
		// notification message itself.
		nodeID := strings.TrimPrefix(data, "node:set_sub:")
		b.setState(chatID, &userState{Step: "set_sub_url", NodeID: nodeID})
		b.editMessage(chatID, messageID,
			fmt.Sprintf("<b>🔗 Set Sub URL</b>\n\nPlease reply/send the Subscription URL for <b>%s</b>:", b.nodeLabel(nodeID)),
			b.cancelMarkup())
	case strings.HasPrefix(data, "node:delete:"):
		b.handleDeleteMenu(chatID, messageID, strings.TrimPrefix(data, "node:delete:"))
	case strings.HasPrefix(data, "node:switch:"):
		rest := strings.TrimPrefix(data, "node:switch:")
		idx := strings.Index(rest, ":")
		if idx < 0 {
			return
		}
		b.handleSwitch(chatID, messageID, rest[:idx], rest[idx+1:])
	case strings.HasPrefix(data, "node:softdelete:"):
		b.handleSoftDelete(chatID, messageID, strings.TrimPrefix(data, "node:softdelete:"))
	case strings.HasPrefix(data, "node:terminate:"):
		nodeID := strings.TrimPrefix(data, "node:terminate:")
		b.setState(chatID, &userState{Step: "terminate_confirm", NodeID: nodeID})
		b.editMessage(chatID, messageID,
			fmt.Sprintf("<b>💥 Terminate &amp; Self-Destruct</b>\n\nThis will destroy node <b>%s</b> on its next poll.\n\nType <code>TERMINATE</code> to confirm:",
				b.nodeLabel(nodeID)),
			b.cancelMarkup())
	}
}

// nodeLabel resolves a node's display name (falls back to the id).
func (b *Bot) nodeLabel(nodeID string) string {
	if node, err := b.repo.GetNodeByID(nodeID); err == nil && node.Name != "" {
		return node.Name
	}
	return nodeID
}

// --- Menus ---

func (b *Bot) handleNodeList(chatID int64, messageID int) {
	nodes, err := b.repo.GetAllNodes()
	if err != nil {
		log.Printf("Bot: failed to list nodes: %v", err)
		return
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	var text strings.Builder
	text.WriteString("<b>💻 Manage Nodes</b>\n\n")
	if len(nodes) == 0 {
		text.WriteString("No nodes registered yet.")
	} else {
		text.WriteString(fmt.Sprintf("%d node(s) registered.\n\nTap a node for details:", len(nodes)))
	}

	for _, n := range nodes {
		icon := "🔴"
		if time.Since(n.LastSeen) < onlineWindow {
			icon = "🟢"
		}
		name := n.Name
		if name == "" {
			name = n.ID
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("%s %s", icon, name), "node:detail:"+n.ID),
		))
	}

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔙 Back", "menu:main"),
	))
	markup := tgbotapi.NewInlineKeyboardMarkup(rows...)
	b.editMessage(chatID, messageID, text.String(), &markup)
}

func (b *Bot) showNodeDetail(chatID int64, messageID int, nodeID, note string) {
	node, err := b.repo.GetNodeByID(nodeID)
	if err != nil {
		b.editMessage(chatID, messageID, "<b>❌ Node not found.</b>", nil)
		b.handleNodeList(chatID, messageID)
		return
	}

	name := emptyDash(node.Name)
	if node.Name == "" {
		name = node.ID
	}

	pendingTask := "None"
	taskCount := 0
	if strings.TrimSpace(node.PendingCommand) != "" {
		pendingTask = "<code>" + xmlEscape(node.PendingCommand) + "</code>"
		taskCount = 1
	}

	vpn := "—"
	if node.ActiveServer != "" {
		vpn = emptyDash(node.ActiveServer)
		if node.ActiveEngine != "" || node.ActiveProto != "" {
			vpn += " (" + emptyDash(node.ActiveEngine) + "/" + emptyDash(node.ActiveProto) + ")"
		}
	}

	var text strings.Builder
	text.WriteString(fmt.Sprintf("🖥️ <b>Node:</b> %s\n", xmlEscape(name)))
	text.WriteString(fmt.Sprintf("<b>IP:</b> %s | <b>Host:</b> %s\n", emptyDash(node.IPLan), emptyDash(node.Hostname)))
	text.WriteString(fmt.Sprintf("<b>VPN:</b> %s\n", xmlEscape(vpn)))
	text.WriteString(fmt.Sprintf("<b>Sub URL:</b> %s\n", emptyDash(node.SubURL)))
	text.WriteString(fmt.Sprintf("<b>Status:</b> %s\n", emptyDash(node.StatusMessage)))
	text.WriteString(fmt.Sprintf("<b>Pending Task:</b> %s\n", pendingTask))
	if note != "" {
		text.WriteString("\n<i>" + note + "</i>")
	}

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🛡️ Switch VPN", "node:vpn:"+node.ID),
			tgbotapi.NewInlineKeyboardButtonData("🔗 Set Sub URL", "node:sub:"+node.ID),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📜 View Logs", "node:logs:"+node.ID),
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("⏳ Task Queue (%d)", taskCount), "node:queue:"+node.ID),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✏️ Rename Node", "node:rename:"+node.ID),
			tgbotapi.NewInlineKeyboardButtonData("🗑️ Delete Node", "node:delete:"+node.ID),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔙 Back to Nodes List", "nodes:list"),
		),
	)
	b.editMessage(chatID, messageID, text.String(), &markup)
}

// handleLogsMenu shows the container-selection sub-tabs for a node's logs.
func (b *Bot) handleLogsMenu(chatID int64, messageID int, nodeID string) {
	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("node-agent", "node:logs_show:"+nodeID+":node-agent"),
			tgbotapi.NewInlineKeyboardButtonData("xray-node", "node:logs_show:"+nodeID+":xray-node"),
			tgbotapi.NewInlineKeyboardButtonData("singbox-node", "node:logs_show:"+nodeID+":singbox-node"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔙 Back to Node", "node:detail:"+nodeID),
		),
	)
	text := fmt.Sprintf("📜 <b>Logs:</b> %s\n\nSelect a container:", xmlEscape(b.nodeLabel(nodeID)))
	b.editMessage(chatID, messageID, text, &markup)
}

// allowedLogContainers are the node containers selectable in the log viewer.
var allowedLogContainers = map[string]bool{"node-agent": true, "xray-node": true, "singbox-node": true}

// showContainerLogs displays the stored log tail for a node container. It
// queues a fresh get_logs command (only when nothing else is pending) so the
// agent uploads the newest tail on its next poll, then renders the most recent
// stored output. Mirrors the web dashboard's log viewer.
func (b *Bot) showContainerLogs(chatID int64, messageID int, nodeID, container string) {
	if !allowedLogContainers[container] {
		b.handleLogsMenu(chatID, messageID, nodeID)
		return
	}

	if existing, err := b.repo.GetPendingCommand(nodeID); err == nil && strings.TrimSpace(existing) == "" {
		command, _ := json.Marshal(map[string]interface{}{
			"action":    "get_logs",
			"container": container,
			"req_id":    time.Now().UnixNano(),
		})
		messageIDTs := time.Now().Unix()
		if err := b.repo.SetPendingCommand(nodeID, string(command), messageIDTs); err != nil {
			log.Printf("Bot: failed to queue get_logs for node %s: %v", nodeID, err)
		}
	}

	logs := "No logs stored for this container yet. The node will upload them on its next poll."
	logsMap := map[string]string{}
	if raw, err := b.repo.GetNodeLogs(nodeID); err == nil && strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &logsMap); err == nil {
			if tail := logsMap[container]; strings.TrimSpace(tail) != "" {
				logs = tail
			}
		}
	}

	// Trim to fit Telegram's 4096-char message limit.
	const maxLogLen = 3600
	if len(logs) > maxLogLen {
		logs = logs[len(logs)-maxLogLen:]
		logs = "...[truncated]...\n" + logs
	}

	text := fmt.Sprintf("📜 <b>Logs:</b> %s -> <b>%s</b>\n\n<code>%s</code>",
		xmlEscape(b.nodeLabel(nodeID)), xmlEscape(container), xmlEscape(logs))

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 Refresh Logs", "node:logs_refresh:"+nodeID+":"+container),
			tgbotapi.NewInlineKeyboardButtonData("🔙 Back to Node", "node:detail:"+nodeID),
		),
	)
	b.editMessage(chatID, messageID, text, &markup)
}

// handleQueueMenu shows the node's pending task with a cancel action, or the
// "no pending tasks" state when the queue is empty.
func (b *Bot) handleQueueMenu(chatID int64, messageID int, nodeID string) {
	pending, err := b.repo.GetPendingCommand(nodeID)
	if err != nil {
		log.Printf("Bot: failed to read pending command for node %s: %v", nodeID, err)
		b.showNodeDetail(chatID, messageID, nodeID, "❌ Failed to read task queue")
		return
	}

	if strings.TrimSpace(pending) == "" {
		markup := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔙 Back to Node", "node:detail:"+nodeID),
			),
		)
		text := fmt.Sprintf("⏳ <b>%s</b>\n\nNo pending tasks for this node.", xmlEscape(b.nodeLabel(nodeID)))
		b.editMessage(chatID, messageID, text, &markup)
		return
	}

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ Cancel Pending Task", "node:queue_cancel:"+nodeID),
			tgbotapi.NewInlineKeyboardButtonData("🔙 Back to Node", "node:detail:"+nodeID),
		),
	)
	text := fmt.Sprintf("⏳ <b>Pending Task for %s:</b>\n\n<code>%s</code>",
		xmlEscape(b.nodeLabel(nodeID)), xmlEscape(pending))
	b.editMessage(chatID, messageID, text, &markup)
}

// handleQueueCancel clears the node's pending command (executes clear-command
// in PostgreSQL) and confirms the result on the message.
func (b *Bot) handleQueueCancel(chatID int64, messageID int, nodeID string) {
	if err := b.repo.ClearPendingCommand(nodeID); err != nil {
		log.Printf("Bot: failed to clear pending command for node %s: %v", nodeID, err)
		b.showNodeDetail(chatID, messageID, nodeID, "❌ Failed to cancel pending task")
		return
	}
	b.repo.UpdateNodePipelineStatus(nodeID, "Idle", "Command cancelled")
	b.audit.Log("telegram_bot", audit.ActionUpdateSettings, nodeID, "Cancelled pending task (via Telegram bot)")

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔙 Back to Node", "node:detail:"+nodeID),
		),
	)
	b.editMessage(chatID, messageID, "✅ <b>Pending task cancelled.</b>", &markup)
}

func (b *Bot) handleVpnMenu(chatID int64, messageID int, nodeID string) {
	node, err := b.repo.GetNodeByID(nodeID)
	if err != nil {
		b.editMessage(chatID, messageID, "<b>❌ Node not found.</b>", nil)
		return
	}

	var text strings.Builder
	text.WriteString(fmt.Sprintf("<b>🛡️ Switch VPN</b>\n\nSelect the target server for <b>%s</b>:\n\n", b.nodeLabel(nodeID)))
	if node.ActiveServer != "" {
		text.WriteString(fmt.Sprintf("Currently active: <b>%s</b>", node.ActiveServer))
	} else {
		text.WriteString("Currently active: <i>none</i>")
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	rows = append(rows,
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⚡ Fastest", "node:switch:"+nodeID+":fastest"),
			tgbotapi.NewInlineKeyboardButtonData("⚖️ Balanced", "node:switch:"+nodeID+":balanced"),
		),
	)
	for _, srv := range node.AvailableServers {
		srv = strings.TrimSpace(srv)
		if srv == "" {
			continue
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🌐 "+srv, "node:switch:"+nodeID+":"+srv),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔙 Back", "node:detail:"+nodeID),
	))

	markup := tgbotapi.NewInlineKeyboardMarkup(rows...)
	b.editMessage(chatID, messageID, text.String(), &markup)
}

func (b *Bot) handleDeleteMenu(chatID int64, messageID int, nodeID string) {
	text := fmt.Sprintf("<b>🗑️ Delete Node</b>\n\nDelete <b>%s</b>?\n\n"+
		"• <b>Soft Delete</b> removes the node from the fleet immediately.\n"+
		"• <b>Terminate &amp; Self-Destruct</b> queues the destructive command; the agent wipes its engine containers and exits on its next poll.",
		b.nodeLabel(nodeID))

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🗑️ Soft Delete", "node:softdelete:"+nodeID),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💥 Terminate & Self-Destruct", "node:terminate:"+nodeID),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("↩️ Cancel", "node:detail:"+nodeID),
		),
	)
	b.editMessage(chatID, messageID, text, &markup)
}

// --- Actions ---

func (b *Bot) handleSwitch(chatID int64, messageID int, nodeID, target string) {
	target = strings.TrimSpace(strings.ToLower(target))
	command, _ := json.Marshal(map[string]string{"action": "switch", "outbound_tag": target})
	cmdMessageID := time.Now().Unix()
	if err := b.repo.SetPendingCommand(nodeID, string(command), cmdMessageID); err != nil {
		log.Printf("Bot: failed to queue switch for node %s: %v", nodeID, err)
		if err == repository.ErrNodeNotFound {
			b.showNodeDetail(chatID, messageID, nodeID, "❌ Node not found")
			return
		}
		b.showNodeDetail(chatID, messageID, nodeID, "❌ Failed to queue switch command")
		return
	}
	b.repo.UpdateNodePipelineStatus(nodeID, "Queued", "switch")

	b.audit.Log("telegram_bot", audit.ActionUpdateSettings, nodeID, "Switched VPN to "+target+" (via Telegram bot)")
	b.showNodeDetail(chatID, messageID, nodeID, "🛡️ Switch to <b>"+target+"</b> queued")
}

func (b *Bot) handleSoftDelete(chatID int64, messageID int, nodeID string) {
	name := b.nodeLabel(nodeID)
	if err := b.repo.DeleteNode(nodeID); err != nil {
		log.Printf("Bot: failed to delete node %s: %v", nodeID, err)
		b.showNodeDetail(chatID, messageID, nodeID, "❌ Failed to delete node")
		return
	}
	b.audit.Log("telegram_bot", audit.ActionDeleteDevice, nodeID, "Rejected and deleted node "+name+" (via Telegram bot)")
	b.editMessage(chatID, messageID,
		fmt.Sprintf("❌ Node <b>%s</b> rejected and deleted.", name),
		b.cancelMarkup())
}

// handleRefreshAllSubs mirrors the web UI "Refresh All Subscriptions" action:
// it queues an update_sub command for EVERY node (each agent re-fetches and
// re-applies its existing subscription URL). No URL input is requested.
func (b *Bot) handleRefreshAllSubs(chatID int64, messageID int) {
	nodes, err := b.repo.GetAllNodes()
	if err != nil {
		log.Printf("Bot: failed to list nodes for refresh subs: %v", err)
		b.editMessage(chatID, messageID, "<b>❌ Failed to list nodes.</b>", b.cancelMarkup())
		return
	}

	command, _ := json.Marshal(map[string]string{"action": "update_sub"})
	cmdMessageID := time.Now().Unix()
	queuedCount := 0
	for _, node := range nodes {
		if err := b.repo.SetPendingCommand(node.ID, string(command), cmdMessageID); err != nil {
			log.Printf("Bot: failed to queue update_sub for node %s: %v", node.ID, err)
		} else {
			queuedCount++
			b.repo.UpdateNodePipelineStatus(node.ID, "Queued", "update_sub")
		}
	}

	b.audit.Log("telegram_bot", audit.ActionUpdateSettings, "all_nodes", "Queued subscription refresh for "+strconv.Itoa(queuedCount)+" nodes (via Telegram bot)")
	text, markup := b.getMainMenuContent()
	b.editMessage(chatID, messageID,
		fmt.Sprintf("🔄 All <b>%d</b> node(s) queued to refresh their subscriptions.\n\n%s", len(nodes), text),
		&markup)
}

func (b *Bot) handleOtaAll(chatID int64, messageID int) {
	baseURL := "https://" + b.cfg.SubDomain
	command, _ := json.Marshal(map[string]string{
		"action":         "update_client_files",
		"agent_url":      baseURL + "/node_agent.py",
		"cli_url":        baseURL + "/fleet-cli.sh",
		"req_url":        baseURL + "/requirements.txt",
		"entrypoint_url": baseURL + "/entrypoint.sh",
	})

	nodes, err := b.repo.GetAllNodes()
	if err != nil {
		log.Printf("Bot: failed to list nodes for OTA: %v", err)
		b.editMessage(chatID, messageID, "<b>❌ Failed to list nodes.</b>", b.cancelMarkup())
		return
	}

	cmdMessageID := time.Now().Unix()
	queuedCount := 0
	for _, node := range nodes {
		if err := b.repo.SetPendingCommand(node.ID, string(command), cmdMessageID); err != nil {
			log.Printf("Bot: failed to queue OTA for node %s: %v", node.ID, err)
		} else {
			queuedCount++
			b.repo.UpdateNodePipelineStatus(node.ID, "Queued", "update_client_files")
		}
	}

	b.audit.Log("telegram_bot", audit.ActionUpdateSettings, "all_nodes", "Queued update_client_files for "+strconv.Itoa(queuedCount)+" nodes (via Telegram bot)")
	text, markup := b.getMainMenuContent()
	b.editMessage(chatID, messageID,
		fmt.Sprintf("🚀 Client files (OTA) queued for <b>%d</b> of <b>%d</b> nodes. Agents will self-update on their next poll.\n\n%s",
			queuedCount, len(nodes), text),
		&markup)
}

func (b *Bot) handlePurge(chatID int64, messageID int) {
	deleted, err := b.repo.DeleteOfflineNodes(3)
	if err != nil {
		log.Printf("Bot: purge failed: %v", err)
		b.editMessage(chatID, messageID, "<b>❌ Purge failed.</b>", b.cancelMarkup())
		return
	}
	b.audit.Log("telegram_bot", audit.ActionDeleteDevice, "all_nodes", "Purged "+strconv.FormatInt(deleted, 10)+" offline nodes older than 3 days (via Telegram bot)")

	text, markup := b.getMainMenuContent()
	b.editMessage(chatID, messageID,
		fmt.Sprintf("🧹 Purged <b>%d</b> offline node(s) (older than 3 days).\n\n%s", deleted, text),
		&markup)
}

// handleBackup creates a pg_dump + gzip backup and sends it as a Telegram
// document, then restores the main menu in the single bot message.
func (b *Bot) handleBackup(chatID int64, messageID int) {
	if messageID > 0 {
		b.editMessage(chatID, messageID, "<b>📦 Creating database backup...</b>", nil)
	} else {
		b.SendAdminMessage("<b>📦 Creating database backup...</b>")
	}

	b.sendBackupDocument(chatID, "📦 Database backup (pg_dump + gzip)")

	if messageID > 0 {
		b.showMainMenu(chatID)
	}
}

// sendBackupDocument creates the gzipped pg_dump and sends it to the admin.
func (b *Bot) sendBackupDocument(chatID int64, caption string) {
	backupPath, err := b.backupEngine.CreateGzipBackup()
	if err != nil {
		log.Printf("Bot: failed to create backup: %v", err)
		b.SendAdminMessage("<b>❌ Failed to create database backup.</b>")
		return
	}

	fileBytes, err := os.ReadFile(backupPath)
	if err != nil {
		log.Printf("Bot: failed to read backup file: %v", err)
		b.SendAdminMessage("<b>❌ Failed to read backup file.</b>")
		return
	}

	b.audit.Log("telegram_bot", audit.ActionUpdateSettings, "database", "Downloaded DB backup via Telegram bot")

	file := tgbotapi.FileBytes{Name: filepath.Base(backupPath), Bytes: fileBytes}
	doc := tgbotapi.NewDocument(chatID, file)
	doc.Caption = caption
	if _, err := b.api.Send(doc); err != nil {
		log.Printf("Bot: failed to send backup document: %v", err)
	}
}

// --- User & Role management ---

// handleUsersMenu lists all fleet users with their role/rank and offers an
// Add User button.
func (b *Bot) handleUsersMenu(chatID int64, messageID int) {
	users, err := b.repo.GetAllUsers()
	if err != nil {
		log.Printf("Bot: failed to list users: %v", err)
		b.editMessage(chatID, messageID, "<b>❌ Failed to list users.</b>", b.cancelMarkup())
		return
	}

	var text strings.Builder
	text.WriteString("<b>👥 Manage Users</b>\n\n")
	if len(users) == 0 {
		text.WriteString("No users yet.")
	} else {
		text.WriteString(fmt.Sprintf("%d user(s). Tap a user to manage:\n", len(users)))
	}

	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(users)+2)
	for _, u := range users {
		roleName := u.RoleName
		if roleName == "" {
			roleName = u.Role
		}
		label := fmt.Sprintf("👤 %s", xmlEscape(u.Username))
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, "user:detail:"+strconv.FormatInt(u.ID, 10)),
		))
	}
	rows = append(rows,
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➕ Add User", "users:add"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔙 Back", "menu:main"),
		),
	)

	markup := tgbotapi.NewInlineKeyboardMarkup(rows...)
	b.editMessage(chatID, messageID, text.String(), &markup)
}

// roleRankOf resolves the numeric rank of a role name, preferring the custom
// roles table and falling back to the built-in domain rank table.
func (b *Bot) roleRankOf(role string) int {
	roles, err := b.repo.GetAllRoles()
	if err == nil {
		for _, r := range roles {
			if r.Rank > 0 && strings.EqualFold(r.Name, role) {
				return r.Rank
			}
		}
	}
	return domain.RoleRank(role)
}

// showUserDetail renders a single user with its role/rank and the full set of
// management actions: change role, change password, delete user.
func (b *Bot) showUserDetail(chatID int64, messageID int, userID int64, note string) {
	user, err := b.repo.GetUserByID(userID)
	if err != nil {
		log.Printf("Bot: user %d not found: %v", userID, err)
		b.editMessage(chatID, messageID, "<b>❌ User not found.</b>", b.cancelMarkup())
		return
	}

	roleName := user.RoleName
	if roleName == "" {
		roleName = user.Role
	}

	var text strings.Builder
	text.WriteString("<b>👤 User Details</b>\n\n")
	text.WriteString(fmt.Sprintf("<b>Username:</b> %s\n", xmlEscape(user.Username)))
	text.WriteString(fmt.Sprintf("<b>Role:</b> %s [rank <code>%d</code>]\n", xmlEscape(roleName), b.roleRankOf(user.Role)))
	text.WriteString(fmt.Sprintf("<b>Created:</b> %s", user.CreatedAt.Format("2006-01-02 15:04")))
	if note != "" {
		text.WriteString("\n\n✅ <i>" + note + "</i>")
	}

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 Change Role", "user:role:"+strconv.FormatInt(user.ID, 10)),
			tgbotapi.NewInlineKeyboardButtonData("🔑 Change Password", "user:pw:"+strconv.FormatInt(user.ID, 10)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🗑️ Delete User", "user:del:"+strconv.FormatInt(user.ID, 10)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔙 Back to Users", "users:menu"),
		),
	)
	b.editMessage(chatID, messageID, text.String(), &markup)
}

// showUserRolePicker renders every non-owner role as a target for reassigning
// the user's role.
func (b *Bot) showUserRolePicker(chatID int64, messageID int, userID int64) {
	user, err := b.repo.GetUserByID(userID)
	if err != nil {
		b.editMessage(chatID, messageID, "<b>❌ User not found.</b>", b.cancelMarkup())
		return
	}

	roles, err := b.repo.GetAllRoles()
	if err != nil {
		b.editMessage(chatID, messageID, "<b>❌ Failed to load roles.</b>", b.cancelMarkup())
		return
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	var row []tgbotapi.InlineKeyboardButton
	for _, r := range roles {
		if strings.EqualFold(r.Name, domain.RoleOwner) {
			continue
		}
		rank := r.Rank
		if rank < 1 {
			rank = domain.RoleRank(r.Name)
		}
		row = append(row, tgbotapi.NewInlineKeyboardButtonData(
			fmt.Sprintf("%s [%d]", r.Name, rank),
			fmt.Sprintf("user:setrole:%d:%d", user.ID, r.ID),
		))
		if len(row) == 2 {
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(row...))
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(row...))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", "user:detail:"+strconv.FormatInt(user.ID, 10)),
	))

	markup := tgbotapi.NewInlineKeyboardMarkup(rows...)
	b.editMessage(chatID, messageID,
		fmt.Sprintf("<b>🔄 Change Role</b>\n\nSelect the new role for <b>%s</b>:", xmlEscape(user.Username)),
		&markup)
}

// handleUserChangeRole assigns a new role to an existing user, mirroring the
// web API's rank guards: the owner role is never assignable and the bot only
// ever reassigns strictly lower-ranked roles.
func (b *Bot) handleUserChangeRole(chatID int64, messageID int, userID, roleID int64) {
	user, err := b.repo.GetUserByID(userID)
	if err != nil {
		b.editMessage(chatID, messageID, "<b>❌ User not found.</b>", b.cancelMarkup())
		return
	}
	role, err := b.repo.GetRoleByID(roleID)
	if err != nil || role == nil {
		b.editMessage(chatID, messageID, "<b>❌ Role not found.</b>", b.cancelMarkup())
		return
	}
	if strings.EqualFold(role.Name, domain.RoleOwner) {
		b.editMessage(chatID, messageID, "<b>❌ The owner role cannot be assigned to a user.</b>", b.cancelMarkup())
		return
	}
	if strings.EqualFold(user.Username, "admin") || strings.EqualFold(user.Role, domain.RoleOwner) {
		b.editMessage(chatID, messageID, "<b>❌ The owner account cannot be reassigned.</b>", b.cancelMarkup())
		return
	}

	oldRole := user.RoleName
	if oldRole == "" {
		oldRole = user.Role
	}
	if strings.EqualFold(oldRole, role.Name) {
		b.editMessage(chatID, messageID, "<b>ℹ️ User already has this role.</b>", b.cancelMarkup())
		return
	}

	user.Role = role.Name
	user.RoleName = role.Name
	user.ColorHex = role.ColorHex
	if err := b.repo.UpdateUser(user); err != nil {
		log.Printf("Bot: failed to update role for user %d: %v", userID, err)
		b.editMessage(chatID, messageID, "<b>❌ Failed to update role.</b>", b.cancelMarkup())
		return
	}

	b.audit.Log("telegram_bot", audit.ActionUpdateUser, user.Username, "Changed role from "+oldRole+" to "+role.Name+" (via Telegram bot)")
	b.showUserDetail(chatID, messageID, userID, "Role changed to "+role.Name)
}

// handleUserPwRequest opens the password-change prompt; the next text message
// becomes the new password (and is deleted after processing).
func (b *Bot) handleUserPwRequest(chatID int64, messageID int, userID int64) {
	user, err := b.repo.GetUserByID(userID)
	if err != nil {
		b.editMessage(chatID, messageID, "<b>❌ User not found.</b>", b.cancelMarkup())
		return
	}
	b.setState(chatID, &userState{Step: "user_pw", TargetID: userID})
	b.editMessage(chatID, messageID,
		fmt.Sprintf("<b>🔑 Change Password</b>\n\nSend the new password for <b>%s</b>:\n\nThe message will be deleted after processing.", xmlEscape(user.Username)),
		b.cancelMarkup())
}

// processChangeUserPwText captures the new password, hashes it and updates the
// target user.
func (b *Bot) processChangeUserPwText(chatID int64, text string) {
	state := b.getState(chatID)
	b.clearState(chatID)

	password := strings.TrimSpace(text)
	if password == "" {
		b.editMessage(chatID, b.getMainMenuID(chatID),
			"<b>🔑 Change Password</b>\n\nPassword cannot be empty. Send a password or press Cancel:",
			b.cancelMarkup())
		return
	}

	user, err := b.repo.GetUserByID(state.TargetID)
	if err != nil {
		b.editMessage(chatID, b.getMainMenuID(chatID), "<b>❌ User not found.</b>", b.cancelMarkup())
		return
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("Bot: failed to hash new password for user %d: %v", state.TargetID, err)
		b.editMessage(chatID, b.getMainMenuID(chatID), "<b>❌ Failed to update password.</b>", b.cancelMarkup())
		return
	}

	if err := b.repo.UpdateUserPassword(state.TargetID, string(hashed)); err != nil {
		log.Printf("Bot: failed to update password for user %d: %v", state.TargetID, err)
		b.editMessage(chatID, b.getMainMenuID(chatID), "<b>❌ Failed to update password.</b>", b.cancelMarkup())
		return
	}

	b.audit.Log("telegram_bot", audit.ActionUpdateUser, user.Username, "Changed password (via Telegram bot)")
	b.showUserDetail(chatID, b.getMainMenuID(chatID), state.TargetID, "Password updated")
}

// handleUserDeleteRequest asks for explicit confirmation before deleting a
// user; the owner account and admin are always protected.
func (b *Bot) handleUserDeleteRequest(chatID int64, messageID int, userID int64) {
	user, err := b.repo.GetUserByID(userID)
	if err != nil {
		b.editMessage(chatID, messageID, "<b>❌ User not found.</b>", b.cancelMarkup())
		return
	}
	if strings.EqualFold(user.Username, "admin") || strings.EqualFold(user.Role, domain.RoleOwner) {
		b.editMessage(chatID, messageID, "<b>❌ The owner account cannot be deleted.</b>", b.cancelMarkup())
		return
	}

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🗑️ Yes, Delete", "user:delconfirm:"+strconv.FormatInt(user.ID, 10)),
			tgbotapi.NewInlineKeyboardButtonData("↩️ Cancel", "user:detail:"+strconv.FormatInt(user.ID, 10)),
		),
	)
	b.editMessage(chatID, messageID,
		fmt.Sprintf("<b>🗑️ Delete User</b>\n\nAre you sure you want to permanently delete <b>%s</b>? This cannot be undone.", xmlEscape(user.Username)),
		&markup)
}

// handleUserDeleteConfirm performs the actual user deletion.
func (b *Bot) handleUserDeleteConfirm(chatID int64, messageID int, userID int64) {
	user, err := b.repo.GetUserByID(userID)
	if err != nil {
		b.editMessage(chatID, messageID, "<b>❌ User not found.</b>", b.cancelMarkup())
		return
	}
	if strings.EqualFold(user.Username, "admin") || strings.EqualFold(user.Role, domain.RoleOwner) {
		b.editMessage(chatID, messageID, "<b>❌ The owner account cannot be deleted.</b>", b.cancelMarkup())
		return
	}

	username := user.Username
	if err := b.repo.DeleteUser(userID); err != nil {
		log.Printf("Bot: failed to delete user %d: %v", userID, err)
		b.editMessage(chatID, messageID, "<b>❌ Failed to delete user.</b>", b.cancelMarkup())
		return
	}

	b.audit.Log("telegram_bot", audit.ActionDeleteUser, username, "Deleted user via Telegram bot")
	b.handleUsersMenu(chatID, messageID)
}

// processAddUserCredsText parses "username password" from the admin's reply,
// validates that the username is free, then moves to the role-selection step.
func (b *Bot) processAddUserCredsText(chatID int64, text string) {
	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) != 2 {
		b.editMessage(chatID, b.getMainMenuID(chatID),
			"<b>👥 Add User</b>\n\nInvalid input. Send <b>username</b> and <b>password</b> separated by a space (e.g., <code>john secret123</code>):",
			b.cancelMarkup())
		return
	}

	username := parts[0]
	password := parts[1]

	if _, err := b.repo.GetUserByUsername(username); err == nil {
		b.editMessage(chatID, b.getMainMenuID(chatID),
			"<b>❌ Username already exists.</b> Please send a different username and password:",
			b.cancelMarkup())
		return
	}

	b.setState(chatID, &userState{Step: "add_user_role", Username: username, Password: password})
	b.showRoleSelection(chatID, b.getMainMenuID(chatID), username, "")
}

// showRoleSelection renders every non-owner role from the database as an
// inline keyboard for the in-progress add-user flow.
func (b *Bot) showRoleSelection(chatID int64, messageID int, username, note string) {
	roles, err := b.repo.GetAllRoles()
	if err != nil {
		log.Printf("Bot: failed to list roles for user creation: %v", err)
		b.editMessage(chatID, messageID, "<b>❌ Failed to load roles.</b>", b.cancelMarkup())
		return
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	var row []tgbotapi.InlineKeyboardButton
	for _, r := range roles {
		if strings.EqualFold(r.Name, domain.RoleOwner) {
			continue
		}
		row = append(row, tgbotapi.NewInlineKeyboardButtonData(r.Name, "user:create:"+strconv.FormatInt(r.ID, 10)))
		if len(row) == 2 {
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(row...))
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(row...))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", "state:cancel"),
	))

	var text strings.Builder
	text.WriteString("<b>👥 Add User</b>\n\nSelect a role for <b>" + xmlEscape(username) + "</b>:")
	if note != "" {
		text.WriteString("\n\n<i>" + note + "</i>")
	}
	markup := tgbotapi.NewInlineKeyboardMarkup(rows...)
	b.editMessage(chatID, messageID, text.String(), &markup)
}

// handleUserCreate finishes the add-user flow: it resolves the chosen role,
// hashes the stored password and persists the new fleet user.
func (b *Bot) handleUserCreate(chatID int64, messageID int, roleIDStr string) {
	state := b.getState(chatID)
	if state == nil || state.Step != "add_user_role" || state.Username == "" || state.Password == "" {
		b.clearState(chatID)
		b.editMessage(chatID, messageID, "<b>⚠️ Add-user flow expired.</b> Please start again from the main menu.", b.cancelMarkup())
		return
	}

	roleID, err := strconv.ParseInt(roleIDStr, 10, 64)
	if err != nil {
		b.editMessage(chatID, messageID, "<b>❌ Invalid role.</b>", b.cancelMarkup())
		return
	}
	role, err := b.repo.GetRoleByID(roleID)
	if err != nil || role == nil || strings.EqualFold(role.Name, domain.RoleOwner) {
		log.Printf("Bot: role %d not found for user creation: %v", roleID, err)
		b.editMessage(chatID, messageID, "<b>❌ Role not found.</b>", b.cancelMarkup())
		return
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(state.Password), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("Bot: failed to hash password for user %q: %v", state.Username, err)
		b.editMessage(chatID, messageID, "<b>❌ Failed to create user.</b>", b.cancelMarkup())
		return
	}

	newUser := &domain.User{
		Username:     state.Username,
		PasswordHash: string(hashed),
		Role:         role.Name,
		CreatedAt:    time.Now(),
		ColorHex:     role.ColorHex,
	}
	if _, err := b.repo.AddUser(newUser); err != nil {
		log.Printf("Bot: failed to create user %q: %v", state.Username, err)
		b.editMessage(chatID, messageID, "<b>❌ Failed to create user.</b> The username may already be taken.", b.cancelMarkup())
		return
	}

	b.audit.Log("telegram_bot", audit.ActionCreateUser, state.Username, "Created user via Telegram bot with role "+role.Name)
	b.clearState(chatID)

	text, markup := b.getMainMenuContent()
	b.editMessage(chatID, messageID,
		fmt.Sprintf("✅ <b>User created!</b>\n\n<b>Username:</b> %s\n<b>Role:</b> %s\n\n%s",
			xmlEscape(state.Username), xmlEscape(role.Name), text),
		&markup)
}

// handleRolesMenu lists all defined roles (with their ranks) and offers an
// Add Role button.
func (b *Bot) handleRolesMenu(chatID int64, messageID int) {
	roles, err := b.repo.GetAllRoles()
	if err != nil {
		log.Printf("Bot: failed to list roles: %v", err)
		b.editMessage(chatID, messageID, "<b>❌ Failed to list roles.</b>", b.cancelMarkup())
		return
	}

	var text strings.Builder
	text.WriteString("<b>🛡️ Manage Roles</b>\n\n")
	if len(roles) == 0 {
		text.WriteString("No roles defined.")
	} else {
		text.WriteString(fmt.Sprintf("%d role(s). Tap a role to manage:\n", len(roles)))
	}

	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(roles)+2)
	for _, r := range roles {
		rank := r.Rank
		if rank < 1 {
			rank = domain.RoleRank(r.Name)
		}
		label := fmt.Sprintf("🛡️ %s [rank %d]", xmlEscape(r.Name), rank)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, "role:detail:"+strconv.FormatInt(r.ID, 10)),
		))
	}
	rows = append(rows,
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➕ Add Role", "roles:add"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔙 Back", "menu:main"),
		),
	)

	markup := tgbotapi.NewInlineKeyboardMarkup(rows...)
	b.editMessage(chatID, messageID, text.String(), &markup)
}

// showRoleDetail renders a single role with its rank, user count and the full
// set of management actions: rename, change rank, delete.
func (b *Bot) showRoleDetail(chatID int64, messageID int, roleID int64, note string) {
	role, err := b.repo.GetRoleByID(roleID)
	if err != nil || role == nil {
		log.Printf("Bot: role %d not found: %v", roleID, err)
		b.editMessage(chatID, messageID, "<b>❌ Role not found.</b>", b.cancelMarkup())
		return
	}

	rank := role.Rank
	if rank < 1 {
		rank = domain.RoleRank(role.Name)
	}
	userCount, _ := b.repo.CountUsersByRoleName(role.Name)

	var text strings.Builder
	text.WriteString("<b>🛡️ Role Details</b>\n\n")
	text.WriteString(fmt.Sprintf("<b>Name:</b> %s\n", xmlEscape(role.Name)))
	text.WriteString(fmt.Sprintf("<b>Rank:</b> <code>%d</code>\n", rank))
	text.WriteString(fmt.Sprintf("<b>Users assigned:</b> %d\n", userCount))
	if role.OwnerID == "system" {
		text.WriteString("<b>Type:</b> system role")
	}
	if note != "" {
		text.WriteString("\n\n✅ <i>" + note + "</i>")
	}

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✏️ Rename Role", "role:rename:"+strconv.FormatInt(role.ID, 10)),
			tgbotapi.NewInlineKeyboardButtonData("🔢 Change Rank", "role:rank:"+strconv.FormatInt(role.ID, 10)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🗑️ Delete Role", "role:del:"+strconv.FormatInt(role.ID, 10)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔙 Back to Roles", "roles:menu"),
		),
	)
	b.editMessage(chatID, messageID, text.String(), &markup)
}

// handleRoleRenameRequest opens the rename prompt; the next text message
// becomes the role's new name, guarded against reserved/duplicate names.
func (b *Bot) handleRoleRenameRequest(chatID int64, messageID int, roleID int64) {
	role, err := b.repo.GetRoleByID(roleID)
	if err != nil || role == nil {
		b.editMessage(chatID, messageID, "<b>❌ Role not found.</b>", b.cancelMarkup())
		return
	}
	if role.OwnerID == "system" {
		b.editMessage(chatID, messageID, "<b>❌ System roles cannot be renamed.</b>", b.cancelMarkup())
		return
	}

	b.setState(chatID, &userState{Step: "role_rename", TargetID: roleID})
	b.editMessage(chatID, messageID,
		fmt.Sprintf("<b>✏️ Rename Role</b>\n\nSend the new name for role <b>%s</b>\n(owner, admin, client, viewer are reserved):", xmlEscape(role.Name)),
		b.cancelMarkup())
}

// processRenameRoleText validates and applies the new role name.
func (b *Bot) processRenameRoleText(chatID int64, text string) {
	state := b.getState(chatID)
	b.clearState(chatID)

	name := strings.TrimSpace(text)
	if name == "" {
		b.editMessage(chatID, b.getMainMenuID(chatID),
			"<b>✏️ Rename Role</b>\n\nRole name cannot be empty. Send a name or press Cancel:",
			b.cancelMarkup())
		return
	}

	lower := strings.ToLower(name)
	if lower == "owner" || lower == "admin" || lower == "client" || lower == "viewer" {
		b.editMessage(chatID, b.getMainMenuID(chatID),
			"<b>❌ Reserved role name.</b> Please choose a different name:",
			b.cancelMarkup())
		return
	}
	if _, err := b.repo.GetRoleByName(name); err == nil {
		b.editMessage(chatID, b.getMainMenuID(chatID),
			"<b>❌ Role name already exists.</b> Please choose a different name:",
			b.cancelMarkup())
		return
	}

	role, err := b.repo.GetRoleByID(state.TargetID)
	if err != nil || role == nil || role.OwnerID == "system" {
		b.editMessage(chatID, b.getMainMenuID(chatID), "<b>❌ Role not found.</b>", b.cancelMarkup())
		return
	}

	oldName := role.Name
	role.Name = name
	if err := b.repo.UpdateCustomRole(role); err != nil {
		log.Printf("Bot: failed to rename role %d: %v", state.TargetID, err)
		b.editMessage(chatID, b.getMainMenuID(chatID), "<b>❌ Failed to rename role.</b>", b.cancelMarkup())
		return
	}

	b.audit.Log("telegram_bot", audit.ActionUpdateSettings, role.Name, "Renamed role from "+oldName+" to "+name+" (via Telegram bot)")
	b.showRoleDetail(chatID, b.getMainMenuID(chatID), state.TargetID, "Renamed to "+role.Name)
}

// handleRoleRankRequest opens the rank prompt; the next text message becomes
// the role's new rank (1-99).
func (b *Bot) handleRoleRankRequest(chatID int64, messageID int, roleID int64) {
	role, err := b.repo.GetRoleByID(roleID)
	if err != nil || role == nil {
		b.editMessage(chatID, messageID, "<b>❌ Role not found.</b>", b.cancelMarkup())
		return
	}
	if role.OwnerID == "system" {
		b.editMessage(chatID, messageID, "<b>❌ System role ranks are fixed.</b>", b.cancelMarkup())
		return
	}
	if role.Name == domain.RoleOwner {
		b.editMessage(chatID, messageID, "<b>❌ The owner role rank is immutable.</b>", b.cancelMarkup())
		return
	}

	b.setState(chatID, &userState{Step: "role_rank", TargetID: roleID})
	b.editMessage(chatID, messageID,
		fmt.Sprintf("<b>🔢 Change Rank</b>\n\nSend the new rank (<b>1-99</b>) for role <b>%s</b>:", xmlEscape(role.Name)),
		b.cancelMarkup())
}

// processChangeRoleRankText validates and applies the new role rank.
func (b *Bot) processChangeRoleRankText(chatID int64, text string) {
	state := b.getState(chatID)
	b.clearState(chatID)

	rank, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || rank < 1 || rank > 99 {
		b.editMessage(chatID, b.getMainMenuID(chatID),
			"<b>🔢 Change Rank</b>\n\nInvalid rank. Send a whole number between <b>1</b> and <b>99</b>:",
			b.cancelMarkup())
		return
	}

	role, err := b.repo.GetRoleByID(state.TargetID)
	if err != nil || role == nil || role.OwnerID == "system" {
		b.editMessage(chatID, b.getMainMenuID(chatID), "<b>❌ Role not found.</b>", b.cancelMarkup())
		return
	}

	oldRank := role.Rank
	role.Rank = rank
	if err := b.repo.UpdateCustomRole(role); err != nil {
		log.Printf("Bot: failed to change rank for role %d: %v", state.TargetID, err)
		b.editMessage(chatID, b.getMainMenuID(chatID), "<b>❌ Failed to update rank.</b>", b.cancelMarkup())
		return
	}

	b.audit.Log("telegram_bot", audit.ActionUpdateSettings, role.Name, "Changed rank from "+strconv.Itoa(oldRank)+" to "+strconv.Itoa(rank)+" (via Telegram bot)")
	b.showRoleDetail(chatID, b.getMainMenuID(chatID), state.TargetID, "Rank updated to "+strconv.Itoa(rank))
}

// handleRoleDeleteRequest asks for explicit confirmation. System roles and the
// owner role are never deletable; roles that still have assigned users also
// cannot be deleted.
func (b *Bot) handleRoleDeleteRequest(chatID int64, messageID int, roleID int64) {
	role, err := b.repo.GetRoleByID(roleID)
	if err != nil || role == nil {
		b.editMessage(chatID, messageID, "<b>❌ Role not found.</b>", b.cancelMarkup())
		return
	}
	if role.OwnerID == "system" || role.Name == domain.RoleOwner {
		b.editMessage(chatID, messageID, "<b>❌ System and owner roles cannot be deleted.</b>", b.cancelMarkup())
		return
	}

	userCount, err := b.repo.CountUsersByRoleName(role.Name)
	if err == nil && userCount > 0 {
		b.editMessage(chatID, messageID,
			fmt.Sprintf("<b>❌ Role <b>%s</b> is assigned to %d user(s). Reassign or delete them first.</b>", xmlEscape(role.Name), userCount),
			b.cancelMarkup())
		return
	}

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🗑️ Yes, Delete", "role:delconfirm:"+strconv.FormatInt(role.ID, 10)),
			tgbotapi.NewInlineKeyboardButtonData("↩️ Cancel", "role:detail:"+strconv.FormatInt(role.ID, 10)),
		),
	)
	b.editMessage(chatID, messageID,
		fmt.Sprintf("<b>🗑️ Delete Role</b>\n\nAre you sure you want to permanently delete role <b>%s</b>? This cannot be undone.", xmlEscape(role.Name)),
		&markup)
}

// handleRoleDeleteConfirm performs the actual role deletion.
func (b *Bot) handleRoleDeleteConfirm(chatID int64, messageID int, roleID int64) {
	role, err := b.repo.GetRoleByID(roleID)
	if err != nil || role == nil {
		b.editMessage(chatID, messageID, "<b>❌ Role not found.</b>", b.cancelMarkup())
		return
	}
	if role.OwnerID == "system" || role.Name == domain.RoleOwner {
		b.editMessage(chatID, messageID, "<b>❌ System roles cannot be deleted.</b>", b.cancelMarkup())
		return
	}

	userCount, _ := b.repo.CountUsersByRoleName(role.Name)
	if userCount > 0 {
		b.editMessage(chatID, messageID, "<b>❌ Role still has assigned users.</b>", b.cancelMarkup())
		return
	}

	if err := b.repo.DeleteCustomRole(roleID); err != nil {
		log.Printf("Bot: failed to delete role %d: %v", roleID, err)
		b.editMessage(chatID, messageID, "<b>❌ Failed to delete role.</b>", b.cancelMarkup())
		return
	}

	b.audit.Log("telegram_bot", audit.ActionDeleteUser, role.Name, "Deleted role via Telegram bot")
	b.handleRolesMenu(chatID, messageID)
}

// processAddRoleNameText captures the new role's name and moves the flow on to
// the rank prompt.
func (b *Bot) processAddRoleNameText(chatID int64, text string) {
	name := strings.TrimSpace(text)
	if name == "" {
		b.editMessage(chatID, b.getMainMenuID(chatID),
			"<b>🛡️ Add Role</b>\n\nRole name cannot be empty. Send the role name (e.g., <code>manager</code>):",
			b.cancelMarkup())
		return
	}

	lower := strings.ToLower(name)
	if lower == "owner" || lower == "admin" || lower == "client" || lower == "viewer" {
		b.editMessage(chatID, b.getMainMenuID(chatID),
			"<b>❌ Reserved role name.</b> Please choose a different role name:",
			b.cancelMarkup())
		return
	}
	if _, err := b.repo.GetRoleByName(name); err == nil {
		b.editMessage(chatID, b.getMainMenuID(chatID),
			"<b>❌ Role name already exists.</b> Please choose a different name:",
			b.cancelMarkup())
		return
	}

	b.setState(chatID, &userState{Step: "add_role_rank", RoleName: name})
	b.editMessage(chatID, b.getMainMenuID(chatID),
		fmt.Sprintf("<b>🛡️ Add Role</b>\n\nRole name <b>%s</b> accepted.\n\nSend the role rank (<b>1-99</b>):", xmlEscape(name)),
		b.cancelMarkup())
}

// processAddRoleRankText validates the 1-99 rank and creates the role with
// empty (web-editable) permissions.
func (b *Bot) processAddRoleRankText(chatID int64, text string) {
	state := b.getState(chatID)

	rank, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || rank < 1 || rank > 99 {
		b.editMessage(chatID, b.getMainMenuID(chatID),
			"<b>🛡️ Add Role</b>\n\nInvalid rank. Send a whole number between <b>1</b> and <b>99</b>:",
			b.cancelMarkup())
		return
	}

	if state == nil || state.RoleName == "" {
		b.clearState(chatID)
		b.handleRolesMenu(chatID, b.getMainMenuID(chatID))
		return
	}

	role := &domain.CustomRole{
		Name:            state.RoleName,
		ColorHex:        "#6B7280",
		OwnerID:         "telegram_bot",
		PermissionsJSON: "[]",
		Rank:            rank,
		CreatedAt:       time.Now(),
	}
	if _, err := b.repo.AddCustomRole(role); err != nil {
		log.Printf("Bot: failed to create role %q: %v", state.RoleName, err)
		b.editMessage(chatID, b.getMainMenuID(chatID),
			"<b>❌ Failed to create role.</b> It may already exist.",
			b.cancelMarkup())
		return
	}

	b.audit.Log("telegram_bot", audit.ActionCreateUser, role.Name, "Created role via Telegram bot (rank: "+strconv.Itoa(rank)+")")
	b.clearState(chatID)
	b.handleRolesMenu(chatID, b.getMainMenuID(chatID))
}

// emptyDash returns a placeholder for empty strings.
func emptyDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// xmlEscape escapes HTML-significant characters so dynamic node/user-supplied
// text is safe to render with Telegram's HTML parse mode.
func xmlEscape(s string) string {
	if s == "" {
		return s
	}
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;")
	return r.Replace(s)
}

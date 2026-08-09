package bot

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"malaxis-fleet/internal/audit"
	"malaxis-fleet/internal/backup"
	"malaxis-fleet/internal/config"
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
	"indigo":  "avatar_indigo.png",
	"emerald": "avatar_emerald.png",
	"amber":   "avatar_amber.png",
	"rose":    "avatar_rose.png",
	"cyan":    "avatar_cyan.png",
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

// --- Backup ---

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
				b.sendBackupDocument(b.chatID, "⏰ "+b.tr("Плановая резервная копия", "Scheduled backup"))
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

// handleBackup creates a pg_dump + gzip backup and sends it as a Telegram
// document, then restores the main menu in the single bot message. Strictly
// owner-only: it exits silently for any chat other than tg_admin_chat_id.
func (b *Bot) handleBackup(chatID int64, messageID int) {
	if !b.isAdminChat(chatID) {
		return
	}
	if messageID > 0 {
		b.editMessage(chatID, messageID, "<b>📦 "+b.tr("Создание резервной копии БД...", "Creating database backup...")+"</b>", nil)
	} else {
		b.SendAdminMessage("<b>📦 " + b.tr("Создание резервной копии БД...", "Creating database backup...") + "</b>")
	}

	b.sendBackupDocument(chatID, "📦 "+b.tr("Резервная копия БД (pg_dump + gzip)", "Database backup (pg_dump + gzip)"))

	if messageID > 0 {
		b.showMainMenu(chatID)
	}
}

// sendBackupDocument creates the gzipped pg_dump and sends it to the admin.
func (b *Bot) sendBackupDocument(chatID int64, caption string) {
	backupPath, err := b.backupEngine.CreateGzipBackup()
	if err != nil {
		log.Printf("Bot: failed to create backup: %v", err)
		b.SendAdminMessage("<b>❌ " + b.tr("Не удалось создать резервную копию БД.", "Failed to create database backup.") + "</b>")
		return
	}

	fileBytes, err := os.ReadFile(backupPath)
	if err != nil {
		log.Printf("Bot: failed to read backup file: %v", err)
		b.SendAdminMessage("<b>❌ " + b.tr("Не удалось прочитать файл резервной копии.", "Failed to read backup file.") + "</b>")
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

// --- Preferences ---

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
			return r >= 0x1F000 && r <= 0x1FAFF || r >= 0x2600 && r <= 0x27BF || r >= 0x2300 && r <= 0x23FF ||
				r >= 0x2B00 && r <= 0x2BFF || r >= 0x1F1E6 && r <= 0x1F1FF || r == 0xFE0F || r == 0x200D
		})
		return strings.TrimSpace(label)
	}
	return label
}

// fmtBtn renders an inline keyboard button label with an optional emoji
// prefix: the emoji is only prepended when emoji rendering is enabled for the
// bot, guaranteeing a strict no-emoji button surface when disabled.
func (b *Bot) fmtBtn(label string, emoji string, emojisEnabled bool) string {
	if !emojisEnabled {
		return label
	}
	return emoji + " " + label
}

// btn is the localization-aware shortcut for fmtBtn: it resolves the stored
// bot language and emoji flag, then formats the button label.
func (b *Bot) btn(emoji, ru, en string) string {
	_, enabled := b.botPrefs()
	return b.fmtBtn(b.tr(ru, en), emoji, enabled)
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
	if err := b.repo.UpdateUserPreferences(user.ID, *prefs); err != nil {
		log.Printf("ERROR: failed to persist preferences for user %d: %v", user.ID, err)
	}
}

// getMainMenuContent builds the main menu: node online/offline counters and
// the fleet action buttons.
func (b *Bot) getMainMenuContent() (string, tgbotapi.InlineKeyboardMarkup) {
	nodes, err := b.repo.GetAllNodes()
	if err != nil {
		log.Printf("ERROR: failed to load nodes for main menu: %v", err)
	}
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
			tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn(b.tr("Добавить устройство", "Add New Device"), "➕", emojis), "join:command"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn(b.tr("Управление узлами", "Manage Nodes"), "💻", emojis), "nodes:list"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn(b.tr("Отправить файлы клиентов (OTA)", "Push Client Files (OTA)"), "🚀", emojis), "ota:all"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn(b.tr("Обновить подписки", "Refresh Subscriptions"), "🔄", emojis), "task:refresh_subs"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn(b.tr("Очистить офлайн (>3д)", "Purge Offline (>3d)"), "🧹", emojis), "purge:go"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn(b.tr("Скачать бэкап БД", "Download DB Backup"), "📦", emojis), "backup:download"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn(b.tr("Частота бэкапов", "Backup Interval"), "⏱️", emojis), "backup:interval"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn(b.tr("Пользователи", "Manage Users"), "👥", emojis), "users:menu"),
			tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn(b.tr("Роли", "Manage Roles"), "🛡️", emojis), "roles:menu"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn(b.tr("Выбрать цвет аватарки", "Select Bot Avatar Color"), "🎨", emojis), "bot:avatar_menu"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn(b.tr("Язык: RU", "Language: EN"), "🌐", emojis), "prefs:lang"),
			tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn(b.tr("Эмодзи: "+emojiState, "Emojis: "+emojiState), "😃", emojis), "prefs:emoji"),
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
		b.showRoleSelection(chatID, b.getMainMenuID(chatID), state.Username, b.tr("Нажмите на роль ниже, чтобы завершить создание пользователя:", "Tap a role below to finish creating the user:"))
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

// --- Onboarding & utilities ---

// handleJoinCommand shows the tokenized node onboarding command. The curl line
// is rendered as a <code> block so Telegram lets the admin copy it on tap.
func (b *Bot) handleJoinCommand(chatID int64, messageID int) {
	if b.cfg.JoinDomain == "" || b.cfg.FleetSecret == "" {
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Домен подключения или секрет флота не настроены.", "Join domain or fleet secret is not configured.")+"</b>", b.backMenuMarkup())
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
	// Backup operations are strictly restricted to the primary owner chat
	// (tg_admin_chat_id) — pollUpdates already filters every update, this is
	// explicit defense-in-depth for the DB-backup surface.
	if (data == "backup:download" || data == "backup:interval" || strings.HasPrefix(data, "backup:set:")) && !b.isAdminChat(q.Message.Chat.ID) {
		b.api.Request(tgbotapi.NewCallback(q.ID, "❌ "+b.tr("Доступ только владельцу", "Owner only")))
		return
	}
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

// isAdminChat reports whether the given Telegram chat is the primary owner
// chat (the configured tg_admin_chat_id). Backup operations are strictly
// restricted to it.
func (b *Bot) isAdminChat(chatID int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.chatID != 0 && chatID == b.chatID
}

// showBackupIntervalPicker renders the backup-frequency picker keyboard.
func (b *Bot) showBackupIntervalPicker(chatID int64, messageID int) {
	interval := b.backupIntervalHours()
	_, emojisEnabled := b.botPrefs()
	text := fmt.Sprintf("<b>%s</b>\n\n%s: <b>%d %s</b>",
		b.fmtBtn(b.tr("Частота бэкапов", "Backup Interval"), "⏱️", emojisEnabled),
		b.tr("Текущий интервал", "Current interval"),
		interval, b.tr("ч", "h"))

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn(b.tr("6 ч", "6 h"), "🕐", emojisEnabled), "backup:set:6"),
			tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn(b.tr("12 ч", "12 h"), "🕜", emojisEnabled), "backup:set:12"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn(b.tr("24 ч (1 раз в сутки)", "24 h (daily)"), "🕐", emojisEnabled), "backup:set:24"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn(b.tr("168 ч (1 раз в неделю)", "168 h (weekly)"), "📅", emojisEnabled), "backup:set:168"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn(b.tr("Главное меню", "Main Menu"), "⬅️", emojisEnabled), "menu:main"),
		),
	)

	b.editMessage(chatID, messageID, text, &markup)
}

// --- Avatar menu ---

// avatarColorDisplay maps the lowercase color keys to their display names.
var avatarColorDisplay = map[string]string{
	"indigo":  "Indigo",
	"emerald": "Emerald",
	"amber":   "Amber",
	"rose":    "Rose",
	"cyan":    "Cyan",
}

// showAvatarMenu renders the bot avatar color picker sub-menu. The five
// color options match the dashboard accent palette (avatarColors).
func (b *Bot) showAvatarMenu(chatID int64, messageID int) {
	text := "<b>🎨 " + b.tr("Выбор цвета аватарки", "Select Bot Avatar Color") + "</b>\n\n" +
		b.tr("Выберите цвет, соответствующий вашей теме:", "Choose a color matching your theme:")

	_, emojis := b.botPrefs()

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn("Indigo", "🟣", emojis), "bot:avatar:indigo"),
			tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn("Emerald", "🟢", emojis), "bot:avatar:emerald"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn("Amber", "🟠", emojis), "bot:avatar:amber"),
			tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn("Rose", "🔴", emojis), "bot:avatar:rose"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn("Cyan", "🔵", emojis), "bot:avatar:cyan"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn(b.tr("В главное меню", "Back to Main Menu"), "🔙", emojis), "menu:main"),
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
	_, emojis := b.botPrefs()
	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn(b.tr("К выбору цвета", "Back to Menu"), "🔙", emojis), "bot:avatar_menu"),
		),
	)
	b.editMessage(q.Message.Chat.ID, q.Message.MessageID, text, &markup)
}

// --- Markup helpers ---

func (b *Bot) cancelMarkup() *tgbotapi.InlineKeyboardMarkup {
	_, emojis := b.botPrefs()
	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn(b.tr("Отмена", "Cancel"), "❌", emojis), "state:cancel"),
		),
	)
	return &markup
}

// backMenuMarkup is the standard "← Main Menu" footer row.
func (b *Bot) backMenuMarkup() *tgbotapi.InlineKeyboardMarkup {
	_, emojis := b.botPrefs()
	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn(b.tr("Главное меню", "Main Menu"), "⬅️", emojis), "menu:main"),
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
			"<b>👥 "+b.tr("Добавление пользователя", "Add User")+"</b>\n\n"+b.tr("Отправьте имя пользователя и пароль через пробел (например, <code>john secret123</code>).\n\nСообщение будет удалено после обработки.", "Send username and password separated by space (e.g., <code>john secret123</code>).\n\nThe message will be deleted after processing."),
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
			"<b>🛡️ "+b.tr("Добавление роли", "Add Role")+"</b>\n\n"+b.tr("Отправьте имя роли (например, <code>manager</code>):", "Send the role name (e.g., <code>manager</code>):"),
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
			fmt.Sprintf("<b>✏️ %s</b>\n\n%s <b>%s</b>:",
				b.tr("Переименование", "Rename"),
				b.tr("Отправьте новое имя для узла", "Send the new name for node"),
				b.nodeLabel(nodeID)),
			b.cancelMarkup())
	case strings.HasPrefix(data, "node:sub:"):
		nodeID := strings.TrimPrefix(data, "node:sub:")
		b.setState(chatID, &userState{Step: "set_sub", NodeID: nodeID})
		b.editMessage(chatID, messageID,
			fmt.Sprintf("<b>🔗 %s</b>\n\n%s <b>%s</b>:",
				b.tr("Установить Sub URL", "Set Sub URL"),
				b.tr("Отправьте новый URL подписки для узла", "Send the new subscription URL for node"),
				b.nodeLabel(nodeID)),
			b.cancelMarkup())
	case strings.HasPrefix(data, "node:set_sub:"):
		// Onboarding quick-action: prompt for the subscription URL inside the
		// notification message itself.
		nodeID := strings.TrimPrefix(data, "node:set_sub:")
		b.setState(chatID, &userState{Step: "set_sub_url", NodeID: nodeID})
		b.editMessage(chatID, messageID,
			fmt.Sprintf("<b>🔗 %s</b>\n\n%s <b>%s</b>:",
				b.tr("Установить Sub URL", "Set Sub URL"),
				b.tr("Отправьте URL подписки для", "Please reply/send the Subscription URL for"),
				b.nodeLabel(nodeID)),
			b.cancelMarkup())
	case strings.HasPrefix(data, "node:approve:"):
		// Onboarding quick-action: the agent already reported a sub URL, so
		// approving just queues the update_sub fetch.
		b.handleApproveNode(chatID, messageID, strings.TrimPrefix(data, "node:approve:"))
	case strings.HasPrefix(data, "node:reject:"):
		b.handleRejectNode(chatID, messageID, strings.TrimPrefix(data, "node:reject:"))
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
			fmt.Sprintf("<b>💥 %s</b>\n\n%s <b>%s</b> %s\n\n%s <code>TERMINATE</code> %s:",
				b.tr("Завершение и самоуничтожение", "Terminate &amp; Self-Destruct"),
				b.tr("Это уничтожит узел", "This will destroy node"),
				b.nodeLabel(nodeID),
				b.tr("при следующем опросе.", "on its next poll."),
				b.tr("Введите", "Type"),
				b.tr("для подтверждения", "to confirm")),
			b.cancelMarkup())
	}
}

// --- Text helpers ---

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

package bot

import (
	"encoding/json"
	"fmt"
	"log"
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

// onlineWindow is how recent a LastSeen timestamp must be for a node to count
// as online (mirrors the web dashboard).
const onlineWindow = 90 * time.Second

// userState is the in-memory text-input session for the admin. The bot shows
// exactly ONE dynamic bot message; when a feature needs free-form text input
// (rename, sub URL, TERMINATE confirmation, mass sub URL) the conversation
// moves into a state machine that consumes the next text message.
type userState struct {
	Step   string // "", "rename_node", "set_sub", "mass_sub", "terminate_confirm"
	NodeID string
	Note   string
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

	b.backupTicker = time.NewTicker(24 * time.Hour)
	go b.runBackupScheduler()

	updates := make(chan tgbotapi.Update, 100)
	go b.fetchUpdates(updates)
	go b.pollUpdates(updates)

	log.Println("Telegram bot started")

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
					b.showMainMenu(adminChatID)
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

	if b.backupTicker != nil {
		b.backupTicker.Stop()
		b.backupTicker = nil
	}
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
			b.sendBackupDocument(b.chatID, "⏰ Scheduled daily backup")
		case <-stopCh:
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

// getMainMenuContent builds the main menu: node online/offline counters and
// the five fleet action buttons.
func (b *Bot) getMainMenuContent() (string, tgbotapi.InlineKeyboardMarkup) {
	nodes, _ := b.repo.GetAllNodes()
	onlineCount := 0
	for _, n := range nodes {
		if time.Since(n.LastSeen) < onlineWindow {
			onlineCount++
		}
	}
	offlineCount := len(nodes) - onlineCount

	text := fmt.Sprintf("<b>🌐 Malaxis Fleet v2.1-BETA</b>\n\n"+
		"Nodes: 🟢 %d Online | 🔴 %d Offline", onlineCount, offlineCount)

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💻 Manage Nodes", "nodes:list"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🚀 Push Client Files (OTA)", "ota:all"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 Mass Update Subscriptions", "task:mass_sub"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🧹 Purge Offline (>3d)", "purge:go"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📦 Download DB Backup", "backup:download"),
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
	case "set_sub":
		b.processSetSubText(chatID, text)
	case "mass_sub":
		b.processMassSubText(chatID, text)
	case "terminate_confirm":
		b.processTerminateText(chatID, text)
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
	b.showNodeDetail(chatID, b.getMainMenuID(chatID), state.NodeID, "✅ Subscription URL updated; node will refresh on next poll")
}

func (b *Bot) processMassSubText(chatID int64, text string) {
	b.clearState(chatID)

	subURL := strings.TrimSpace(text)
	if subURL == "" {
		b.editMessage(chatID, b.getMainMenuID(chatID), "<b>🔄 Mass Update Subscriptions</b>\n\nURL cannot be empty. Send a URL or press Cancel.", b.cancelMarkup())
		return
	}

	if err := b.repo.UpdateAllNodesSubURL(subURL); err != nil {
		log.Printf("Bot: mass sub update failed: %v", err)
		b.editMessage(chatID, b.getMainMenuID(chatID), "<b>❌ Failed to mass update subscription URLs.</b>", b.cancelMarkup())
		return
	}

	nodes, err := b.repo.GetAllNodes()
	if err != nil {
		log.Printf("Bot: failed to list nodes for mass sub: %v", err)
		b.editMessage(chatID, b.getMainMenuID(chatID), "<b>❌ Failed to queue commands.</b>", b.cancelMarkup())
		return
	}

	messageID := time.Now().Unix()
	command, _ := json.Marshal(map[string]string{"action": "update_sub", "sub_url": subURL})
	queuedCount := 0
	for _, node := range nodes {
		if err := b.repo.SetPendingCommand(node.ID, string(command), messageID); err != nil {
			log.Printf("Bot: failed to queue command for node %s: %v", node.ID, err)
		} else {
			queuedCount++
		}
	}

	b.audit.Log("telegram_bot", audit.ActionUpdateSettings, "all_nodes", "Mass updated subscription URL for all nodes (via Telegram bot)")
	text2, markup := b.getMainMenuContent()
	b.editMessage(chatID, b.getMainMenuID(chatID),
		fmt.Sprintf("✅ Subscription URL updated for <b>%d</b> nodes, commands queued for <b>%d</b>.\n\n%s", len(nodes), queuedCount, text2),
		&markup)
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

func (b *Bot) cancelMarkup() *tgbotapi.InlineKeyboardMarkup {
	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", "state:cancel"),
		),
	)
	return &markup
}

// --- Callback handling ---

func (b *Bot) handleCallbackQuery(q *tgbotapi.CallbackQuery) {
	callback := tgbotapi.NewCallback(q.ID, "")
	b.api.Request(callback)

	chatID := q.Message.Chat.ID
	messageID := q.Message.MessageID

	data := q.Data
	switch {
	case data == "state:cancel":
		b.clearState(chatID)
		b.showMainMenu(chatID)
	case data == "menu:main" || data == "start":
		b.showMainMenu(chatID)
	case data == "nodes:list":
		b.handleNodeList(chatID, messageID)
	case data == "ota:all":
		b.handleOtaAll(chatID, messageID)
	case data == "task:mass_sub":
		b.setState(chatID, &userState{Step: "mass_sub"})
		b.editMessage(chatID, messageID,
			"<b>🔄 Mass Update Subscriptions</b>\n\nSend the new subscription URL that will be applied to <b>ALL</b> nodes:",
			b.cancelMarkup())
	case data == "purge:go":
		b.handlePurge(chatID, messageID)
	case data == "backup:download":
		b.handleBackup(chatID, messageID)
	case strings.HasPrefix(data, "node:detail:"):
		b.showNodeDetail(chatID, messageID, strings.TrimPrefix(data, "node:detail:"), "")
	case strings.HasPrefix(data, "node:vpn:"):
		b.handleVpnMenu(chatID, messageID, strings.TrimPrefix(data, "node:vpn:"))
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

	icon := "🔴"
	if time.Since(node.LastSeen) < onlineWindow {
		icon = "🟢"
	}

	lastSeen := "never"
	if !node.LastSeen.IsZero() {
		d := time.Since(node.LastSeen)
		if d < time.Minute {
			lastSeen = "just now"
		} else if d < time.Hour {
			lastSeen = fmt.Sprintf("%d min ago", int(d.Minutes()))
		} else if d < 24*time.Hour {
			lastSeen = fmt.Sprintf("%d h ago", int(d.Hours()))
		} else {
			lastSeen = fmt.Sprintf("%d d ago", int(d.Hours()/24))
		}
	}

	name := node.Name
	if name == "" {
		name = node.ID
	}

	var text strings.Builder
	text.WriteString(fmt.Sprintf("%s <b>%s</b>\n\n", icon, name))
	text.WriteString(fmt.Sprintf("🖥️ <b>IP:</b> %s\n", emptyDash(node.IPLan)))
	text.WriteString(fmt.Sprintf("🏷️ <b>Hostname:</b> %s\n", emptyDash(node.Hostname)))
	text.WriteString(fmt.Sprintf("🌐 <b>Active Server:</b> %s\n", emptyDash(node.ActiveServer)))
	text.WriteString(fmt.Sprintf("⚙️ <b>Engine:</b> %s\n", emptyDash(node.ActiveEngine)))
	text.WriteString(fmt.Sprintf("🔗 <b>Sub URL:</b> %s\n", emptyDash(node.SubURL)))
	text.WriteString(fmt.Sprintf("🕒 <b>Last Seen:</b> %s\n", lastSeen))
	if node.PendingCommand != "" {
		text.WriteString(fmt.Sprintf("📥 <b>Queue:</b> <code>%s</code>\n", node.PendingCommand))
	} else {
		text.WriteString("📥 <b>Queue:</b> empty\n")
	}
	if node.PipelineStatus != "" {
		text.WriteString(fmt.Sprintf("🔄 <b>Status:</b> %s\n", emptyDash(node.PipelineStatus)))
	}
	if note != "" {
		text.WriteString("\n<i>" + note + "</i>")
	}

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🛡️ Switch VPN", "node:vpn:"+node.ID),
			tgbotapi.NewInlineKeyboardButtonData("🔗 Set Sub URL", "node:sub:"+node.ID),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✏️ Rename", "node:rename:"+node.ID),
			tgbotapi.NewInlineKeyboardButtonData("🗑️ Delete Node", "node:delete:"+node.ID),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 Refresh", "node:detail:"+node.ID),
			tgbotapi.NewInlineKeyboardButtonData("🔙 Back", "nodes:list"),
		),
	)
	b.editMessage(chatID, messageID, text.String(), &markup)
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
	b.audit.Log("telegram_bot", audit.ActionDeleteDevice, nodeID, "Deleted node "+name+" (via Telegram bot)")
	b.handleNodeList(chatID, messageID)
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

// emptyDash returns a placeholder for empty strings.
func emptyDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

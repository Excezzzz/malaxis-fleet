package bot

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"malaxis-fleet/internal/audit"
	"malaxis-fleet/internal/repository"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// --- Public API: node onboarding (BotManager interface) ---

// NotifyNewNode sends an instant onboarding notification to the admin the moment a brand-new device registers for the first time. When the agent already reported subscription URL(s) on its first poll, the message carries an "Approve & Fetch Config" quick action; otherwise it offers to set the sub URLs manually. Both variants include the reject (queues a terminate).
func (b *Bot) NotifyNewNode(id, name, ipLan string, subURLs []string) {
	b.mu.Lock()
	api := b.api
	chatID := b.chatID
	b.mu.Unlock()

	if api == nil {
		return
	}

	name = emptyDash(name)
	ipLan = emptyDash(ipLan)

	hasSub := false
	for _, u := range subURLs {
		if strings.TrimSpace(u) != "" {
			hasSub = true
			break
		}
	}

	_, emojis := b.botPrefs()
	text := fmt.Sprintf("<b>🖥️ %s</b>\n\n"+
		"<b>%s</b> %s\n"+
		"<b>%s</b> %s\n"+
		"<b>%s</b> <code>%s</code>\n\n"+
		"%s <b>%s</b>",
		b.tr("НОВОЕ УСТРОЙСТВО ПОДКЛЮЧЕНО!", "NEW DEVICE CONNECTED!"),
		b.tr("Имя:", "Name:"), name,
		b.tr("IP в LAN:", "LAN IP:"), ipLan,
		b.tr("ID узла:", "Node ID:"), id,
		b.tr("Статус:", "Status:"),
		b.tr("Зарегистрировано и ожидает настройки.", "Registered &amp; Waiting for Configuration."))
	if hasSub {
		text += "\n\n<b>Sub URL(s):</b>\n"
		for _, u := range subURLs {
			if strings.TrimSpace(u) != "" {
				text += "<code>" + xmlEscape(u) + "</code>\n"
			}
		}
		text += b.tr("Устройство зарегистрировано с указанными Sub URL.", "Device registered with provided Sub URL(s).")
	}

	var markup tgbotapi.InlineKeyboardMarkup
	if hasSub {
		markup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn(b.tr("Принять и получить конфиг", "Approve & Fetch Config"), "✅", emojis), "node:approve:"+id),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn(b.tr("Установить другой Sub URL", "Set Different Sub URL"), "🔗", emojis), "node:set_sub:"+id),
				tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn(b.tr("Отклонить и удалить", "Reject & Delete"), "❌", emojis), "node:reject:"+id),
			),
		)
	} else {
		markup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn(b.tr("Установить Sub URL", "Set Sub URL"), "🔗", emojis), "node:set_sub:"+id),
				tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn(b.tr("Отклонить и удалить", "Reject & Delete"), "❌", emojis), "node:reject:"+id),
			),
		)
	}

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.ReplyMarkup = &markup
	if _, err := api.Send(msg); err != nil {
		log.Printf("Bot: failed to send new-node onboarding notification: %v", err)
	}
}

// handleApproveNode is the onboarding "Approve & Fetch Config" action: the agent already reported subscription URL(s) on its first poll, so we simply queue an update_sub command against them. Mirrors the tail of the manual set-sub-URL flow.
func (b *Bot) handleApproveNode(chatID int64, messageID int, nodeID string) {
	node, err := b.repo.GetNodeByID(nodeID)
	if err != nil {
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Узел не найден.", "Node not found.")+"</b>", b.cancelMarkup())
		return
	}
	if len(node.SubURLs) == 0 {
		b.editMessage(chatID, messageID,
			"<b>⚠️ "+b.tr("У узла нет Sub URL.", "Node has no Sub URL.")+"</b> "+b.tr("Установите их вручную:", "Set them manually:"),
			b.cancelMarkup())
		return
	}

	command := map[string]interface{}{"action": "update_sub", "sub_urls": node.SubURLs}
	command["sub_url"] = node.SubURLs[0]
	cmdJSON, _ := json.Marshal(command)
	messageIDTs := time.Now().Unix()
	if err := b.repo.SetPendingCommand(nodeID, string(cmdJSON), messageIDTs); err != nil {
		log.Printf("Bot: failed to queue update_sub for node %s: %v", nodeID, err)
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Не удалось поставить команду обновления подписки.", "Failed to queue subscription update.")+"</b>", b.cancelMarkup())
		return
	}
	b.repo.UpdateNodePipelineStatus(nodeID, "Queued", "update_sub")

	b.audit.Log("telegram_bot", audit.ActionUpdateDevice, nodeID, "Approved node: queued update_sub (via Telegram bot)")
	b.showNodeDetail(chatID, messageID, nodeID, "✅ "+b.tr("Узел одобрен. Подписка будет загружена при следующем опросе.", "Node approved. The subscription will be fetched on its next poll."))
}

// nodeLabel resolves a node's display name (falls back to the id).
func (b *Bot) nodeLabel(nodeID string) string {
	if node, err := b.repo.GetNodeByID(nodeID); err == nil && node.Name != "" {
		return node.Name
	}
	return nodeID
}

// --- Node menus ---

// buildNodeListContent renders the node list menu text and markup.
func (b *Bot) buildNodeListContent() (string, tgbotapi.InlineKeyboardMarkup) {
	nodes, err := b.repo.GetAllNodes()
	if err != nil {
		log.Printf("Bot: failed to list nodes: %v", err)
		return "<b>❌ " + b.tr("Не удалось получить список узлов.", "Failed to list nodes.") + "</b>", tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(b.btn("🔙", "Назад", "Back"), "menu:main"),
			),
		)
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	var text strings.Builder
	_, emojis := b.botPrefs()
	text.WriteString("<b>💻 " + b.tr("Управление узлами", "Manage Nodes") + "</b>\n\n")
	if len(nodes) == 0 {
		text.WriteString(b.tr("Узлы ещё не зарегистрированы.", "No nodes registered yet."))
	} else {
		text.WriteString(fmt.Sprintf(b.tr("%d узлов зарегистрировано.\n\nНажмите на узел для деталей:", "%d node(s) registered.\n\nTap a node for details:"), len(nodes)))
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
			tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn(name, icon, emojis), "node:detail:"+n.ID),
		))
	}

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(b.btn("🔙", "Назад", "Back"), "menu:main"),
	))
	return text.String(), tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func (b *Bot) handleNodeList(chatID int64, messageID int) {
	text, markup := b.buildNodeListContent()
	b.editMessage(chatID, messageID, text, &markup)
}

// showNodeListFresh sends a brand new node list menu (used by the /nodes slash command): the old menu message is deleted and a fresh one is placed at the bottom of the chat.
func (b *Bot) showNodeListFresh(chatID int64) {
	text, markup := b.buildNodeListContent()
	b.sendFreshMenu(chatID, text, &markup)
}

func (b *Bot) showNodeDetail(chatID int64, messageID int, nodeID, note string) {
	node, err := b.repo.GetNodeByID(nodeID)
	if err != nil {
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Узел не найден.", "Node not found.")+"</b>", nil)
		b.handleNodeList(chatID, messageID)
		return
	}

	name := emptyDash(node.Name)
	if node.Name == "" {
		name = node.ID
	}

	pendingTask := b.tr("Нет", "None")
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
	text.WriteString(fmt.Sprintf("🖥️ <b>%s</b> %s\n", b.tr("Узел:", "Node:"), xmlEscape(name)))
	text.WriteString(fmt.Sprintf("<b>IP:</b> %s | <b>%s</b> %s\n", emptyDash(node.IPLan), b.tr("Хост:", "Host:"), emptyDash(node.Hostname)))
	text.WriteString(fmt.Sprintf("<b>VPN:</b> %s\n", xmlEscape(vpn)))
	if len(node.SubURLs) > 0 {
		text.WriteString(fmt.Sprintf("<b>%s:</b> %d\n", b.tr("Sub URL(s)", "Sub URL(s)"), len(node.SubURLs)))
		for _, u := range node.SubURLs {
			text.WriteString("<code>" + xmlEscape(u) + "</code>\n")
		}
	} else {
		text.WriteString(fmt.Sprintf("<b>Sub URL:</b> %s\n", emptyDash("")))
	}
	text.WriteString(fmt.Sprintf("<b>%s</b> %s\n", b.tr("Статус:", "Status:"), emptyDash(node.StatusMessage)))
	text.WriteString(fmt.Sprintf("<b>%s</b> %s\n", b.tr("Ожидаемая задача:", "Pending Task:"), pendingTask))
	if note != "" {
		text.WriteString("\n<i>" + note + "</i>")
	}

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.btn("🛡️", "Переключить VPN", "Switch VPN"), "node:vpn:"+node.ID),
			tgbotapi.NewInlineKeyboardButtonData(b.btn("🔗", "Установить Sub URL", "Set Sub URL"), "node:sub:"+node.ID),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.btn("🔄", "Обновить подписки", "Refresh Subs"), "node:refresh_sub:"+node.ID),
			tgbotapi.NewInlineKeyboardButtonData(b.btn("📜", "Просмотр логов", "View Logs"), "node:logs:"+node.ID),
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf(b.btn("⏳", "Очередь задач (%d)", "Task Queue (%d)"), taskCount), "node:queue:"+node.ID),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.btn("✏️", "Переименовать узел", "Rename Node"), "node:rename:"+node.ID),
			tgbotapi.NewInlineKeyboardButtonData(b.btn("🗑️", "Удалить узел", "Delete Node"), "node:delete:"+node.ID),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.btn("🔙", "К списку узлов", "Back to Nodes List"), "nodes:list"),
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
			tgbotapi.NewInlineKeyboardButtonData(b.btn("🔙", "К узлу", "Back to Node"), "node:detail:"+nodeID),
		),
	)
	text := fmt.Sprintf("📜 <b>%s</b> %s\n\n%s:", b.tr("Логи:", "Logs:"), xmlEscape(b.nodeLabel(nodeID)), b.tr("Выберите контейнер", "Select a container"))
	b.editMessage(chatID, messageID, text, &markup)
}

// allowedLogContainers are the node containers selectable in the log viewer.
var allowedLogContainers = map[string]bool{"node-agent": true, "xray-node": true, "singbox-node": true}

// showContainerLogs displays the stored log tail for a node container. It queues a fresh get_logs command (only when nothing else is pending) so the agent uploads the newest tail on its next poll, then renders the most recent stored output. Mirrors the web dashboard's log viewer.
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

	logs := b.tr("Логов для этого контейнера пока нет. Узел загрузит их при следующем опросе.", "No logs stored for this container yet. The node will upload them on its next poll.")
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

	text := fmt.Sprintf("📜 <b>%s</b> %s -> <b>%s</b>\n\n<code>%s</code>",
		b.tr("Логи:", "Logs:"), xmlEscape(b.nodeLabel(nodeID)), xmlEscape(container), xmlEscape(logs))

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.btn("🔄", "Обновить логи", "Refresh Logs"), "node:logs_refresh:"+nodeID+":"+container),
			tgbotapi.NewInlineKeyboardButtonData(b.btn("🔙", "К узлу", "Back to Node"), "node:detail:"+nodeID),
		),
	)
	b.editMessage(chatID, messageID, text, &markup)
}

// handleQueueMenu shows the node's pending task with a cancel action, or the "no pending tasks" state when the queue is empty.
func (b *Bot) handleQueueMenu(chatID int64, messageID int, nodeID string) {
	pending, err := b.repo.GetPendingCommand(nodeID)
	if err != nil {
		log.Printf("Bot: failed to read pending command for node %s: %v", nodeID, err)
		b.showNodeDetail(chatID, messageID, nodeID, "❌ "+b.tr("Не удалось прочитать очередь задач", "Failed to read task queue"))
		return
	}

	if strings.TrimSpace(pending) == "" {
		markup := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(b.btn("🔙", "К узлу", "Back to Node"), "node:detail:"+nodeID),
			),
		)
		text := fmt.Sprintf("⏳ <b>%s</b>\n\n%s", xmlEscape(b.nodeLabel(nodeID)), b.tr("Нет ожидающих задач для этого узла.", "No pending tasks for this node."))
		b.editMessage(chatID, messageID, text, &markup)
		return
	}

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.btn("❌", "Отменить ожидаемую задачу", "Cancel Pending Task"), "node:queue_cancel:"+nodeID),
			tgbotapi.NewInlineKeyboardButtonData(b.btn("🔙", "К узлу", "Back to Node"), "node:detail:"+nodeID),
		),
	)
	text := fmt.Sprintf("⏳ <b>%s %s:</b>\n\n<code>%s</code>",
		b.tr("Ожидаемая задача для", "Pending Task for"), xmlEscape(b.nodeLabel(nodeID)), xmlEscape(pending))
	b.editMessage(chatID, messageID, text, &markup)
}

// handleQueueCancel clears the node's pending command (executes clear-command in PostgreSQL) and confirms the result on the message.
func (b *Bot) handleQueueCancel(chatID int64, messageID int, nodeID string) {
	if err := b.repo.ClearPendingCommand(nodeID); err != nil {
		log.Printf("Bot: failed to clear pending command for node %s: %v", nodeID, err)
		b.showNodeDetail(chatID, messageID, nodeID, "❌ "+b.tr("Не удалось отменить ожидаемую задачу", "Failed to cancel pending task"))
		return
	}
	b.repo.UpdateNodePipelineStatus(nodeID, "Idle", "Command cancelled")
	b.audit.Log("telegram_bot", audit.ActionUpdateSettings, nodeID, "Cancelled pending task (via Telegram bot)")

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.btn("🔙", "К узлу", "Back to Node"), "node:detail:"+nodeID),
		),
	)
	b.editMessage(chatID, messageID, "✅ <b>"+b.tr("Ожидаемая задача отменена.", "Pending task cancelled.")+"</b>", &markup)
}

// handleRefreshSub queues an update_sub command against the node's current (master-synced) subscription URLs, forcing the agent to re-download and re-parse its subscription on the next poll.
func (b *Bot) handleRefreshSub(chatID int64, messageID int, nodeID string) {
	command, _ := json.Marshal(map[string]interface{}{"action": "update_sub"})
	messageIDTs := time.Now().Unix()
	if err := b.repo.SetPendingCommand(nodeID, string(command), messageIDTs); err != nil {
		log.Printf("Bot: failed to queue update_sub for node %s: %v", nodeID, err)
		b.showNodeDetail(chatID, messageID, nodeID, "❌ "+b.tr("Не удалось поставить обновление подписок в очередь.", "Failed to queue the subscription refresh."))
		return
	}
	b.repo.UpdateNodePipelineStatus(nodeID, "Queued", "update_sub")
	b.audit.Log("telegram_bot", audit.ActionUpdateSettings, nodeID, "Queued update_sub (via Telegram bot)")
	b.showNodeDetail(chatID, messageID, nodeID, "✅ "+b.tr("Обновление подписок поставлено в очередь.", "Subscription refresh queued."))
}

// sortedKeys returns the keys of m in a stable (sorted) order so VPN menus render provider groups deterministically.
func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (b *Bot) handleVpnMenu(chatID int64, messageID int, nodeID string) {
	node, err := b.repo.GetNodeByID(nodeID)
	if err != nil {
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Узел не найден.", "Node not found.")+"</b>", nil)
		return
	}

	var text strings.Builder
	text.WriteString(fmt.Sprintf("<b>🛡️ %s</b>\n\n%s <b>%s</b>:\n\n",
		b.tr("Переключить VPN", "Switch VPN"),
		b.tr("Выберите целевой сервер для", "Select the target server for"),
		b.nodeLabel(nodeID)))
	if node.ActiveServer != "" {
		text.WriteString(fmt.Sprintf("%s: <b>%s</b>", b.tr("Сейчас активен", "Currently active"), node.ActiveServer))
	} else {
		text.WriteString(b.tr("Сейчас активен: <i>нет</i>", "Currently active: <i>none</i>"))
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	_, emojis := b.botPrefs()
	rows = append(rows,
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn(b.tr("Быстрый", "Fastest"), "⚡", emojis), "node:switch:"+nodeID+":fastest"),
			tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn(b.tr("Баланс", "Balanced"), "⚖️", emojis), "node:switch:"+nodeID+":balanced"),
		),
	)
	// v1.2.2: the agent reports available_servers as an object grouped by provider {provider: [server, ...]}, so the menu renders one non-clickable separator row "[ ➖ Provider Name ➖ ]" before each group (callback data "ignore" — silently acked, never matched). Provider-less legacy entries render without a separator.
	for _, provider := range sortedKeys(node.AvailableServers) {
		servers := node.AvailableServers[provider]
		if provider != "" {
			sep := "[ " + provider + " ]"
			if emojis {
				sep = "[ ➖ " + provider + " ➖ ]"
			}
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(sep, "ignore"),
			))
		}
		for _, srv := range servers {
			srv = strings.TrimSpace(srv)
			if srv == "" {
				continue
			}
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn(srv, "🌐", emojis), "node:switch:"+nodeID+":"+srv),
			))
		}
	}
	if len(node.AvailableServers) == 0 {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.fmtBtn(b.tr("Серверы не загружены", "No Servers Loaded"), "❌", emojis), "ignore"),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(b.btn("🔙", "Назад", "Back"), "node:detail:"+nodeID),
	))

	markup := tgbotapi.NewInlineKeyboardMarkup(rows...)
	b.editMessage(chatID, messageID, text.String(), &markup)
}

// --- Node text flows ---

func (b *Bot) processRenameText(chatID int64, text string) {
	state := b.getState(chatID)
	b.clearState(chatID)

	newName := strings.TrimSpace(text)
	if newName == "" {
		b.editMessage(chatID, b.getMainMenuID(chatID),
			"<b>✏️ "+b.tr("Переименование", "Rename")+"</b>\n\n"+b.tr("Имя не может быть пустым. Отправьте имя или нажмите «Отмена».", "Name cannot be empty. Send a name or press Cancel."),
			b.cancelMarkup())
		return
	}

	if err := b.repo.RenameNode(state.NodeID, newName); err != nil {
		log.Printf("Bot: failed to rename node %s: %v", state.NodeID, err)
		b.editMessage(chatID, b.getMainMenuID(chatID), "<b>❌ "+b.tr("Не удалось переименовать узел.", "Failed to rename node.")+"</b>", b.cancelMarkup())
		return
	}

	b.audit.Log("telegram_bot", audit.ActionUpdateDevice, state.NodeID, "Renamed node to "+newName+" (via Telegram bot)")
	b.showNodeDetail(chatID, b.getMainMenuID(chatID), state.NodeID, "✅ "+b.tr("Переименован в", "Renamed to")+" "+newName)
}

func (b *Bot) processSetSubText(chatID int64, text string) {
	state := b.getState(chatID)
	b.clearState(chatID)

	// v1.2.0: accept one or MORE subscription URLs, separated by spaces, newlines or commas.
	rawParts := strings.FieldsFunc(text, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == ','
	})
	subURLs := []string{}
	seen := map[string]bool{}
	for _, part := range rawParts {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		subURLs = append(subURLs, part)
	}
	if len(subURLs) == 0 {
		b.editMessage(chatID, b.getMainMenuID(chatID),
			"<b>🔗 "+b.tr("Установить Sub URL", "Set Sub URL")+"</b>\n\n"+b.tr("URL не может быть пустым. Отправьте URL или нажмите «Отмена».", "URL cannot be empty. Send a URL or press Cancel."),
			b.cancelMarkup())
		return
	}

	for _, subURL := range subURLs {
		if !subURLReachable(subURL) {
			b.editMessage(chatID, b.getMainMenuID(chatID),
				"<b>❌ "+b.tr("Неверный URL подписки!", "Invalid Subscription URL!")+"</b>\n\n"+b.tr("Не удалось подключиться к ссылке или сервер вернул ошибку. Проверьте, что адрес подписки работает, и попробуйте снова.", "Could not connect to the link or the server returned an error. Check that the subscription address is working and try again."),
				b.cancelMarkup())
			return
		}
	}

	if _, err := b.repo.GetNodeByID(state.NodeID); err != nil {
		log.Printf("Bot: node %s not found for sub update: %v", state.NodeID, err)
		b.showNodeDetail(chatID, b.getMainMenuID(chatID), state.NodeID, "❌ "+b.tr("Узел не найден", "Node not found"))
		return
	}

	command := map[string]interface{}{"action": "update_sub", "sub_urls": subURLs}
	command["sub_url"] = subURLs[0]
	cmdJSON, _ := json.Marshal(command)
	messageID := time.Now().Unix()
	// Single atomic UPDATE: sub_urls and the queued update_sub command are committed together, so the agent is always triggered to fetch servers.
	if err := b.repo.UpdateNodeSubURLsAndQueue(state.NodeID, subURLs, string(cmdJSON), messageID); err != nil {
		log.Printf("Bot: failed to update sub_urls and queue update_sub for node %s: %v", state.NodeID, err)
		b.editMessage(chatID, b.getMainMenuID(chatID), "<b>❌ "+b.tr("Не удалось обновить URL подписки.", "Failed to update subscription URLs.")+"</b>", b.cancelMarkup())
		return
	}

	b.audit.Log("telegram_bot", audit.ActionUpdateDevice, state.NodeID, "Updated subscription URLs to "+strings.Join(subURLs, ", ")+" (via Telegram bot)")

	// Onboarding-style success: keep the message compact and offer a path back to the full node detail view.
	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.btn("💻", "Детали узла", "View Node Details"), "node:detail:"+state.NodeID),
		),
	)
	b.editMessage(chatID, b.getMainMenuID(chatID),
		fmt.Sprintf("<b>✅ %s</b>\n\n%s <b>%s</b> %s",
			b.tr("URL подписки установлен!", "Subscription URL Set!"),
			b.tr("Устройство", "Device"),
			b.nodeLabel(state.NodeID),
			b.tr("теперь загружает конфигурации...", "is now fetching configs...")),
		&markup)
}

// subURLReachable checks that a subscription URL is well-formed (http/https, no embedded credentials) and actually reachable (5s timeout, HTTP < 400) before the bot saves it.
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
			"<b>💥 "+b.tr("Завершение и самоуничтожение", "Terminate & Self-Destruct")+"</b>\n\n<code>TERMINATE</code> "+b.tr("не получено. Введите точное слово", "not received. Type the exact word")+" <code>TERMINATE</code> "+b.tr("для подтверждения (или нажмите «Отмена»).", "to confirm (or press Cancel)."),
			b.cancelMarkup())
		return
	}

	b.clearState(chatID)

	command, _ := json.Marshal(map[string]string{"action": "terminate"})
	messageID := time.Now().Unix()
	if err := b.repo.SetPendingCommand(state.NodeID, string(command), messageID); err != nil {
		log.Printf("Bot: failed to queue terminate for node %s: %v", state.NodeID, err)
		b.showNodeDetail(chatID, b.getMainMenuID(chatID), state.NodeID, "❌ "+b.tr("Не удалось поставить команду завершения", "Failed to queue terminate"))
		return
	}
	b.repo.UpdateNodePipelineStatus(state.NodeID, "Queued", "terminate")

	b.audit.Log("telegram_bot", audit.ActionDeleteDevice, state.NodeID, "Queued terminate (self-destruct) command (via Telegram bot)")
	b.showNodeDetail(chatID, b.getMainMenuID(chatID), state.NodeID, "💥 "+b.tr("Завершение поставлено в очередь. Узел самоуничтожится при следующем опросе.", "Terminate queued. The node will self-destruct on its next poll."))
}

// --- Node actions ---

func (b *Bot) handleDeleteMenu(chatID int64, messageID int, nodeID string) {
	text := fmt.Sprintf("<b>🗑️ %s</b>\n\n%s <b>%s</b>?\n\n"+
		"• <b>%s</b> %s\n"+
		"• <b>%s</b> %s",
		b.tr("Удалить узел", "Delete Node"),
		b.tr("Удалить", "Delete"),
		b.nodeLabel(nodeID),
		b.tr("Мягкое удаление", "Soft Delete"),
		b.tr("сразу убирает узел из флота.", "removes the node from the fleet immediately."),
		b.tr("Завершение и самоуничтожение", "Terminate &amp; Self-Destruct"),
		b.tr("ставит деструктивную команду в очередь; агент удалит свои контейнеры и выйдет при следующем опросе.", "queues the destructive command; the agent wipes its engine containers and exits on its next poll."))

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.btn("🗑️", "Мягкое удаление", "Soft Delete"), "node:softdelete:"+nodeID),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.btn("💥", "Завершение и самоуничтожение", "Terminate & Self-Destruct"), "node:terminate:"+nodeID),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.btn("↩️", "Отмена", "Cancel"), "node:detail:"+nodeID),
		),
	)
	b.editMessage(chatID, messageID, text, &markup)
}

func (b *Bot) handleSwitch(chatID int64, messageID int, nodeID, target string) {
	target = strings.TrimSpace(strings.ToLower(target))
	command, _ := json.Marshal(map[string]string{"action": "switch", "outbound_tag": target})
	cmdMessageID := time.Now().Unix()
	if err := b.repo.SetPendingCommand(nodeID, string(command), cmdMessageID); err != nil {
		log.Printf("Bot: failed to queue switch for node %s: %v", nodeID, err)
		if err == repository.ErrNodeNotFound {
			b.showNodeDetail(chatID, messageID, nodeID, "❌ "+b.tr("Узел не найден", "Node not found"))
			return
		}
		b.showNodeDetail(chatID, messageID, nodeID, "❌ "+b.tr("Не удалось поставить команду переключения", "Failed to queue switch command"))
		return
	}
	b.repo.UpdateNodePipelineStatus(nodeID, "Queued", "switch")

	b.audit.Log("telegram_bot", audit.ActionUpdateSettings, nodeID, "Switched VPN to "+target+" (via Telegram bot)")
	b.showNodeDetail(chatID, messageID, nodeID, "🛡️ "+b.tr("Переключение на", "Switch to")+" <b>"+target+"</b> "+b.tr("поставлено в очередь", "queued"))
}

// backToNodesMarkup is the standard "🔙 Back to Nodes List" footer row, used after a node was deleted/rejected so the admin returns to the node list (the deleted node can no longer render its detail view).
func (b *Bot) backToNodesMarkup() *tgbotapi.InlineKeyboardMarkup {
	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.btn("🔙", "К списку узлов", "Back to Nodes List"), "nodes:list"),
		),
	)
	return &markup
}

// handleRejectNode is the onboarding "Reject & Delete" action. Instead of only dropping the DB row, it queues a full TERMINATE command so the unwanted client actually stops its Docker containers and destroys its local config on its next poll. The DB row is left for the offline-cleanup cron to reap.
func (b *Bot) handleRejectNode(chatID int64, messageID int, nodeID string) {
	name := b.nodeLabel(nodeID)
	command, _ := json.Marshal(map[string]string{"action": "terminate"})
	cmdMessageID := time.Now().Unix()
	if err := b.repo.SetPendingCommand(nodeID, string(command), cmdMessageID); err != nil {
		log.Printf("Bot: failed to queue reject-terminate for node %s: %v", nodeID, err)
		if err == repository.ErrNodeNotFound {
			b.editMessage(chatID, messageID,
				"❌ "+b.tr("Узел не найден", "Node not found"), b.cancelMarkup())
			return
		}
		b.editMessage(chatID, messageID,
			"❌ "+b.tr("Не удалось поставить команду завершения", "Failed to queue terminate"), b.cancelMarkup())
		return
	}
	b.repo.UpdateNodePipelineStatus(nodeID, "Queued", "terminate")
	b.audit.Log("telegram_bot", audit.ActionDeleteDevice, nodeID, "Rejected node "+name+": queued terminate (self-destruct) command (via Telegram bot)")
	b.editMessage(chatID, messageID,
		"❌ "+b.tr("Устройство отклонено. Команда завершения отправлена. Клиент остановит и удалит свои контейнеры.", "Device rejected. Termination command sent. The client will stop and remove its containers."),
		b.backToNodesMarkup())
}

// handleSoftDelete is the node-menu "Soft Delete" action: it removes the DB row only, without touching the client (for nodes you simply want gone).
func (b *Bot) handleSoftDelete(chatID int64, messageID int, nodeID string) {
	name := b.nodeLabel(nodeID)
	if err := b.repo.DeleteNode(nodeID); err != nil {
		log.Printf("Bot: failed to delete node %s: %v", nodeID, err)
		b.showNodeDetail(chatID, messageID, nodeID, "❌ "+b.tr("Не удалось удалить узел", "Failed to delete node"))
		return
	}
	b.audit.Log("telegram_bot", audit.ActionDeleteDevice, nodeID, "Rejected and deleted node "+name+" (via Telegram bot)")
	b.editMessage(chatID, messageID,
		fmt.Sprintf("❌ %s <b>%s</b> %s", b.tr("Узел", "Node"), name, b.tr("отклонён и удалён.", "rejected and deleted.")),
		b.backToNodesMarkup())
}

// handleRefreshAllSubs mirrors the web UI "Refresh All Subscriptions" action: it queues an update_sub command for EVERY node (each agent re-fetches and re-applies its existing subscription URL). No URL input is requested.
func (b *Bot) handleRefreshAllSubs(chatID int64, messageID int) {
	nodes, err := b.repo.GetAllNodes()
	if err != nil {
		log.Printf("Bot: failed to list nodes for refresh subs: %v", err)
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Не удалось получить список узлов.", "Failed to list nodes.")+"</b>", b.cancelMarkup())
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
		fmt.Sprintf("🔄 "+b.tr("<b>%d</b> узлов поставлены в очередь на обновление подписок.", "<b>%d</b> node(s) queued to refresh their subscriptions.")+"\n\n%s", len(nodes), text),
		&markup)
}

func (b *Bot) handleOtaAll(chatID int64, messageID int) {
	baseURL := "https://" + b.cfg.SubDomain
	token := b.cfg.FleetSecret
	// Tokenized download URLs: every payload endpoint on the sub-domain requires ?t=<SECRET_TOKEN> (payloadTokenGuard serves a fake nginx 404 otherwise), so OTA silently failed before the token was added. The pkg_url field (agent_src.zip) is required by the agent to update its own Python package; the web handler (UpdateClientFilesHandler) sets the exact same fields.
	command, _ := json.Marshal(map[string]string{
		"action":         "update_client_files",
		"agent_url":      baseURL + "/node_agent.py?t=" + token,
		"pkg_url":        baseURL + "/agent_src.zip?t=" + token,
		"cli_url":        baseURL + "/fleet-cli.sh?t=" + token,
		"req_url":        baseURL + "/requirements.txt?t=" + token,
		"entrypoint_url": baseURL + "/entrypoint.sh?t=" + token,
	})

	nodes, err := b.repo.GetAllNodes()
	if err != nil {
		log.Printf("Bot: failed to list nodes for OTA: %v", err)
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Не удалось получить список узлов.", "Failed to list nodes.")+"</b>", b.cancelMarkup())
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
		fmt.Sprintf("🚀 "+b.tr("Файлы клиентов (OTA) поставлены в очередь для <b>%d</b> из <b>%d</b> узлов. Агенты обновятся при следующем опросе.", "Client files (OTA) queued for <b>%d</b> of <b>%d</b> nodes. Agents will self-update on their next poll.")+"\n\n%s",
			queuedCount, len(nodes), text),
		&markup)
}

func (b *Bot) handlePurge(chatID int64, messageID int) {
	deleted, err := b.repo.DeleteOfflineNodes(7)
	if err != nil {
		log.Printf("Bot: purge failed: %v", err)
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Очистка не удалась.", "Purge failed.")+"</b>", b.cancelMarkup())
		return
	}
	b.audit.Log("telegram_bot", audit.ActionDeleteDevice, "all_nodes", "Purged "+strconv.FormatInt(deleted, 10)+" offline nodes older than 7 days (via Telegram bot)")

	text, markup := b.getMainMenuContent()
	b.editMessage(chatID, messageID,
		fmt.Sprintf("🧹 "+b.tr("Очищено <b>%d</b> офлайн-узлов (старше 7 дней).", "Purged <b>%d</b> offline node(s) (older than 7 days).")+"\n\n%s", deleted, text),
		&markup)
}

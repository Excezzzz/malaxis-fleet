package bot

import (
	"fmt"
	"log"
	"net"
	"strings"

	"malaxis-fleet/internal/audit"
	"malaxis-fleet/internal/domain"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// validProviderDomain validates and normalizes a subscription provider domain: bare hostnames only, no scheme, path, query, userinfo, port or IP literal. Mirrors the web API's providerDomainFromRequest so the bot accepts exactly the same input space.
func validProviderDomain(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") || strings.ContainsAny(raw, "/?#@") {
		return ""
	}
	if net.ParseIP(raw) != nil {
		return ""
	}
	for _, c := range raw {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' || c == '-') {
			return ""
		}
	}
	return raw
}

// providerLabel resolves a provider's friendly name (falls back to the domain).
func (b *Bot) providerLabel(domain string) string {
	if names, err := b.repo.GetProviderNames(); err == nil {
		if name, ok := names[domain]; ok && name != "" {
			return name
		}
	}
	return domain
}

// buildProvidersMenuContent renders the provider list menu text and markup.
func (b *Bot) buildProvidersMenuContent() (string, tgbotapi.InlineKeyboardMarkup) {
	providers, err := b.repo.GetSubscriptionProviders()
	if err != nil {
		log.Printf("Bot: failed to list providers: %v", err)
		return "<b>❌ " + b.tr("Не удалось получить список провайдеров.", "Failed to list providers.") + "</b>", tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(b.btn("🔙", "Назад", "Back"), "menu:main"),
			),
		)
	}

	var text strings.Builder
	text.WriteString("<b>🌍 " + b.tr("Управление провайдерами", "Manage Providers") + "</b>\n\n")
	if len(providers) == 0 {
		text.WriteString(b.tr("Провайдеров пока нет. Они добавляются автоматически из URL подписок узлов.", "No providers yet. They are auto-discovered from node subscription URLs."))
	} else {
		text.WriteString(fmt.Sprintf(b.tr("%d провайдеров. Нажмите на провайдера для управления:\n", "%d provider(s). Tap a provider to manage:\n"), len(providers)))
	}

	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(providers)+3)
	_, emojis := b.botPrefs()
	for _, p := range providers {
		label := b.fmtBtn(xmlEscape(p.Name), "🌐", emojis)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, "provider:detail:"+p.Domain),
		))
	}
	rows = append(rows,
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.btn("➕", "Добавить провайдера", "Add Provider"), "providers:add"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.btn("🔙", "Назад", "Back"), "menu:main"),
		),
	)
	return text.String(), tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// handleProvidersMenu lists all subscription providers and offers an Add Provider button.
func (b *Bot) handleProvidersMenu(chatID int64, messageID int) {
	text, markup := b.buildProvidersMenuContent()
	b.editMessage(chatID, messageID, text, &markup)
}

// showProvidersMenuFresh sends a brand new providers list menu (used by the /providers slash command): the old menu message is deleted and a fresh one is placed at the bottom of the chat.
func (b *Bot) showProvidersMenuFresh(chatID int64) {
	text, markup := b.buildProvidersMenuContent()
	b.sendFreshMenu(chatID, text, &markup)
}

// showProviderDetail renders a single provider with its domain and friendly name plus the management actions: rename and delete.
func (b *Bot) showProviderDetail(chatID int64, messageID int, domain, note string) {
	names, err := b.repo.GetProviderNames()
	if err != nil {
		log.Printf("Bot: failed to load providers: %v", err)
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Не удалось загрузить провайдеров.", "Failed to load providers.")+"</b>", b.cancelMarkup())
		return
	}
	name, ok := names[domain]
	if !ok || name == "" {
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Провайдер не найден.", "Provider not found.")+"</b>", b.cancelMarkup())
		return
	}

	var text strings.Builder
	text.WriteString("<b>🌍 " + b.tr("Данные провайдера", "Provider Details") + "</b>\n\n")
	text.WriteString(fmt.Sprintf("<b>%s</b> <code>%s</code>\n", b.tr("Домен:", "Domain:"), xmlEscape(domain)))
	text.WriteString(fmt.Sprintf("<b>%s</b> %s", b.tr("Имя:", "Name:"), xmlEscape(name)))
	if note != "" {
		text.WriteString("\n\n✅ <i>" + note + "</i>")
	}

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.btn("✏️", "Переименовать", "Rename"), "provider:rename:"+domain),
			tgbotapi.NewInlineKeyboardButtonData(b.btn("🗑️", "Удалить", "Delete"), "provider:del:"+domain),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.btn("🔙", "К провайдерам", "Back to Providers"), "providers:menu"),
		),
	)
	b.editMessage(chatID, messageID, text.String(), &markup)
}

// handleProviderAddRequest opens the add-provider flow; the next text message becomes the provider domain.
func (b *Bot) handleProviderAddRequest(chatID int64, messageID int) {
	b.setState(chatID, &userState{Step: "add_provider_domain"})
	b.editMessage(chatID, messageID,
		"<b>🌍 "+b.tr("Добавление провайдера", "Add Provider")+"</b>\n\n"+b.tr("Отправьте домен провайдера (например, <code>sub.example.com</code>). Без протокола, пути и порта:", "Send the provider domain (e.g., <code>sub.example.com</code>). No scheme, path or port:"),
		b.cancelMarkup())
}

// processAddProviderDomainText validates the domain and moves the flow on to the friendly-name prompt.
func (b *Bot) processAddProviderDomainText(chatID int64, text string) {
	domain := validProviderDomain(text)
	if domain == "" {
		b.editMessage(chatID, b.getMainMenuID(chatID),
			"<b>🌍 "+b.tr("Добавление провайдера", "Add Provider")+"</b>\n\n"+b.tr("Некорректный домен. Отправьте имя хоста без протокола, пути и порта (например, <code>sub.example.com</code>):", "Invalid domain. Send a bare hostname without scheme, path or port (e.g., <code>sub.example.com</code>):"),
			b.cancelMarkup())
		return
	}

	if names, err := b.repo.GetProviderNames(); err == nil {
		if _, exists := names[domain]; exists {
			b.editMessage(chatID, b.getMainMenuID(chatID),
				"<b>❌ "+b.tr("Провайдер с таким доменом уже существует.", "Provider with this domain already exists.")+" "+b.tr("Отправьте другой домен или нажмите «Отмена»:", "Send a different domain or press Cancel:"),
				b.cancelMarkup())
			return
		}
	}

	b.setState(chatID, &userState{Step: "add_provider_name", Domain: domain})
	b.editMessage(chatID, b.getMainMenuID(chatID),
		fmt.Sprintf("<b>🌍 %s</b>\n\n%s <b>%s</b> %s\n\n%s:", b.tr("Добавление провайдера", "Add Provider"), b.tr("Домен", "Domain"), xmlEscape(domain), b.tr("принят.", "accepted."), b.tr("Отправьте отображаемое имя провайдера", "Send the provider display name")),
		b.cancelMarkup())
}

// processAddProviderNameText validates the friendly name and inserts the provider mapping.
func (b *Bot) processAddProviderNameText(chatID int64, text string) {
	state := b.getState(chatID)
	if state == nil || state.Step != "add_provider_name" || state.Domain == "" {
		b.clearState(chatID)
		b.editMessage(chatID, b.getMainMenuID(chatID), "<b>⚠️ "+b.tr("Процесс добавления провайдера истёк.", "Add-provider flow expired.")+"</b> "+b.tr("Начните заново из главного меню.", "Please start again from the main menu."), b.cancelMarkup())
		return
	}

	name := strings.TrimSpace(text)
	if name == "" {
		b.editMessage(chatID, b.getMainMenuID(chatID),
			"<b>🌍 "+b.tr("Добавление провайдера", "Add Provider")+"</b>\n\n"+b.tr("Имя не может быть пустым. Отправьте имя провайдера или нажмите «Отмена»:", "Name cannot be empty. Send the provider name or press Cancel:"),
			b.cancelMarkup())
		return
	}
	if len(name) > 64 {
		b.editMessage(chatID, b.getMainMenuID(chatID),
			"<b>🌍 "+b.tr("Добавление провайдера", "Add Provider")+"</b>\n\n"+b.tr("Имя слишком длинное (максимум 64 символа). Отправьте более короткое имя:", "Name is too long (max 64 characters). Send a shorter name:"),
			b.cancelMarkup())
		return
	}

	provider := domain.SubscriptionProvider{Domain: state.Domain, Name: name}
	if err := b.repo.UpsertSubscriptionProvider(provider); err != nil {
		log.Printf("Bot: failed to create provider %q: %v", state.Domain, err)
		b.editMessage(chatID, b.getMainMenuID(chatID), "<b>❌ "+b.tr("Не удалось создать провайдера.", "Failed to create provider.")+"</b>", b.cancelMarkup())
		return
	}

	b.audit.Log("telegram_bot", audit.ActionUpdateSettings, provider.Domain, "Created provider "+provider.Domain+" -> "+provider.Name+" (via Telegram bot)")
	b.clearState(chatID)
	b.showProviderDetail(chatID, b.getMainMenuID(chatID), state.Domain, b.tr("Провайдер создан", "Provider created"))
}

// handleProviderRenameRequest opens the rename prompt; the next text message becomes the provider's new friendly name.
func (b *Bot) handleProviderRenameRequest(chatID int64, messageID int, domain string) {
	names, err := b.repo.GetProviderNames()
	if err != nil || names[domain] == "" {
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Провайдер не найден.", "Provider not found.")+"</b>", b.cancelMarkup())
		return
	}

	b.setState(chatID, &userState{Step: "provider_rename", Domain: domain})
	b.editMessage(chatID, messageID,
		fmt.Sprintf("<b>✏️ %s</b>\n\n%s <b>%s</b>:", b.tr("Переименование провайдера", "Rename Provider"), b.tr("Отправьте новое имя для", "Send the new name for"), xmlEscape(b.providerLabel(domain))),
		b.cancelMarkup())
}

// processRenameProviderText validates and applies the new provider name.
func (b *Bot) processRenameProviderText(chatID int64, text string) {
	state := b.getState(chatID)
	b.clearState(chatID)

	name := strings.TrimSpace(text)
	if name == "" {
		b.editMessage(chatID, b.getMainMenuID(chatID),
			"<b>✏️ "+b.tr("Переименование провайдера", "Rename Provider")+"</b>\n\n"+b.tr("Имя не может быть пустым. Отправьте имя или нажмите «Отмена»:", "Name cannot be empty. Send a name or press Cancel:"),
			b.cancelMarkup())
		return
	}
	if len(name) > 64 {
		b.editMessage(chatID, b.getMainMenuID(chatID),
			"<b>✏️ "+b.tr("Переименование провайдера", "Rename Provider")+"</b>\n\n"+b.tr("Имя слишком длинное (максимум 64 символа). Отправьте более короткое имя:", "Name is too long (max 64 characters). Send a shorter name:"),
			b.cancelMarkup())
		return
	}
	if state == nil || state.Domain == "" {
		b.handleProvidersMenu(chatID, b.getMainMenuID(chatID))
		return
	}

	provider := domain.SubscriptionProvider{Domain: state.Domain, Name: name}
	if err := b.repo.UpsertSubscriptionProvider(provider); err != nil {
		log.Printf("Bot: failed to rename provider %q: %v", state.Domain, err)
		b.editMessage(chatID, b.getMainMenuID(chatID), "<b>❌ "+b.tr("Не удалось переименовать провайдера.", "Failed to rename provider.")+"</b>", b.cancelMarkup())
		return
	}

	b.audit.Log("telegram_bot", audit.ActionUpdateSettings, provider.Domain, "Renamed provider "+provider.Domain+" -> "+provider.Name+" (via Telegram bot)")
	b.showProviderDetail(chatID, b.getMainMenuID(chatID), state.Domain, b.tr("Переименован в", "Renamed to")+" "+provider.Name)
}

// handleProviderDeleteRequest asks for explicit confirmation before deleting a provider mapping.
func (b *Bot) handleProviderDeleteRequest(chatID int64, messageID int, domain string) {
	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.btn("🗑️", "Да, удалить", "Yes, Delete"), "provider:delconfirm:"+domain),
			tgbotapi.NewInlineKeyboardButtonData(b.btn("↩️", "Отмена", "Cancel"), "provider:detail:"+domain),
		),
	)
	b.editMessage(chatID, messageID,
		fmt.Sprintf("<b>🗑️ %s</b>\n\n%s <b>%s</b>? %s", b.tr("Удаление провайдера", "Delete Provider"), b.tr("Вы уверены, что хотите навсегда удалить провайдера", "Are you sure you want to permanently delete provider"), xmlEscape(b.providerLabel(domain)), b.tr("Это действие необратимо.", "This cannot be undone.")),
		&markup)
}

// handleProviderDeleteConfirm performs the actual provider deletion.
func (b *Bot) handleProviderDeleteConfirm(chatID int64, messageID int, domain string) {
	if err := b.repo.DeleteSubscriptionProvider(domain); err != nil {
		log.Printf("Bot: failed to delete provider %s: %v", domain, err)
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Не удалось удалить провайдера.", "Failed to delete provider.")+"</b>", b.cancelMarkup())
		return
	}

	b.audit.Log("telegram_bot", audit.ActionUpdateSettings, domain, "Deleted provider "+domain+" (via Telegram bot)")
	b.handleProvidersMenu(chatID, messageID)
}

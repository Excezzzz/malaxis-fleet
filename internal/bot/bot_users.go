package bot

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"malaxis-fleet/internal/audit"
	"malaxis-fleet/internal/domain"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// --- User & Role management ---

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

// handleUsersMenu lists all fleet users with their role/rank and offers an
// Add User button.
func (b *Bot) handleUsersMenu(chatID int64, messageID int) {
	users, err := b.repo.GetAllUsers()
	if err != nil {
		log.Printf("Bot: failed to list users: %v", err)
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Не удалось получить список пользователей.", "Failed to list users.")+"</b>", b.cancelMarkup())
		return
	}

	var text strings.Builder
	text.WriteString("<b>👥 " + b.tr("Управление пользователями", "Manage Users") + "</b>\n\n")
	if len(users) == 0 {
		text.WriteString(b.tr("Пользователей пока нет.", "No users yet."))
	} else {
		text.WriteString(fmt.Sprintf(b.tr("%d пользователей. Нажмите на пользователя для управления:\n", "%d user(s). Tap a user to manage:\n"), len(users)))
	}

	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(users)+2)
	_, emojis := b.botPrefs()
	for _, u := range users {
		roleName := u.RoleName
		if roleName == "" {
			roleName = u.Role
		}
		label := b.fmtBtn(xmlEscape(u.Username), "👤", emojis)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, "user:detail:"+strconv.FormatInt(u.ID, 10)),
		))
	}
	rows = append(rows,
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.btn("➕", "Добавить пользователя", "Add User"), "users:add"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.btn("🔙", "Назад", "Back"), "menu:main"),
		),
	)

	markup := tgbotapi.NewInlineKeyboardMarkup(rows...)
	b.editMessage(chatID, messageID, text.String(), &markup)
}

// showUserDetail renders a single user with its role/rank and the full set of
// management actions: change role, change password, delete user. The manage
// actions are hidden for the owner account and for any role ranked at or above
// the built-in admin rank (mirroring the web API's rank guards).
func (b *Bot) showUserDetail(chatID int64, messageID int, userID int64, note string) {
	user, err := b.repo.GetUserByID(userID)
	if err != nil {
		log.Printf("Bot: user %d not found: %v", userID, err)
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Пользователь не найден.", "User not found.")+"</b>", b.cancelMarkup())
		return
	}

	roleName := user.RoleName
	if roleName == "" {
		roleName = user.Role
	}

	var text strings.Builder
	text.WriteString("<b>👤 " + b.tr("Данные пользователя", "User Details") + "</b>\n\n")
	text.WriteString(fmt.Sprintf("<b>%s</b> %s\n", b.tr("Имя пользователя:", "Username:"), xmlEscape(user.Username)))
	text.WriteString(fmt.Sprintf("<b>%s</b> %s [%s <code>%d</code>]\n", b.tr("Роль:", "Role:"), xmlEscape(roleName), b.tr("ранг", "rank"), b.roleRankOf(user.Role)))
	text.WriteString(fmt.Sprintf("<b>%s</b> %s", b.tr("Создан:", "Created:"), user.CreatedAt.Format("2006-01-02 15:04")))
	if note != "" {
		text.WriteString("\n\n✅ <i>" + note + "</i>")
	}

	isOwner := strings.EqualFold(user.Username, "admin") || strings.EqualFold(user.Role, domain.RoleOwner)
	rank := b.roleRankOf(user.Role)

	var rows [][]tgbotapi.InlineKeyboardButton
	if !isOwner && rank < domain.RoleRankAdmin {
		rows = append(rows,
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(b.btn("🔄", "Сменить роль", "Change Role"), "user:role:"+strconv.FormatInt(user.ID, 10)),
				tgbotapi.NewInlineKeyboardButtonData(b.btn("🔑", "Сменить пароль", "Change Password"), "user:pw:"+strconv.FormatInt(user.ID, 10)),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(b.btn("🗑️", "Удалить пользователя", "Delete User"), "user:del:"+strconv.FormatInt(user.ID, 10)),
			),
		)
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(b.btn("🔙", "К пользователям", "Back to Users"), "users:menu"),
	))
	markup := tgbotapi.NewInlineKeyboardMarkup(rows...)
	b.editMessage(chatID, messageID, text.String(), &markup)
}

// showUserRolePicker renders every non-owner role as a target for reassigning
// the user's role. One button per row so every role stays fully visible.
func (b *Bot) showUserRolePicker(chatID int64, messageID int, userID int64) {
	user, err := b.repo.GetUserByID(userID)
	if err != nil {
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Пользователь не найден.", "User not found.")+"</b>", b.cancelMarkup())
		return
	}

	roles, err := b.repo.GetAllRoles()
	if err != nil {
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Не удалось загрузить роли.", "Failed to load roles.")+"</b>", b.cancelMarkup())
		return
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, r := range roles {
		if strings.EqualFold(r.Name, domain.RoleOwner) {
			continue
		}
		rank := r.Rank
		if rank < 1 {
			rank = domain.RoleRank(r.Name)
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("%s [%d]", r.Name, rank),
				fmt.Sprintf("user:setrole:%d:%d", user.ID, r.ID),
			),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(b.btn("❌", "Отмена", "Cancel"), "user:detail:"+strconv.FormatInt(user.ID, 10)),
	))

	markup := tgbotapi.NewInlineKeyboardMarkup(rows...)
	b.editMessage(chatID, messageID,
		fmt.Sprintf("<b>🔄 %s</b>\n\n%s <b>%s</b>:", b.tr("Смена роли", "Change Role"), b.tr("Выберите новую роль для", "Select the new role for"), xmlEscape(user.Username)),
		&markup)
}

// handleUserChangeRole assigns a new role to an existing user, mirroring the
// web API's rank guards: the owner role is never assignable and the bot only
// ever reassigns strictly lower-ranked roles.
func (b *Bot) handleUserChangeRole(chatID int64, messageID int, userID, roleID int64) {
	user, err := b.repo.GetUserByID(userID)
	if err != nil {
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Пользователь не найден.", "User not found.")+"</b>", b.cancelMarkup())
		return
	}
	role, err := b.repo.GetRoleByID(roleID)
	if err != nil || role == nil {
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Роль не найдена.", "Role not found.")+"</b>", b.cancelMarkup())
		return
	}
	if strings.EqualFold(role.Name, domain.RoleOwner) {
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Роль владельца нельзя назначить пользователю.", "The owner role cannot be assigned to a user.")+"</b>", b.cancelMarkup())
		return
	}
	if strings.EqualFold(user.Username, "admin") || strings.EqualFold(user.Role, domain.RoleOwner) {
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Учётная запись владельца не может быть переведена.", "The owner account cannot be reassigned.")+"</b>", b.cancelMarkup())
		return
	}

	oldRole := user.RoleName
	if oldRole == "" {
		oldRole = user.Role
	}
	if strings.EqualFold(oldRole, role.Name) {
		b.editMessage(chatID, messageID, "<b>ℹ️ "+b.tr("У пользователя уже есть эта роль.", "User already has this role.")+"</b>", b.cancelMarkup())
		return
	}

	user.Role = role.Name
	user.RoleName = role.Name
	user.ColorHex = role.ColorHex
	if err := b.repo.UpdateUser(user); err != nil {
		log.Printf("Bot: failed to update role for user %d: %v", userID, err)
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Не удалось обновить роль.", "Failed to update role.")+"</b>", b.cancelMarkup())
		return
	}

	b.audit.Log("telegram_bot", audit.ActionUpdateUser, user.Username, "Changed role from "+oldRole+" to "+role.Name+" (via Telegram bot)")
	b.showUserDetail(chatID, messageID, userID, b.tr("Роль изменена на", "Role changed to")+" "+role.Name)
}

// handleUserPwRequest opens the password-change prompt; the next text message
// becomes the new password (and is deleted after processing).
func (b *Bot) handleUserPwRequest(chatID int64, messageID int, userID int64) {
	user, err := b.repo.GetUserByID(userID)
	if err != nil {
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Пользователь не найден.", "User not found.")+"</b>", b.cancelMarkup())
		return
	}
	b.setState(chatID, &userState{Step: "user_pw", TargetID: userID})
	b.editMessage(chatID, messageID,
		fmt.Sprintf("<b>🔑 %s</b>\n\n%s <b>%s</b>:\n\n%s", b.tr("Смена пароля", "Change Password"), b.tr("Отправьте новый пароль для", "Send the new password for"), xmlEscape(user.Username), b.tr("Сообщение будет удалено после обработки.", "The message will be deleted after processing.")),
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
			"<b>🔑 "+b.tr("Смена пароля", "Change Password")+"</b>\n\n"+b.tr("Пароль не может быть пустым. Отправьте пароль или нажмите «Отмена»:", "Password cannot be empty. Send a password or press Cancel:"),
			b.cancelMarkup())
		return
	}

	user, err := b.repo.GetUserByID(state.TargetID)
	if err != nil {
		b.editMessage(chatID, b.getMainMenuID(chatID), "<b>❌ "+b.tr("Пользователь не найден.", "User not found.")+"</b>", b.cancelMarkup())
		return
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("Bot: failed to hash new password for user %d: %v", state.TargetID, err)
		b.editMessage(chatID, b.getMainMenuID(chatID), "<b>❌ "+b.tr("Не удалось обновить пароль.", "Failed to update password.")+"</b>", b.cancelMarkup())
		return
	}

	if err := b.repo.UpdateUserPassword(state.TargetID, string(hashed)); err != nil {
		log.Printf("Bot: failed to update password for user %d: %v", state.TargetID, err)
		b.editMessage(chatID, b.getMainMenuID(chatID), "<b>❌ "+b.tr("Не удалось обновить пароль.", "Failed to update password.")+"</b>", b.cancelMarkup())
		return
	}

	b.audit.Log("telegram_bot", audit.ActionUpdateUser, user.Username, "Changed password (via Telegram bot)")
	b.showUserDetail(chatID, b.getMainMenuID(chatID), state.TargetID, b.tr("Пароль обновлён", "Password updated"))
}

// handleUserDeleteRequest asks for explicit confirmation before deleting a
// user; the owner account and admin are always protected.
func (b *Bot) handleUserDeleteRequest(chatID int64, messageID int, userID int64) {
	user, err := b.repo.GetUserByID(userID)
	if err != nil {
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Пользователь не найден.", "User not found.")+"</b>", b.cancelMarkup())
		return
	}
	if strings.EqualFold(user.Username, "admin") || strings.EqualFold(user.Role, domain.RoleOwner) {
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Учётная запись владельца не может быть удалена.", "The owner account cannot be deleted.")+"</b>", b.cancelMarkup())
		return
	}

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.btn("🗑️", "Да, удалить", "Yes, Delete"), "user:delconfirm:"+strconv.FormatInt(user.ID, 10)),
			tgbotapi.NewInlineKeyboardButtonData(b.btn("↩️", "Отмена", "Cancel"), "user:detail:"+strconv.FormatInt(user.ID, 10)),
		),
	)
	b.editMessage(chatID, messageID,
		fmt.Sprintf("<b>🗑️ %s</b>\n\n%s <b>%s</b>? %s", b.tr("Удаление пользователя", "Delete User"), b.tr("Вы уверены, что хотите навсегда удалить", "Are you sure you want to permanently delete"), xmlEscape(user.Username), b.tr("Это действие необратимо.", "This cannot be undone.")),
		&markup)
}

// handleUserDeleteConfirm performs the actual user deletion.
func (b *Bot) handleUserDeleteConfirm(chatID int64, messageID int, userID int64) {
	user, err := b.repo.GetUserByID(userID)
	if err != nil {
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Пользователь не найден.", "User not found.")+"</b>", b.cancelMarkup())
		return
	}
	if strings.EqualFold(user.Username, "admin") || strings.EqualFold(user.Role, domain.RoleOwner) {
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Учётная запись владельца не может быть удалена.", "The owner account cannot be deleted.")+"</b>", b.cancelMarkup())
		return
	}

	username := user.Username
	if err := b.repo.DeleteUser(userID); err != nil {
		log.Printf("Bot: failed to delete user %d: %v", userID, err)
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Не удалось удалить пользователя.", "Failed to delete user.")+"</b>", b.cancelMarkup())
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
			"<b>👥 "+b.tr("Добавление пользователя", "Add User")+"</b>\n\n"+b.tr("Некорректный ввод. Отправьте <b>имя пользователя</b> и <b>пароль</b> через пробел (например, <code>john secret123</code>):", "Invalid input. Send <b>username</b> and <b>password</b> separated by a space (e.g., <code>john secret123</code>):"),
			b.cancelMarkup())
		return
	}

	username := parts[0]
	password := parts[1]

	if _, err := b.repo.GetUserByUsername(username); err == nil {
		b.editMessage(chatID, b.getMainMenuID(chatID),
			"<b>❌ "+b.tr("Имя пользователя уже существует.", "Username already exists.")+"</b> "+b.tr("Отправьте другое имя и пароль:", "Please send a different username and password:"),
			b.cancelMarkup())
		return
	}

	b.setState(chatID, &userState{Step: "add_user_role", Username: username, Password: password})
	b.showRoleSelection(chatID, b.getMainMenuID(chatID), username, "")
}

// showRoleSelection renders every non-owner role from the database as an
// inline keyboard for the in-progress add-user flow. One button per row so
// every role stays fully visible.
func (b *Bot) showRoleSelection(chatID int64, messageID int, username, note string) {
	roles, err := b.repo.GetAllRoles()
	if err != nil {
		log.Printf("Bot: failed to list roles for user creation: %v", err)
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Не удалось загрузить роли.", "Failed to load roles.")+"</b>", b.cancelMarkup())
		return
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, r := range roles {
		if strings.EqualFold(r.Name, domain.RoleOwner) {
			continue
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(r.Name, "user:create:"+strconv.FormatInt(r.ID, 10)),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(b.btn("❌", "Отмена", "Cancel"), "state:cancel"),
	))

	var text strings.Builder
	text.WriteString("<b>👥 " + b.tr("Добавление пользователя", "Add User") + "</b>\n\n" + b.tr("Выберите роль для", "Select a role for") + " <b>" + xmlEscape(username) + "</b>:")
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
		b.editMessage(chatID, messageID, "<b>⚠️ "+b.tr("Процесс добавления пользователя истёк.", "Add-user flow expired.")+"</b> "+b.tr("Начните заново из главного меню.", "Please start again from the main menu."), b.cancelMarkup())
		return
	}

	roleID, err := strconv.ParseInt(roleIDStr, 10, 64)
	if err != nil {
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Неверная роль.", "Invalid role.")+"</b>", b.cancelMarkup())
		return
	}
	role, err := b.repo.GetRoleByID(roleID)
	if err != nil || role == nil || strings.EqualFold(role.Name, domain.RoleOwner) {
		log.Printf("Bot: role %d not found for user creation: %v", roleID, err)
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Роль не найдена.", "Role not found.")+"</b>", b.cancelMarkup())
		return
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(state.Password), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("Bot: failed to hash password for user %q: %v", state.Username, err)
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Не удалось создать пользователя.", "Failed to create user.")+"</b>", b.cancelMarkup())
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
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Не удалось создать пользователя.", "Failed to create user.")+"</b> "+b.tr("Возможно, имя уже занято.", "The username may already be taken."), b.cancelMarkup())
		return
	}

	b.audit.Log("telegram_bot", audit.ActionCreateUser, state.Username, "Created user via Telegram bot with role "+role.Name)
	b.clearState(chatID)

	text, markup := b.getMainMenuContent()
	b.editMessage(chatID, messageID,
		fmt.Sprintf("✅ <b>%s</b>\n\n<b>%s</b> %s\n<b>%s</b> %s\n\n%s",
			b.tr("Пользователь создан!", "User created!"),
			b.tr("Имя:", "Username:"), xmlEscape(state.Username),
			b.tr("Роль:", "Role:"), xmlEscape(role.Name), text),
		&markup)
}

// handleRolesMenu lists all defined roles (with their ranks) and offers an
// Add Role button.
func (b *Bot) handleRolesMenu(chatID int64, messageID int) {
	roles, err := b.repo.GetAllRoles()
	if err != nil {
		log.Printf("Bot: failed to list roles: %v", err)
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Не удалось получить список ролей.", "Failed to list roles.")+"</b>", b.cancelMarkup())
		return
	}

	var text strings.Builder
	text.WriteString("<b>🛡️ " + b.tr("Управление ролями", "Manage Roles") + "</b>\n\n")
	if len(roles) == 0 {
		text.WriteString(b.tr("Роли не заданы.", "No roles defined."))
	} else {
		text.WriteString(fmt.Sprintf(b.tr("%d ролей. Нажмите на роль для управления:\n", "%d role(s). Tap a role to manage:\n"), len(roles)))
	}

	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(roles)+2)
	_, emojis := b.botPrefs()
	for _, r := range roles {
		rank := r.Rank
		if rank < 1 {
			rank = domain.RoleRank(r.Name)
		}
		label := b.fmtBtn(fmt.Sprintf("%s [%s %d]", xmlEscape(r.Name), b.tr("ранг", "rank"), rank), "🛡️", emojis)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, "role:detail:"+strconv.FormatInt(r.ID, 10)),
		))
	}
	rows = append(rows,
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.btn("➕", "Добавить роль", "Add Role"), "roles:add"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.btn("🔙", "Назад", "Back"), "menu:main"),
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
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Роль не найдена.", "Role not found.")+"</b>", b.cancelMarkup())
		return
	}

	rank := role.Rank
	if rank < 1 {
		rank = domain.RoleRank(role.Name)
	}
	userCount, _ := b.repo.CountUsersByRoleName(role.Name)

	var text strings.Builder
	text.WriteString("<b>🛡️ " + b.tr("Данные роли", "Role Details") + "</b>\n\n")
	text.WriteString(fmt.Sprintf("<b>%s</b> %s\n", b.tr("Имя:", "Name:"), xmlEscape(role.Name)))
	text.WriteString(fmt.Sprintf("<b>%s</b> <code>%d</code>\n", b.tr("Ранг:", "Rank:"), rank))
	text.WriteString(fmt.Sprintf("<b>%s</b> %d\n", b.tr("Назначено пользователей:", "Users assigned:"), userCount))
	if role.OwnerID == "system" {
		text.WriteString("<b>" + b.tr("Тип:", "Type:") + "</b> " + b.tr("системная роль", "system role"))
	}
	if note != "" {
		text.WriteString("\n\n✅ <i>" + note + "</i>")
	}

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.btn("✏️", "Переименовать роль", "Rename Role"), "role:rename:"+strconv.FormatInt(role.ID, 10)),
			tgbotapi.NewInlineKeyboardButtonData(b.btn("🔢", "Изменить ранг", "Change Rank"), "role:rank:"+strconv.FormatInt(role.ID, 10)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.btn("🗑️", "Удалить роль", "Delete Role"), "role:del:"+strconv.FormatInt(role.ID, 10)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.btn("🔙", "К ролям", "Back to Roles"), "roles:menu"),
		),
	)
	b.editMessage(chatID, messageID, text.String(), &markup)
}

// handleRoleRenameRequest opens the rename prompt; the next text message
// becomes the role's new name, guarded against reserved/duplicate names.
func (b *Bot) handleRoleRenameRequest(chatID int64, messageID int, roleID int64) {
	role, err := b.repo.GetRoleByID(roleID)
	if err != nil || role == nil {
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Роль не найдена.", "Role not found.")+"</b>", b.cancelMarkup())
		return
	}
	if role.OwnerID == "system" {
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Системные роли нельзя переименовывать.", "System roles cannot be renamed.")+"</b>", b.cancelMarkup())
		return
	}

	b.setState(chatID, &userState{Step: "role_rename", TargetID: roleID})
	b.editMessage(chatID, messageID,
		fmt.Sprintf("<b>✏️ %s</b>\n\n%s <b>%s</b>\n%s", b.tr("Переименование роли", "Rename Role"), b.tr("Отправьте новое имя для роли", "Send the new name for role"), xmlEscape(role.Name), b.tr("(owner, admin, client, viewer зарезервированы):", "(owner, admin, client, viewer are reserved):")),
		b.cancelMarkup())
}

// processRenameRoleText validates and applies the new role name.
func (b *Bot) processRenameRoleText(chatID int64, text string) {
	state := b.getState(chatID)
	b.clearState(chatID)

	name := strings.TrimSpace(text)
	if name == "" {
		b.editMessage(chatID, b.getMainMenuID(chatID),
			"<b>✏️ "+b.tr("Переименование роли", "Rename Role")+"</b>\n\n"+b.tr("Имя роли не может быть пустым. Отправьте имя или нажмите «Отмена»:", "Role name cannot be empty. Send a name or press Cancel:"),
			b.cancelMarkup())
		return
	}

	lower := strings.ToLower(name)
	if lower == "owner" || lower == "admin" || lower == "client" || lower == "viewer" {
		b.editMessage(chatID, b.getMainMenuID(chatID),
			"<b>❌ "+b.tr("Зарезервированное имя роли.", "Reserved role name.")+"</b> "+b.tr("Выберите другое имя:", "Please choose a different name:"),
			b.cancelMarkup())
		return
	}
	if _, err := b.repo.GetRoleByName(name); err == nil {
		b.editMessage(chatID, b.getMainMenuID(chatID),
			"<b>❌ "+b.tr("Роль с таким именем уже существует.", "Role name already exists.")+"</b> "+b.tr("Выберите другое имя:", "Please choose a different name:"),
			b.cancelMarkup())
		return
	}

	role, err := b.repo.GetRoleByID(state.TargetID)
	if err != nil || role == nil || role.OwnerID == "system" {
		b.editMessage(chatID, b.getMainMenuID(chatID), "<b>❌ "+b.tr("Роль не найдена.", "Role not found.")+"</b>", b.cancelMarkup())
		return
	}

	oldName := role.Name
	role.Name = name
	if err := b.repo.UpdateCustomRole(role); err != nil {
		log.Printf("Bot: failed to rename role %d: %v", state.TargetID, err)
		b.editMessage(chatID, b.getMainMenuID(chatID), "<b>❌ "+b.tr("Не удалось переименовать роль.", "Failed to rename role.")+"</b>", b.cancelMarkup())
		return
	}

	b.audit.Log("telegram_bot", audit.ActionUpdateSettings, role.Name, "Renamed role from "+oldName+" to "+name+" (via Telegram bot)")
	b.showRoleDetail(chatID, b.getMainMenuID(chatID), state.TargetID, b.tr("Переименована в", "Renamed to")+" "+role.Name)
}

// handleRoleRankRequest opens the rank prompt; the next text message becomes
// the role's new rank (1-99).
func (b *Bot) handleRoleRankRequest(chatID int64, messageID int, roleID int64) {
	role, err := b.repo.GetRoleByID(roleID)
	if err != nil || role == nil {
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Роль не найдена.", "Role not found.")+"</b>", b.cancelMarkup())
		return
	}
	if role.OwnerID == "system" {
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Ранги системных ролей фиксированы.", "System role ranks are fixed.")+"</b>", b.cancelMarkup())
		return
	}
	if role.Name == domain.RoleOwner {
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Ранг роли владельца неизменяем.", "The owner role rank is immutable.")+"</b>", b.cancelMarkup())
		return
	}

	b.setState(chatID, &userState{Step: "role_rank", TargetID: roleID})
	b.editMessage(chatID, messageID,
		fmt.Sprintf("<b>🔢 %s</b>\n\n%s (<b>1-99</b>) %s <b>%s</b>:", b.tr("Изменение ранга", "Change Rank"), b.tr("Отправьте новый ранг", "Send the new rank"), b.tr("для роли", "for role"), xmlEscape(role.Name)),
		b.cancelMarkup())
}

// processChangeRoleRankText validates and applies the new role rank.
func (b *Bot) processChangeRoleRankText(chatID int64, text string) {
	state := b.getState(chatID)
	b.clearState(chatID)

	rank, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || rank < 1 || rank > 99 {
		b.editMessage(chatID, b.getMainMenuID(chatID),
			"<b>🔢 "+b.tr("Изменение ранга", "Change Rank")+"</b>\n\n"+b.tr("Неверный ранг. Отправьте целое число от <b>1</b> до <b>99</b>:", "Invalid rank. Send a whole number between <b>1</b> and <b>99</b>:"),
			b.cancelMarkup())
		return
	}

	role, err := b.repo.GetRoleByID(state.TargetID)
	if err != nil || role == nil || role.OwnerID == "system" {
		b.editMessage(chatID, b.getMainMenuID(chatID), "<b>❌ "+b.tr("Роль не найдена.", "Role not found.")+"</b>", b.cancelMarkup())
		return
	}

	oldRank := role.Rank
	role.Rank = rank
	if err := b.repo.UpdateCustomRole(role); err != nil {
		log.Printf("Bot: failed to change rank for role %d: %v", state.TargetID, err)
		b.editMessage(chatID, b.getMainMenuID(chatID), "<b>❌ "+b.tr("Не удалось обновить ранг.", "Failed to update rank.")+"</b>", b.cancelMarkup())
		return
	}

	b.audit.Log("telegram_bot", audit.ActionUpdateSettings, role.Name, "Changed rank from "+strconv.Itoa(oldRank)+" to "+strconv.Itoa(rank)+" (via Telegram bot)")
	b.showRoleDetail(chatID, b.getMainMenuID(chatID), state.TargetID, b.tr("Ранг обновлён до", "Rank updated to")+" "+strconv.Itoa(rank))
}

// handleRoleDeleteRequest asks for explicit confirmation. System roles and the
// owner role are never deletable; roles that still have assigned users also
// cannot be deleted.
func (b *Bot) handleRoleDeleteRequest(chatID int64, messageID int, roleID int64) {
	role, err := b.repo.GetRoleByID(roleID)
	if err != nil || role == nil {
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Роль не найдена.", "Role not found.")+"</b>", b.cancelMarkup())
		return
	}
	if role.OwnerID == "system" || role.Name == domain.RoleOwner {
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Системные роли и роль владельца нельзя удалять.", "System and owner roles cannot be deleted.")+"</b>", b.cancelMarkup())
		return
	}

	userCount, err := b.repo.CountUsersByRoleName(role.Name)
	if err == nil && userCount > 0 {
		b.editMessage(chatID, messageID,
			fmt.Sprintf("<b>❌ %s <b>%s</b> %s %d %s. %s</b>", b.tr("Роль", "Role"), xmlEscape(role.Name), b.tr("назначена", "is assigned to"), userCount, b.tr("пользователям.", "user(s)."), b.tr("Сначала переназначьте или удалите их.", "Reassign or delete them first.")),
			b.cancelMarkup())
		return
	}

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.btn("🗑️", "Да, удалить", "Yes, Delete"), "role:delconfirm:"+strconv.FormatInt(role.ID, 10)),
			tgbotapi.NewInlineKeyboardButtonData(b.btn("↩️", "Отмена", "Cancel"), "role:detail:"+strconv.FormatInt(role.ID, 10)),
		),
	)
	b.editMessage(chatID, messageID,
		fmt.Sprintf("<b>🗑️ %s</b>\n\n%s <b>%s</b>? %s", b.tr("Удаление роли", "Delete Role"), b.tr("Вы уверены, что хотите навсегда удалить роль", "Are you sure you want to permanently delete role"), xmlEscape(role.Name), b.tr("Это действие необратимо.", "This cannot be undone.")),
		&markup)
}

// handleRoleDeleteConfirm performs the actual role deletion.
func (b *Bot) handleRoleDeleteConfirm(chatID int64, messageID int, roleID int64) {
	role, err := b.repo.GetRoleByID(roleID)
	if err != nil || role == nil {
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Роль не найдена.", "Role not found.")+"</b>", b.cancelMarkup())
		return
	}
	if role.OwnerID == "system" || role.Name == domain.RoleOwner {
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Системные роли нельзя удалять.", "System roles cannot be deleted.")+"</b>", b.cancelMarkup())
		return
	}

	userCount, _ := b.repo.CountUsersByRoleName(role.Name)
	if userCount > 0 {
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("У роли ещё есть назначенные пользователи.", "Role still has assigned users.")+"</b>", b.cancelMarkup())
		return
	}

	if err := b.repo.DeleteCustomRole(roleID); err != nil {
		log.Printf("Bot: failed to delete role %d: %v", roleID, err)
		b.editMessage(chatID, messageID, "<b>❌ "+b.tr("Не удалось удалить роль.", "Failed to delete role.")+"</b>", b.cancelMarkup())
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
			"<b>🛡️ "+b.tr("Добавление роли", "Add Role")+"</b>\n\n"+b.tr("Имя роли не может быть пустым. Отправьте имя роли (например, <code>manager</code>):", "Role name cannot be empty. Send the role name (e.g., <code>manager</code>):"),
			b.cancelMarkup())
		return
	}

	lower := strings.ToLower(name)
	if lower == "owner" || lower == "admin" || lower == "client" || lower == "viewer" {
		b.editMessage(chatID, b.getMainMenuID(chatID),
			"<b>❌ "+b.tr("Зарезервированное имя роли.", "Reserved role name.")+"</b> "+b.tr("Выберите другое имя роли:", "Please choose a different role name:"),
			b.cancelMarkup())
		return
	}
	if _, err := b.repo.GetRoleByName(name); err == nil {
		b.editMessage(chatID, b.getMainMenuID(chatID),
			"<b>❌ "+b.tr("Роль с таким именем уже существует.", "Role name already exists.")+"</b> "+b.tr("Выберите другое имя:", "Please choose a different name:"),
			b.cancelMarkup())
		return
	}

	b.setState(chatID, &userState{Step: "add_role_rank", RoleName: name})
	b.editMessage(chatID, b.getMainMenuID(chatID),
		fmt.Sprintf("<b>🛡️ %s</b>\n\n%s <b>%s</b> %s\n\n%s (<b>1-99</b>):", b.tr("Добавление роли", "Add Role"), b.tr("Имя роли", "Role name"), xmlEscape(name), b.tr("принято.", "accepted."), b.tr("Отправьте ранг роли", "Send the role rank")),
		b.cancelMarkup())
}

// processAddRoleRankText validates the 1-99 rank and creates the role with
// empty (web-editable) permissions.
func (b *Bot) processAddRoleRankText(chatID int64, text string) {
	state := b.getState(chatID)

	rank, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || rank < 1 || rank > 99 {
		b.editMessage(chatID, b.getMainMenuID(chatID),
			"<b>🛡️ "+b.tr("Добавление роли", "Add Role")+"</b>\n\n"+b.tr("Неверный ранг. Отправьте целое число от <b>1</b> до <b>99</b>:", "Invalid rank. Send a whole number between <b>1</b> and <b>99</b>:"),
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
			"<b>❌ "+b.tr("Не удалось создать роль.", "Failed to create role.")+"</b> "+b.tr("Возможно, она уже существует.", "It may already exist."),
			b.cancelMarkup())
		return
	}

	b.audit.Log("telegram_bot", audit.ActionCreateUser, role.Name, "Created role via Telegram bot (rank: "+strconv.Itoa(rank)+")")
	b.clearState(chatID)
	b.handleRolesMenu(chatID, b.getMainMenuID(chatID))
}

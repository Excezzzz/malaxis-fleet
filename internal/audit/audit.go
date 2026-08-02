package audit

import (
	"fmt"
	"net/http"
	"time"

	"malaxis-fleet/internal/domain"
	"malaxis-fleet/internal/repository"
)

const (
	ActionLoginSuccess   = "LOGIN_SUCCESS"
	ActionLoginFailure   = "LOGIN_FAILURE"
	ActionLogout         = "LOGOUT"
	ActionCreateUser     = "CREATE_USER"
	ActionDeleteUser     = "DELETE_USER"
	ActionUpdateDevice   = "UPDATE_DEVICE"
	ActionDeleteDevice   = "DELETE_DEVICE"
	ActionUpdateUser     = "UPDATE_USER"
	ActionUpdatePassword = "UPDATE_PASSWORD"
	ActionUpdateSettings = "UPDATE_SETTINGS"
	ActionUpdateTemplate = "UPDATE_TEMPLATE"
)

type Logger struct {
	repo repository.Repository
}

func NewLogger(repo repository.Repository) *Logger {
	return &Logger{repo: repo}
}

func (l *Logger) Log(actor, action, target, details string) {
	logEntry := &domain.AuditLog{
		Timestamp: time.Now(),
		Actor:     actor,
		Action:    action,
		Target:    target,
		Details:   details,
	}
	if err := l.repo.AddAuditLog(logEntry); err != nil {
		// In a real app, you might want more robust error handling here
		fmt.Printf("Failed to write audit log: %v\n", err)
	}
}

func (l *Logger) LogFromRequest(r *http.Request, actor, action, target, details string) {
	// Attempt to get a more accurate IP address
	ip := r.Header.Get("X-Forwarded-For")
	if ip == "" {
		ip = r.RemoteAddr
	}

	fullDetails := fmt.Sprintf("IP: %s. %s", ip, details)
	l.Log(actor, action, target, fullDetails)
}

package api

import (
	"fmt"
	"net/http"
	"runtime/debug"
)

func PanicRecoveryMiddleware(botMgr BotManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					logMessage := fmt.Sprintf("🚨 [CRITICAL ERROR] Server Panic:\n\n%v\n\n%s", err, string(debug.Stack()))
					fmt.Println(logMessage)

					if botMgr != nil {
						botMgr.SendAdminMessage(logMessage)
					}

					http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

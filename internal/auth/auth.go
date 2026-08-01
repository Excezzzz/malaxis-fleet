package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"malaxis-fleet/internal/config"
	"malaxis-fleet/internal/domain"
	"malaxis-fleet/internal/repository"

	"github.com/gorilla/sessions"
)

var (
	// Store the cookie store as a package-level variable
	Store *sessions.CookieStore
)

type contextKey string

const (
	UserContextKey = contextKey("user")
	RoleContextKey = contextKey("role")
)

// InitStore initializes the session store.
func InitStore(cfg *config.Config) {
	Store = sessions.NewCookieStore([]byte(cfg.SessionSecret))

	isSecure := !strings.HasPrefix(cfg.DashboardDomain, "localhost")

	Store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   86400 * 7, // 7 days
		HttpOnly: true,
		Secure:   isSecure,
		SameSite: http.SameSiteLaxMode,
	}
}

// Middleware verifies the user session and handles CORS.
func Middleware(cfg *config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// CORS Headers
			w.Header().Set("Access-Control-Allow-Origin", cfg.DashboardDomain)
			w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
			w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")
			w.Header().Set("Access-Control-Allow-Credentials", "true")

			// Handle pre-flight requests
			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}

			session, err := Store.Get(r, "fleet-session")
			if err != nil {
				http.Error(w, "Unauthorized: Invalid session", http.StatusUnauthorized)
				return
			}

			// Check if user is authenticated
			userID, ok := session.Values["user_id"].(int64)
			if !ok || userID == 0 {
				http.Error(w, "Unauthorized: Not logged in", http.StatusUnauthorized)
				return
			}

			// Add user ID to context for handlers to use
			ctx := context.WithValue(r.Context(), UserContextKey, userID)

			// Add role to context if present in session (set by RequireRole middleware)
			if role, ok := session.Values["role"].(string); ok {
				ctx = context.WithValue(ctx, RoleContextKey, role)
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireRole returns middleware that ensures the authenticated user has one of the required roles.
func RequireRole(repo repository.Repository, allowedRoles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Get user ID from context (set by Middleware)
			userID, ok := r.Context().Value(UserContextKey).(int64)
			if !ok || userID == 0 {
				http.Error(w, "Unauthorized: Not authenticated", http.StatusUnauthorized)
				return
			}

			// Fetch user from DB to verify role
			user, err := repo.GetUserByID(userID)
			if err != nil {
				http.Error(w, "Forbidden: User not found", http.StatusForbidden)
				return
			}

			// Check if user has one of the allowed roles
			authorized := false
			for _, allowed := range allowedRoles {
				if user.Role == allowed {
					authorized = true
					break
				}
			}

			if !authorized {
				http.Error(w, "Forbidden: Insufficient permissions", http.StatusForbidden)
				return
			}

			// Store role in session for quick access
			session, err := Store.Get(r, "fleet-session")
			if err == nil {
				session.Values["role"] = user.Role
				session.Save(r, w)
			}

			// Add role to context
			ctx := context.WithValue(r.Context(), RoleContextKey, user.Role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireOwner is a convenience wrapper for RequireRole(repo, domain.RoleOwner)
func RequireOwner(repo repository.Repository) func(http.Handler) http.Handler {
	return RequireRole(repo, domain.RoleOwner)
}

// RequireAdminOrOwner is a convenience wrapper for RequireRole(repo, domain.RoleAdmin, domain.RoleOwner)
func RequireAdminOrOwner(repo repository.Repository) func(http.Handler) http.Handler {
	return RequireRole(repo, domain.RoleAdmin, domain.RoleOwner)
}

// RequirePermission returns middleware that checks if the authenticated user's role
// has a specific permission in permissions_json. Owner and admin bypass permission checks.
func RequirePermission(repo repository.Repository, permission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, ok := r.Context().Value(UserContextKey).(int64)
			if !ok || userID == 0 {
				http.Error(w, "Unauthorized: Not authenticated", http.StatusUnauthorized)
				return
			}

			user, err := repo.GetUserByID(userID)
			if err != nil {
				http.Error(w, "Forbidden: User not found", http.StatusForbidden)
				return
			}

			// Owner and admin have all permissions
			if user.Role == domain.RoleOwner || user.Role == domain.RoleAdmin {
				next.ServeHTTP(w, r)
				return
			}

			// Look up the role and check permissions_json
			role, err := repo.GetRoleByName(user.Role)
			if err != nil {
				http.Error(w, "Forbidden: Role not found", http.StatusForbidden)
				return
			}

			var perms []string
			permRaw := role.PermissionsJSON
			if err := json.Unmarshal([]byte(permRaw), &perms); err != nil {
				var permMap map[string]bool
				if err2 := json.Unmarshal([]byte(permRaw), &permMap); err2 != nil {
					http.Error(w, "Forbidden: Invalid permissions", http.StatusForbidden)
					return
				}
				for p, v := range permMap {
					if v {
						perms = append(perms, p)
					}
				}
			}

			hasPermission := false
			for _, p := range perms {
				if p == permission {
					hasPermission = true
					break
				}
			}

			if !hasPermission {
				http.Error(w, "Forbidden: Missing permission: "+permission, http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"malaxis-fleet/internal/audit"
	"malaxis-fleet/internal/auth"
	"malaxis-fleet/internal/domain"

	"github.com/gorilla/mux"
	"golang.org/x/crypto/bcrypt"
)

// --- User Handlers ---

func (a *API) GetUsersHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermViewUsers) {
		return
	}

	users, err := a.repo.GetAllUsers()
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		log.Printf("Error getting users: %v", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(users)
}

// --- Owner-Only Handlers ---

type CreateUserRequest struct {
	Username string      `json:"username"`
	Password string      `json:"password"`
	Role     string      `json:"role"`
	RoleID   interface{} `json:"role_id,omitempty"`
	ColorHex string      `json:"color_hex,omitempty"`
}

func (a *API) CreateUserHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermCreateUsers) {
		return
	}

	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)
	actorUser, err := a.repo.GetUserByID(actorID)
	if err != nil || actorUser == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(ErrResponse{Error: "Forbidden: User not found"})
		return
	}

	var req CreateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[CreateUser] JSON decode error: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrResponse{Error: "Bad Request: " + err.Error()})
		return
	}
	log.Printf("[CreateUser] Body: username=%q role=%q role_id=%v", req.Username, req.Role, req.RoleID)

	if req.Username == "" || req.Password == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrResponse{Error: "username and password are required"})
		return
	}

	if req.Role == "" && req.RoleID != nil {
		if rid := toInt64(req.RoleID); rid != nil {
			roleLookup, err := a.repo.GetRoleByID(*rid)
			if err == nil {
				req.Role = roleLookup.Name
				if req.ColorHex == "" {
					req.ColorHex = roleLookup.ColorHex
				}
			}
		} else if roleName, ok := req.RoleID.(string); ok && roleName != "" {
			roleLookup, err := a.repo.GetRoleByName(roleName)
			if err == nil {
				req.Role = roleLookup.Name
				if req.ColorHex == "" {
					req.ColorHex = roleLookup.ColorHex
				}
			}
		}
	}
	if req.Role == "" {
		req.Role = domain.RoleClient
	}

	// ROLE HIERARCHY: an actor may only create users whose role rank is STRICTLY LOWER than their own. Creating a user with an equal or higher rank (e.g. an admin creating another admin, or a client escalating to owner) is forbidden.
	actorRank := a.roleRank(actorUser.Role)
	if a.roleRank(req.Role) >= actorRank {
		a.writeForbidden(w, "Forbidden: Cannot create users with equal or higher role rank")
		return
	}

	// The owner role is reserved for the original seeded admin account. Nobody (not even the owner) may create additional owner accounts.
	if req.Role == domain.RoleOwner {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(ErrResponse{Error: "The owner role is reserved for the original admin account"})
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), domain.BcryptCost)
	if err != nil {
		log.Printf("[CreateUser] bcrypt error: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrResponse{Error: "Failed to hash password"})
		return
	}

	user := &domain.User{
		Username:     req.Username,
		PasswordHash: string(hashedPassword),
		Role:         req.Role,
		CreatedAt:    time.Now(),
		ColorHex:     req.ColorHex,
	}

	id, err := a.repo.AddUser(user)
	if err != nil {
		log.Printf("[CreateUser] add error: %v", err)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(err.Error(), "23505") {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(ErrResponse{Error: "Username already exists. Please choose a different name."})
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrResponse{Error: err.Error()})
		}
		return
	}
	user.ID = id
	user.RoleName = user.Role
	user.RoleColor = user.ColorHex

	a.auditLogger.LogFromRequest(r, a.actorName(actorUser), audit.ActionCreateUser, user.Username, "Role: "+user.Role)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(user)
}

func (a *API) UpdateUserHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermEditUsers) {
		return
	}

	vars := mux.Vars(r)
	idStr := vars["id"]
	log.Printf("[UpdateUser] Path ID: %s", idStr)

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		log.Printf("[UpdateUser] Invalid ID '%s': %v", idStr, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrResponse{Error: "Invalid user ID: " + idStr})
		return
	}

	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)

	actorUser, errGet := a.repo.GetUserByID(actorID)
	if errGet != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(ErrResponse{Error: "Forbidden: User not found"})
		return
	}

	var req UpdateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[UpdateUser] JSON decode error: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrResponse{Error: "Bad Request: " + err.Error()})
		return
	}
	log.Printf("[UpdateUser] Body: role=%q role_id=%v color_hex=%q has_password=%v username=%q", req.Role, req.RoleID, req.ColorHex, req.Password != "", req.Username)

	user, err := a.repo.GetUserByID(id)
	if err != nil {
		log.Printf("[UpdateUser] User %d not found: %v", id, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(ErrResponse{Error: "User not found"})
		return
	}

	isSelf := actorID == id

	if isSelf {
		// Self-editing is allowed for username, password and color, but the RBAC self-demotion protection still holds: nobody may change their own role through this endpoint.
		if req.Role != "" || req.RoleID != nil {
			a.writeForbidden(w, "Forbidden: Cannot change your own role")
			return
		}
	} else {
		// ROLE HIERARCHY: an actor may only edit a target whose role rank is STRICTLY LOWER than their own. Editing an equal/higher rank (an admin demoting another admin, or anything touching the owner) is forbidden.
		if a.roleRank(user.Role) >= a.roleRank(actorUser.Role) {
			a.writeForbidden(w, "Forbidden: Cannot modify users with equal or higher role rank")
			return
		}

		if req.RoleID != nil {
			if rid := toInt64(req.RoleID); rid != nil {
				roleLookup, err := a.repo.GetRoleByID(*rid)
				if err == nil {
					req.Role = roleLookup.Name
					if req.ColorHex == "" {
						user.ColorHex = roleLookup.ColorHex
					}
				}
			} else if roleName, ok := req.RoleID.(string); ok && roleName != "" {
				roleLookup, err := a.repo.GetRoleByName(roleName)
				if err == nil {
					req.Role = roleLookup.Name
					if req.ColorHex == "" {
						user.ColorHex = roleLookup.ColorHex
					}
				}
			}
		}
		if req.Role != "" {
			// An actor may not grant a target a role with an equal/higher rank than themselves, even when the target currently ranks lower.
			if a.roleRank(req.Role) >= a.roleRank(actorUser.Role) {
				a.writeForbidden(w, "Forbidden: Cannot assign a role with equal or higher role rank")
				return
			}
			user.Role = req.Role
		}

		// Owner role protection: the original admin account (seeded by UpsertAdminUser) is the sole owner and cannot be demoted; no other user may ever be assigned the owner role.
		if user.Role == domain.RoleOwner {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(ErrResponse{Error: "The owner role is reserved for the original admin account"})
			return
		}
	}

	if req.ColorHex != "" {
		user.ColorHex = req.ColorHex
	}

	// A user may rename themselves (or an admin a lower-rank user) via the management endpoint; username conflicts are rejected explicitly.
	if req.Username != "" && req.Username != user.Username {
		if existing, errUniq := a.repo.GetUserByUsername(req.Username); errUniq == nil && existing != nil && existing.ID != id {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(ErrResponse{Error: "Username already taken"})
			return
		}
		if err := a.repo.UpdateUserUsername(id, req.Username); err != nil {
			log.Printf("[UpdateUser] username update error: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrResponse{Error: "Failed to update username: " + err.Error()})
			return
		}
		user.Username = req.Username
	}

	if req.Password != "" {
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), domain.BcryptCost)
		if err != nil {
			log.Printf("[UpdateUser] bcrypt error: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrResponse{Error: "Failed to hash password"})
			return
		}
		if err := a.repo.UpdateUserPassword(id, string(hashedPassword)); err != nil {
			log.Printf("[UpdateUser] password update error: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrResponse{Error: "Failed to update password: " + err.Error()})
			return
		}
	}

	if req.Role != "" || req.ColorHex != "" || req.RoleID != nil {
		if err := a.repo.UpdateUser(user); err != nil {
			log.Printf("[UpdateUser] user update error: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(ErrResponse{Error: "Failed to update user: " + err.Error()})
			return
		}
	}

	updatedUser, _ := a.repo.GetUserByID(id)
	if updatedUser == nil {
		updatedUser = user
	} else {
		updatedUser.PasswordHash = ""
	}

	a.auditLogger.LogFromRequest(r, user.Username, audit.ActionUpdateUser, updatedUser.Username, "Role updated to: "+updatedUser.Role)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(updatedUser)
}

func (a *API) DeleteUserHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermDeleteUsers) {
		return
	}

	vars := mux.Vars(r)
	idStr := vars["id"]
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrResponse{Error: "Invalid user ID: " + idStr})
		return
	}

	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)

	// Nobody may delete their own account through the management endpoint.
	if actorID == id {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(ErrResponse{Error: "Cannot delete your own account"})
		return
	}

	user, err := a.repo.GetUserByID(id)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(ErrResponse{Error: "User not found"})
		return
	}

	// ROLE HIERARCHY: an actor may only delete a user whose role rank is STRICTLY LOWER than their own. This protects the owner account and any equal-or-higher-rank colleague from lower-rank deletion.
	actorUser, errAct := a.repo.GetUserByID(actorID)
	if errAct == nil && a.roleRank(user.Role) >= a.roleRank(actorUser.Role) {
		a.writeForbidden(w, "Forbidden: Cannot delete users with equal or higher role rank")
		return
	}

	if user.Role == domain.RoleOwner {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(ErrResponse{Error: "Cannot delete the Owner account"})
		return
	}

	if err := a.repo.DeleteUser(id); err != nil {
		log.Printf("[DeleteUser] error deleting user %d: %v", id, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrResponse{Error: err.Error()})
		return
	}

	actorUser = a.actor(r)
	a.auditLogger.LogFromRequest(r, a.actorName(actorUser), audit.ActionDeleteUser, user.Username, "User deleted")

	w.WriteHeader(http.StatusNoContent)
}

func (a *API) ResetUserPasswordHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermEditUsers) {
		return
	}

	vars := mux.Vars(r)
	idStr := vars["id"]
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)

	// Nobody may reset their own password through the management endpoint.
	if actorID == id {
		a.writeForbidden(w, "Forbidden: Cannot modify your own account through this endpoint")
		return
	}

	actorUser, err := a.repo.GetUserByID(actorID)
	if err != nil {
		a.writeForbidden(w, "Forbidden: User not found")
		return
	}

	targetUser, err := a.repo.GetUserByID(id)
	if err != nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	// ROLE HIERARCHY: password resets count as modification. An actor may only reset passwords of users ranking strictly lower than themselves.
	if a.roleRank(targetUser.Role) >= a.roleRank(actorUser.Role) {
		a.writeForbidden(w, "Forbidden: Cannot modify users with equal or higher role rank")
		return
	}

	var req ResetPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	if req.NewPassword == "" {
		http.Error(w, "Bad Request: new_password is required", http.StatusBadRequest)
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), domain.BcryptCost)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if err := a.repo.UpdateUserPassword(id, string(hashedPassword)); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	targetName := idStr
	if targetUser != nil {
		targetName = targetUser.Username
	}
	a.auditLogger.LogFromRequest(r, a.actorName(actorUser), audit.ActionUpdatePassword, targetName, "Password reset by "+a.actorRole(actorUser))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// --- Custom Roles Handlers ---

func (a *API) CreateCustomRoleHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermManageRoles) {
		return
	}

	var req CustomRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request: Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		http.Error(w, "Bad Request: name is required", http.StatusBadRequest)
		return
	}

	// ROLE RANK ENFORCEMENT: the actor may only create roles ranked STRICTLY LOWER than their own role. A custom role with a rank >= actor rank could be granted to peers, breaking the hierarchy.
	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)
	actorUser, err := a.repo.GetUserByID(actorID)
	if err != nil {
		a.writeForbidden(w, "Forbidden: User not found")
		return
	}
	actorRank := a.roleRank(actorUser.Role)

	if req.Rank < 1 {
		req.Rank = domain.RoleRankViewer
	}
	if req.Rank >= 100 || req.Rank >= actorRank {
		a.writeForbidden(w, "Forbidden: Role rank must be lower than your current rank ("+strconv.Itoa(actorRank)+")")
		return
	}

	// Escalation guard: an actor may never grant permissions they do not hold themselves (owner/admin bypass via enforcePermission).
	if !a.rolePermissionsAllowed(w, r, req.PermissionsJSON) {
		return
	}

	if req.ColorHex == "" {
		req.ColorHex = "#6B7280"
	}
	if req.PermissionsJSON == "" {
		req.PermissionsJSON = "[]"
	}

	role := &domain.CustomRole{
		Name:            req.Name,
		ColorHex:        req.ColorHex,
		OwnerID:         strconv.FormatInt(actorID, 10),
		PermissionsJSON: req.PermissionsJSON,
		Rank:            req.Rank,
		CreatedAt:       time.Now(),
	}

	id, err := a.repo.AddCustomRole(role)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "23505") {
			http.Error(w, "Conflict: Role with this name already exists", http.StatusConflict)
			return
		}
		log.Printf("Error creating role: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	role.ID = id

	a.auditLogger.LogFromRequest(r, a.actorName(actorUser), audit.ActionCreateUser, role.Name, "Created custom role")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(role)
}

func (a *API) GetCustomRolesHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermViewRoles) {
		return
	}

	roles, err := a.repo.GetAllRoles()
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(roles)
}

func (a *API) GetRolesHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermViewRoles) {
		return
	}

	roles, err := a.repo.GetAllRoles()
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		log.Printf("Error getting all roles: %v", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(roles)
}

func (a *API) UpdateCustomRoleHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermManageRoles) {
		return
	}

	vars := mux.Vars(r)
	idStr := vars["id"]
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid custom role ID", http.StatusBadRequest)
		return
	}

	existing, err := a.repo.GetRoleByID(id)
	if err != nil {
		http.Error(w, "Role not found", http.StatusNotFound)
		return
	}

	var req UpdateCustomRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request: Invalid JSON", http.StatusBadRequest)
		return
	}

	// ROLE RANK ENFORCEMENT: - The owner role (rank 100) is completely immutable: nobody may re-rank or otherwise modify it. - An actor may only modify roles ranked STRICTLY LOWER than their own.
	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)
	actorUser, err := a.repo.GetUserByID(actorID)
	if err != nil {
		a.writeForbidden(w, "Forbidden: User not found")
		return
	}
	actorRank := a.roleRank(actorUser.Role)

	existingRank := existing.Rank
	if existingRank < 1 {
		existingRank = a.roleRank(existing.Name)
	}

	if existing.Name == domain.RoleOwner || existing.Rank >= domain.RoleRankOwner {
		a.writeForbidden(w, "Forbidden: The owner role is immutable and cannot be modified")
		return
	}
	if existingRank >= actorRank {
		a.writeForbidden(w, "Forbidden: Cannot modify a role with an equal or higher rank than yours")
		return
	}
	if req.Rank >= 100 || req.Rank >= actorRank {
		a.writeForbidden(w, "Forbidden: Role rank must be lower than your current rank ("+strconv.Itoa(actorRank)+")")
		return
	}

	// Escalation guard: an actor may never grant permissions they do not hold themselves (owner/admin bypass via enforcePermission).
	if req.PermissionsJSON != "" && !a.rolePermissionsAllowed(w, r, req.PermissionsJSON) {
		return
	}

	if req.Name != "" {
		existing.Name = req.Name
	}
	if req.ColorHex != "" {
		existing.ColorHex = req.ColorHex
	}
	if req.PermissionsJSON != "" {
		existing.PermissionsJSON = req.PermissionsJSON
	}
	if req.Rank >= 1 {
		existing.Rank = req.Rank
	}

	if err := a.repo.UpdateCustomRole(existing); err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "23505") {
			http.Error(w, "Conflict: Role with this name already exists", http.StatusConflict)
			return
		}
		log.Printf("Error updating role: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	a.auditLogger.LogFromRequest(r, a.actorName(actorUser), audit.ActionUpdateSettings, existing.Name, "Updated custom role")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(existing)
}

func (a *API) DeleteCustomRoleHandler(w http.ResponseWriter, r *http.Request) {
	if !a.enforcePermission(w, r, domain.PermManageRoles) {
		return
	}

	vars := mux.Vars(r)
	idStr := vars["id"]
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid custom role ID", http.StatusBadRequest)
		return
	}

	// Check if the role exists
	role, err := a.repo.GetRoleByID(id)
	if err != nil {
		http.Error(w, "Role not found", http.StatusNotFound)
		return
	}

	// ROLE RANK ENFORCEMENT: the owner role (rank 100) is immutable and may never be deleted. Every other role - including the built-in admin, client and viewer roles - can be deleted by an actor whose rank is STRICTLY higher, per the mathematical hierarchy.
	actorID, _ := r.Context().Value(auth.UserContextKey).(int64)
	actorUser, err := a.repo.GetUserByID(actorID)
	if err != nil {
		a.writeForbidden(w, "Forbidden: User not found")
		return
	}
	actorRank := a.roleRank(actorUser.Role)

	roleRank := role.Rank
	if roleRank < 1 {
		roleRank = a.roleRank(role.Name)
	}
	if role.Name == domain.RoleOwner || role.Rank >= domain.RoleRankOwner {
		a.writeForbidden(w, "Forbidden: The owner role is immutable and cannot be deleted")
		return
	}
	if roleRank >= actorRank {
		a.writeForbidden(w, "Forbidden: Cannot delete a role with an equal or higher rank than yours")
		return
	}

	// Prevent deletion if users are assigned to this role
	userCount, err := a.repo.CountUsersByRoleName(role.Name)
	if err == nil && userCount > 0 {
		http.Error(w, "Bad Request: Role is assigned to users", http.StatusBadRequest)
		return
	}

	if err := a.repo.DeleteCustomRole(id); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	a.auditLogger.LogFromRequest(r, a.actorName(actorUser), audit.ActionDeleteUser, role.Name, "Deleted custom role")

	w.WriteHeader(http.StatusNoContent)
}

// rolePermissionsAllowed verifies that none of the granted permissions in permissionsJSON exceed the actor's own granted permissions. Used to prevent role-creation privilege escalation: a manager may only hand out permissions they hold themselves.
func (a *API) rolePermissionsAllowed(w http.ResponseWriter, r *http.Request, permissionsJSON string) bool {
	if permissionsJSON == "" {
		return true
	}

	actorID, ok := r.Context().Value(auth.UserContextKey).(int64)
	if !ok || actorID == 0 {
		a.writeForbidden(w, "Forbidden: User not found")
		return false
	}
	actorUser, err := a.repo.GetUserByID(actorID)
	if err != nil {
		a.writeForbidden(w, "Forbidden: User not found")
		return false
	}

	// Owner and admin implicitly hold every permission.
	if actorUser.Role == domain.RoleOwner || actorUser.Role == domain.RoleAdmin {
		return true
	}

	actorPerms := a.permissionsForUser(actorUser)
	want := a.parsePermissionsJSON(permissionsJSON)
	for _, p := range want {
		if !auth.HasPermission(actorPerms, p) {
			a.writeForbidden(w, "Forbidden: Cannot grant permission you do not hold: "+p)
			return false
		}
	}
	return true
}

// toInt64 converts a value from JSON (float64, int64, string) to *int64.
func toInt64(v interface{}) *int64 {
	switch val := v.(type) {
	case float64:
		n := int64(val)
		return &n
	case int64:
		return &val
	case string:
		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return nil
		}
		return &n
	}
	return nil
}

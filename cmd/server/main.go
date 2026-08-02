package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"malaxis-fleet/internal/auth"
	"malaxis-fleet/internal/config"
	"malaxis-fleet/internal/domain"
	"malaxis-fleet/internal/repository"
	"malaxis-fleet/internal/server"

	"golang.org/x/crypto/bcrypt"
)

func main() {
	cfg := config.LoadConfig()

	// Auto-generate secrets if not provided
	secretsGenerated := false
	if cfg.FleetSecret == "" {
		cfg.FleetSecret = generateRandomHex(64)
		log.Println("🔑 Auto-generated SECRET_TOKEN (64-char hex)")
		secretsGenerated = true
	}
	if cfg.SessionSecret == "" {
		cfg.SessionSecret = generateRandomHex(64)
		log.Println("🔑 Auto-generated SESSION_SECRET (64-char hex)")
		secretsGenerated = true
	}
	if secretsGenerated {
		log.Println("ℹ️  Secrets auto-generated. Set them in .env to persist across restarts.")
	}

	// Initialize the session store
	auth.InitStore(cfg)

	repo, err := repository.NewRepository(cfg)
	if err != nil {
		log.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	if err := repo.Init(); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	// Persist auto-generated secrets to database for dynamic injection
	if secretsGenerated {
		repo.SetSetting("fleet_secret", cfg.FleetSecret)
		repo.SetSetting("session_secret", cfg.SessionSecret)
	} else {
		// Always sync from DB in case it was set previously
		dbSecret, _ := repo.GetSetting("fleet_secret")
		if dbSecret != "" {
			cfg.FleetSecret = dbSecret
		} else {
			repo.SetSetting("fleet_secret", cfg.FleetSecret)
		}
		dbSessionSecret, _ := repo.GetSetting("session_secret")
		if dbSessionSecret != "" {
			cfg.SessionSecret = dbSessionSecret
		} else {
			repo.SetSetting("session_secret", cfg.SessionSecret)
		}
	}

	// Create initial admin user if no users exist
	if err := createInitialAdmin(repo, cfg.AdminUser, cfg.AdminPass); err != nil {
		log.Fatalf("Failed to create initial admin user: %v", err)
	}

	// Seed default roles (admin/client) if roles table is empty
	if err := seedDefaultRoles(repo); err != nil {
		log.Fatalf("Failed to seed default roles: %v", err)
	}

	srv := server.NewServer(cfg, repo)

	go func() {
		if err := srv.Start(); err != nil {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// Auto-cleanup: delete nodes that have been offline for 3+ days (including
	// self-destructed "Terminated" nodes) so stale rows never pile up.
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			deleted, err := repo.DeleteOfflineNodes(3)
			if err != nil {
				log.Printf("ERROR: Auto-cleanup of stale nodes failed: %v", err)
				continue
			}
			if deleted > 0 {
				log.Printf("Auto-cleanup: removed %d stale node(s) (offline > 3 days)", deleted)
			}
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")
}

func createInitialAdmin(repo repository.Repository, username, password string) error {
	username = strings.TrimSpace(username)
	password = strings.TrimSpace(password)
	if username == "" {
		username = "admin"
	}
	if password == "" {
		password = "admin"
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	isEmpty, err := repo.IsUsersEmpty()
	if err != nil {
		return err
	}

	if isEmpty {
		log.Printf("No users found. Creating initial admin user '%s'...", username)
		user := &domain.User{
			Username:     username,
			PasswordHash: string(hashedPassword),
			Role:         domain.RoleOwner,
			CreatedAt:    time.Now(),
			ColorHex:     "#FF5733",
		}

		if _, err := repo.AddUser(user); err != nil {
			return err
		}
		log.Println("Initial admin user created successfully.")
		return nil
	}

	// Guarantee the default admin credential works 100%: on every startup,
	// sync the admin user's password to the configured default so a stale or
	// corrupted hash can never lock the dashboard out.
	admin, err := repo.GetUserByUsername(username)
	if err != nil {
		if err == sql.ErrNoRows {
			log.Printf("Admin user '%s' missing, recreating...", username)
			user := &domain.User{
				Username:     username,
				PasswordHash: string(hashedPassword),
				Role:         domain.RoleOwner,
				CreatedAt:    time.Now(),
				ColorHex:     "#FF5733",
			}
			_, err = repo.AddUser(user)
			return err
		}
		return err
	}

	if admin.PasswordHash != string(hashedPassword) {
		if err := repo.UpdateUserPassword(admin.ID, string(hashedPassword)); err != nil {
			return err
		}
		log.Printf("Admin password synced to configured default")
	}
	return nil
}

func seedDefaultRoles(repo repository.Repository) error {
	roles, err := repo.GetAllRoles()
	if err != nil {
		return err
	}

	if len(roles) > 0 {
		log.Printf("Roles table already has %d entries, skipping seed", len(roles))
		return nil
	}

	log.Println("Roles table is empty. Seeding default roles...")

	now := time.Now()

	adminRole := &domain.CustomRole{
		Name:            "admin",
		ColorHex:        "#EF4444",
		OwnerID:         "system",
		PermissionsJSON: `{"can_view_nodes":true,"can_switch_vpn":true,"can_edit_sub":true,"can_manage_users":true,"can_view_audit":true,"can_export_backups":true}`,
		CreatedAt:       now,
	}
	if _, err := repo.AddCustomRole(adminRole); err != nil {
		return err
	}
	log.Println("Seeded role: admin")

	clientRole := &domain.CustomRole{
		Name:            "client",
		ColorHex:        "#3B82F6",
		OwnerID:         "system",
		PermissionsJSON: `{"can_view_nodes":true}`,
		CreatedAt:       now,
	}
	if _, err := repo.AddCustomRole(clientRole); err != nil {
		return err
	}
	log.Println("Seeded role: client")

	return nil
}

// generateRandomHex generates a cryptographically secure random hex string of n bytes (2*n hex chars).
func generateRandomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("FATAL: Failed to generate random bytes: %v", err)
	}
	return hex.EncodeToString(b)
}

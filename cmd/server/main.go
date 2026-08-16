package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
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

	// Auto-discovery on boot: reconcile subscription_providers with the URLs
	// actually present on nodes (also runs after every sub_urls mutation).
	if err := repo.SyncProviders(); err != nil {
		log.Printf("ERROR: failed to sync subscription providers: %v", err)
	}

	// Persist auto-generated secrets to database for dynamic injection
	if secretsGenerated {
		if err := repo.SetSetting("fleet_secret", cfg.FleetSecret); err != nil {
			log.Printf("ERROR: failed to persist fleet_secret: %v", err)
		}
		if err := repo.SetSetting("session_secret", cfg.SessionSecret); err != nil {
			log.Printf("ERROR: failed to persist session_secret: %v", err)
		}
	} else {
		// Always sync from DB in case it was set previously
		dbSecret, err := repo.GetSetting("fleet_secret")
		if err != nil {
			log.Printf("ERROR: failed to read fleet_secret from DB: %v", err)
		} else if dbSecret != "" {
			cfg.FleetSecret = dbSecret
		} else if err := repo.SetSetting("fleet_secret", cfg.FleetSecret); err != nil {
			log.Printf("ERROR: failed to persist fleet_secret: %v", err)
		}
		dbSessionSecret, err := repo.GetSetting("session_secret")
		if err != nil {
			log.Printf("ERROR: failed to read session_secret from DB: %v", err)
		} else if dbSessionSecret != "" {
			cfg.SessionSecret = dbSessionSecret
		} else if err := repo.SetSetting("session_secret", cfg.SessionSecret); err != nil {
			log.Printf("ERROR: failed to persist session_secret: %v", err)
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

	// Auto-cleanup: delete nodes that have been offline for 14+ days
	// (including self-destructed "Terminated" nodes) so stale rows never pile
	// up, while long vacations (or a temporarily unreachable network) never
	// wipe a node from the fleet.
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			deleted, err := repo.DeleteOfflineNodes(14)
			if err != nil {
				log.Printf("ERROR: Auto-cleanup of stale nodes failed: %v", err)
				continue
			}
			if deleted > 0 {
				log.Printf("Auto-cleanup: removed %d stale node(s) (offline > 14 days)", deleted)
			}
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")
}

func createInitialAdmin(repo repository.Repository, username, password string) error {
	// Seed the default admin only on the FIRST boot. Once any user row
	// exists, the configured ADMIN_USER/ADMIN_PASS no longer override
	// anything: passwords changed through the UI or the bot must survive
	// restarts.
	empty, err := repo.IsUsersEmpty()
	if err != nil {
		return fmt.Errorf("failed to check users table: %w", err)
	}
	if !empty {
		log.Println("Users already exist, skipping initial admin seed")
		return nil
	}

	username = strings.TrimSpace(username)
	password = strings.TrimSpace(password)
	if username == "" {
		username = "owner"
	}
	if password == "" {
		password = "owner"
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	if _, err := repo.AddUser(&domain.User{
		Username:     username,
		PasswordHash: string(hashedPassword),
		Role:         domain.RoleOwner,
		CreatedAt:    time.Now(),
		ColorHex:     "#FF5733",
	}); err != nil {
		return fmt.Errorf("failed to seed admin user: %w", err)
	}
	log.Printf("Seeded initial admin user '%s' (owner role)", username)
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

	ownerRole := &domain.CustomRole{
		Name:            "owner",
		ColorHex:        "#FF5733",
		OwnerID:         "system",
		PermissionsJSON: `{"can_view_nodes":true,"can_switch_vpn":true,"can_edit_sub":true,"can_rename_node":true,"can_terminate_node":true,"can_update_client":true,"can_purge_nodes":true,"can_view_users":true,"can_create_users":true,"can_edit_users":true,"can_delete_users":true,"can_view_roles":true,"can_manage_roles":true,"can_view_audit":true,"can_view_node_logs":true,"can_view_master_logs":true,"can_view_audit_logs":true}`,
		Rank:            domain.RoleRankOwner,
		CreatedAt:       now,
	}
	if _, err := repo.AddCustomRole(ownerRole); err != nil {
		return err
	}
	log.Println("Seeded role: owner")

	adminRole := &domain.CustomRole{
		Name:            "admin",
		ColorHex:        "#EF4444",
		OwnerID:         "system",
		PermissionsJSON: `{"can_view_nodes":true,"can_switch_vpn":true,"can_edit_sub":true,"can_rename_node":true,"can_terminate_node":true,"can_update_client":true,"can_purge_nodes":true,"can_view_users":true,"can_create_users":true,"can_edit_users":true,"can_delete_users":true,"can_view_roles":true,"can_manage_roles":true,"can_view_audit":true,"can_view_node_logs":true,"can_view_master_logs":true,"can_view_audit_logs":true}`,
		Rank:            domain.RoleRankAdmin,
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
		Rank:            domain.RoleRankClient,
		CreatedAt:       now,
	}
	if _, err := repo.AddCustomRole(clientRole); err != nil {
		return err
	}
	log.Println("Seeded role: client")

	viewerRole := &domain.CustomRole{
		Name:            "viewer",
		ColorHex:        "#6B7280",
		OwnerID:         "system",
		PermissionsJSON: `{"can_view_nodes":true}`,
		Rank:            domain.RoleRankViewer,
		CreatedAt:       now,
	}
	if _, err := repo.AddCustomRole(viewerRole); err != nil {
		return err
	}
	log.Println("Seeded role: viewer")

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

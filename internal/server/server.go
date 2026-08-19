package server

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"malaxis-fleet/internal/api"
	"malaxis-fleet/internal/bot"
	"malaxis-fleet/internal/config"
	"malaxis-fleet/internal/repository"
	"malaxis-fleet/internal/service"

	"github.com/gorilla/mux"
)

type Server struct {
	router          *mux.Router
	config          *config.Config
	repo            repository.Repository
	autoSyncService *service.AutoSyncService
	bot             *bot.Bot
}

func NewServer(cfg *config.Config, repo repository.Repository) *Server {
	autoSyncService := service.NewAutoSyncService(repo, cfg)
	telegramBot := bot.NewBot(cfg, repo, autoSyncService)

	s := &Server{
		router:          mux.NewRouter(),
		config:          cfg,
		repo:            repo,
		autoSyncService: autoSyncService,
		bot:             telegramBot,
	}
	return s
}

// Start initializes all background services, registers routes and starts the
// HTTP server in the background. It returns the running *http.Server so the
// caller can gracefully drain active requests via Shutdown on SIGINT/SIGTERM.
func (s *Server) Start() (*http.Server, error) {
	setupMasterLogFile(s.config.MasterLogFile)

	go s.autoSyncService.Start()

	go func() {
		if err := s.bot.Start(); err != nil {
			log.Printf("Telegram bot not started: %v", err)
		}
	}()

	s.router.Use(api.PanicRecoveryMiddleware(s.bot))

	api.RegisterRoutes(s.router, s.repo, s.config, s.bot)

	log.Printf("Starting server on port %d\n", s.config.WebPort)
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", s.config.WebPort),
		Handler:      s.router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()
	return srv, nil
}

func (s *Server) RebootBot() error {
	log.Println("Rebooting Telegram bot with new settings...")
	return s.bot.Reboot()
}

// setupMasterLogFile tees Go's standard logger into a file so the "Logs & Audit" tab can show the master's own logs.
func setupMasterLogFile(path string) {
	if path == "" {
		path = "data/logs/master.log"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Printf("WARN: cannot create log dir: %v", err)
		return
	}
	rotateMasterLogIfLarge(path)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Printf("WARN: cannot open master log file: %v", err)
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	log.Printf("Master logs are being written to %s", path)
}

// rotateMasterLogIfLarge performs a simple rotation at startup: when the log file has grown beyond 50 MB, the current file is renamed to master.log.1 and a fresh file is started, so the file can never grow without bound.
func rotateMasterLogIfLarge(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if info.Size() < 50*1024*1024 {
		return
	}
	rotated := path + ".1"
	if err := os.Rename(path, rotated); err != nil {
		log.Printf("WARN: failed to rotate master log: %v", err)
		return
	}
	log.Printf("Master log exceeded 50 MB, rotated to %s", rotated)
}

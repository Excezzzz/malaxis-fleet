package server

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"

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

func (s *Server) Start() error {
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
	return http.ListenAndServe(fmt.Sprintf(":%d", s.config.WebPort), s.router)
}

func (s *Server) RebootBot() error {
	log.Println("Rebooting Telegram bot with new settings...")
	return s.bot.Reboot()
}

// setupMasterLogFile tees Go's standard logger into a file so the
// "Logs & Audit" tab can show the master's own logs.
func setupMasterLogFile(path string) {
	if path == "" {
		path = "data/logs/master.log"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Printf("WARN: cannot create log dir: %v", err)
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Printf("WARN: cannot open master log file: %v", err)
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	log.Printf("Master logs are being written to %s", path)
}

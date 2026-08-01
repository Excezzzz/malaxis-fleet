package server

import (
	"fmt"
	"log"
	"net/http"

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

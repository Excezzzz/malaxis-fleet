package service

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"malaxis-fleet/internal/config"
	"malaxis-fleet/internal/domain"
	"malaxis-fleet/internal/repository"
)

type AutoSyncService struct {
	repo         repository.Repository
	syncInterval time.Duration
	ticker       *time.Ticker
	stop         chan bool
}

func NewAutoSyncService(repo repository.Repository, cfg *config.Config) *AutoSyncService {
	intervalStr, err := repo.GetSetting("autosync_interval_minutes")
	if err != nil {
		log.Printf("Could not get autosync interval from settings, will use default: %v", err)
	}

	interval, err := strconv.Atoi(intervalStr)
	if err != nil || interval == 0 {
		interval = 60 // Default to 60 minutes
	}

	return &AutoSyncService{
		repo:         repo,
		syncInterval: time.Duration(interval) * time.Minute,
		stop:         make(chan bool),
	}
}

func (s *AutoSyncService) Start() {
	if s.syncInterval <= 0 {
		return
	}
	s.ticker = time.NewTicker(s.syncInterval)
	go func() {
		for {
			select {
			case <-s.ticker.C:
				log.Println("Running auto-sync for subscriptions")
				s.runSync()
			case <-s.stop:
				s.ticker.Stop()
				return
			}
		}
	}()
}

func (s *AutoSyncService) Stop() {
	if s.ticker != nil {
		s.stop <- true
	}
}

func (s *AutoSyncService) SetSyncInterval(minutes int) error {
	s.Stop()
	s.syncInterval = time.Duration(minutes) * time.Minute
	err := s.repo.SetSetting("autosync_interval_minutes", strconv.Itoa(minutes))
	if err != nil {
		return fmt.Errorf("failed to save new sync interval: %w", err)
	}
	s.Start()
	return nil
}

func (s *AutoSyncService) runSync() {
	nodes, err := s.repo.GetNodesWithSubURL()
	if err != nil {
		log.Printf("Error getting nodes for auto-sync: %v", err)
		return
	}

	for _, n := range nodes {
		s.syncNode(&n)
	}
}

func (s *AutoSyncService) RunSync() {
	log.Println("Manual auto-sync triggered")
	s.runSync()
}

func (s *AutoSyncService) syncNode(n *domain.Node) {
	log.Printf("Syncing subscription for %s", n.Name)
	outbounds, err := fetchSubOutbounds(n.SubURL)
	if err != nil {
		log.Printf("Error fetching subscription for %s: %v", n.Name, err)
		return
	}

	for _, o := range outbounds {
		if o.Tag == n.ActiveServer {
			newJSON, err := CanonicalJSON(o)
			if err != nil {
				log.Printf("Error getting canonical JSON for new outbound: %v", err)
				continue
			}

			if n.ActiveOutboundJSON != newJSON {
				log.Printf("Configuration change detected for %s. Queuing update.", n.Name)
				cmd := domain.Command{
					Action:   "switch",
					Outbound: o,
				}
				cmdJSON, err := json.Marshal(cmd)
				if err != nil {
					log.Printf("Error marshaling command: %v", err)
					continue
				}
				// The message ID is 0 because this is not initiated by a Telegram message
				if err := s.repo.SetPendingCommand(n.ID, string(cmdJSON), 0); err != nil {
					log.Printf("Error setting pending command: %v", err)
					continue
				}
			}
			break
		}
	}
}

func fetchSubOutbounds(subURL string) ([]domain.Outbound, error) {
	resp, err := http.Get(subURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	decoded, err := base64.StdEncoding.DecodeString(string(body))
	if err != nil {
		// If base64 decoding fails, assume it's plain text
		decoded = body
	}

	lines := strings.Split(string(decoded), "\n")
	var outbounds []domain.Outbound
	for _, line := range lines {
		outbound, err := parseLink(line)
		if err == nil && outbound != nil {
			outbounds = append(outbounds, *outbound)
		}
	}
	return outbounds, nil
}

func parseLink(link string) (*domain.Outbound, error) {
	if !strings.Contains(link, "://") {
		return nil, fmt.Errorf("invalid link format")
	}
	parsed, err := url.Parse(link)
	if err != nil {
		return nil, err
	}

	var outbound domain.Outbound
	outbound.Tag = parsed.Fragment
	if outbound.Tag == "" {
		outbound.Tag = "Default"
	}
	outbound.Type = parsed.Scheme

	switch parsed.Scheme {
	case "vless":
		outbound.Engine = "xray"
		outbound.PrettyProto = "VLESS"
		rawParams := make(map[string]interface{})
		rawParams["uuid"] = parsed.User.Username()
		rawParams["server"] = parsed.Hostname()
		port, _ := strconv.Atoi(parsed.Port())
		rawParams["port"] = port
		rawParams["params"] = parsed.Query()
		outbound.RawParams = rawParams
	case "hysteria2":
		outbound.Engine = "singbox"
		outbound.PrettyProto = "Hysteria2"
		rawParams := make(map[string]interface{})
		rawParams["password"] = parsed.User.Username()
		rawParams["server"] = parsed.Hostname()
		port, _ := strconv.Atoi(parsed.Port())
		rawParams["port"] = port
		rawParams["params"] = parsed.Query()
		outbound.RawParams = rawParams
	case "tuic":
		outbound.Engine = "singbox"
		outbound.PrettyProto = "TUIC"
		rawParams := make(map[string]interface{})
		rawParams["uuid"] = parsed.User.Username()
		password, _ := parsed.User.Password()
		rawParams["password"] = password
		rawParams["server"] = parsed.Hostname()
		port, _ := strconv.Atoi(parsed.Port())
		rawParams["port"] = port
		rawParams["params"] = parsed.Query()
		outbound.RawParams = rawParams
	case "wireguard":
		outbound.Engine = "singbox"
		outbound.PrettyProto = "WireGuard"
	case "vmess":
		outbound.Engine = "xray"
		outbound.PrettyProto = "VMess"
	case "trojan":
		outbound.Engine = "xray"
		outbound.PrettyProto = "Trojan"
	default:
		outbound.Engine = "singbox"
		outbound.PrettyProto = strings.ToUpper(parsed.Scheme)
	}

	return &outbound, nil
}

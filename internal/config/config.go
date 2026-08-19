package config

import (
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds all configuration for the application
type Config struct {
	// User-facing secrets & IDs
	BotToken      string
	AdminChatID   int64
	AdminUser     string
	AdminPass     string
	FleetSecret   string
	SessionSecret string

	// Subdomain Architecture
	DashboardDomain string
	ApiDomain       string
	JoinDomain      string
	SubDomain       string
	DashboardURL    string
	ApiURL          string
	JoinURL         string
	SubURL          string

	// SSL/TLS Certificates
	SSLCertPath string
	SSLKeyPath  string

	// Internal server config
	WebPort int

	// Database configuration (loaded from Docker Compose, not .env)
	PostgresHost     string
	PostgresPort     string
	PostgresUser     string
	PostgresPassword string
	PostgresDB       string

	// Features
	MaxBackupRetention int
	LoginRateLimit     int
	LowRAMMode         bool
	OwnerRoleName      string
	OwnerColorHex      string

	// Master log file path (used by the "Logs & Audit" tab)
	MasterLogFile string
}

// LoadConfig loads configuration from environment variables (.env file)
func LoadConfig() *Config {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	return &Config{
		// User-facing secrets & IDs from .env
		BotToken:      getStringDefault("BOT_TOKEN", ""),
		AdminChatID:   getInt64Default("ADMIN_CHAT_ID", 0),
		AdminUser:     getStringDefault("ADMIN_USER", "owner"),
		AdminPass:     getStringDefault("ADMIN_PASS", "owner"),
		FleetSecret:   getStringDefault("SECRET_TOKEN", ""),
		SessionSecret: getStringDefault("SESSION_SECRET", ""),

		// Subdomain Architecture from .env
		DashboardDomain: getStringDefault("DASHBOARD_DOMAIN", "dash.yourdomain.com"),
		ApiDomain:       getStringDefault("API_DOMAIN", "api.yourdomain.com"),
		JoinDomain:      getStringDefault("JOIN_DOMAIN", "join.yourdomain.com"),
		SubDomain:       getStringDefault("SUB_DOMAIN", "sub.yourdomain.com"),
		DashboardURL:    getStringDefault("DASHBOARD_URL", "https://dash.yourdomain.com"),
		ApiURL:          getStringDefault("API_URL", "https://api.yourdomain.com"),
		JoinURL:         getStringDefault("JOIN_URL", "https://join.yourdomain.com"),
		SubURL:          getStringDefault("SUB_URL", "https://sub.yourdomain.com"),

		// SSL/TLS Certificates
		SSLCertPath: getStringDefault("SSL_CERT_PATH", "/cf-certs/cf.crt"),
		SSLKeyPath:  getStringDefault("SSL_KEY_PATH", "/cf-certs/cf.key"),

		// Internal server config
		WebPort: getIntDefault("WEB_PORT", 8000),

		// Database configuration (defaults for docker-compose)
		PostgresHost:     getStringDefault("POSTGRES_HOST", "postgres"),
		PostgresPort:     getStringDefault("POSTGRES_PORT", "5432"),
		PostgresUser:     getStringDefault("POSTGRES_USER", "fleet_internal"),
		PostgresPassword: getStringDefault("POSTGRES_PASSWORD", ""),
		PostgresDB:       getStringDefault("POSTGRES_DB", "fleet_db"),

		// Features
		MaxBackupRetention: getIntDefault("MAX_BACKUP_RETENTION", 3),
		LoginRateLimit:     getIntDefault("LOGIN_RATE_LIMIT", 30),
		LowRAMMode:         getBoolDefault("LOW_RAM_MODE", false),
		OwnerRoleName:      getStringDefault("OWNER_ROLE_NAME", "Owner"),
		OwnerColorHex:      getStringDefault("OWNER_COLOR_HEX", "#FF5733"),
		MasterLogFile:      getStringDefault("MASTER_LOG_FILE", "data/logs/master.log"),
	}
}

func getString(key string) string {
	value, ok := os.LookupEnv(key)
	if !ok {
		log.Fatalf("FATAL: Environment variable %s is not set", key)
	}
	return value
}

func getStringDefault(key string, defaultValue string) string {
	value, ok := os.LookupEnv(key)
	if !ok {
		return defaultValue
	}
	value = strings.TrimSpace(value)
	// Unwrap surrounding quotes ONLY when the value is fully wrapped in matching quotes. This preserves passwords containing special characters (backticks, quotes, etc.) while still handling the common ADMIN_PASS="admin" form used in .env files. A value that merely *contains* a quote is left untouched.
	if len(value) >= 2 {
		first, last := value[0], value[len(value)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			value = value[1 : len(value)-1]
		}
	}
	return value
}

func getInt(key string) int {
	valueStr := getString(key)
	value, err := strconv.Atoi(valueStr)
	if err != nil {
		log.Fatalf("FATAL: Invalid value for %s: %v", key, err)
	}
	return value
}

func getIntDefault(key string, defaultValue int) int {
	valueStr, ok := os.LookupEnv(key)
	if !ok {
		return defaultValue
	}
	value, err := strconv.Atoi(valueStr)
	if err != nil {
		log.Println("Invalid integer value, using default")
		return defaultValue
	}
	return value
}

func getInt64(key string) int64 {
	valueStr := getString(key)
	value, err := strconv.ParseInt(valueStr, 10, 64)
	if err != nil {
		log.Fatalf("FATAL: Invalid value for %s: %v", key, err)
	}
	return value
}

func getInt64Default(key string, defaultValue int64) int64 {
	valueStr, ok := os.LookupEnv(key)
	if !ok {
		return defaultValue
	}
	value, err := strconv.ParseInt(valueStr, 10, 64)
	if err != nil {
		return defaultValue
	}
	return value
}

func getBoolDefault(key string, defaultValue bool) bool {
	valueStr, ok := os.LookupEnv(key)
	if !ok {
		return defaultValue
	}
	value, err := strconv.ParseBool(valueStr)
	if err != nil {
		return defaultValue
	}
	return value
}

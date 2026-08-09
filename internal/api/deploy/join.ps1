# ============================================================
#  Malaxis Fleet - Interactive Client Installer (Windows)
#  Native Windows PowerShell (5.1+).
#  Run with:  irm https://<join-domain>/join.ps1?t=<SECRET_TOKEN> | iex
# ============================================================

$ErrorActionPreference = "Stop"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

function Say  { Write-Host "[+] $args" -ForegroundColor Green }
function Warn { Write-Host "[!] $args" -ForegroundColor Yellow }
function Fail { Write-Host "[x] $args" -ForegroundColor Red; exit 1 }

# ------------------------------------------------------------
# 0. Language selector (first step, before any other output)
# ------------------------------------------------------------
Write-Host ""
Write-Host "Select installer language / Выберите язык установки:"
Write-Host "  [1] Русский (По умолчанию / Default)"
Write-Host "  [2] English"
$langChoice = Read-Host ">"
if ([string]::IsNullOrWhiteSpace($langChoice)) { $langChoice = "1" }

$lang = "ru"
if ($langChoice -eq "2" -or $langChoice -eq "en" -or $langChoice -eq "EN") { $lang = "en" }

# Values needed by localized prompts (computed before strings)
$docs = [Environment]::GetFolderPath('MyDocuments')
$desk = [Environment]::GetFolderPath('Desktop')
if ([string]::IsNullOrWhiteSpace($docs)) { $docs = $HOME }
if ([string]::IsNullOrWhiteSpace($desk)) { $desk = $HOME }
$hostName = $env:COMPUTERNAME
if ([string]::IsNullOrWhiteSpace($hostName)) { $hostName = "fleet-node" }

# ------------------------------------------------------------
# Localized strings
# ------------------------------------------------------------
if ($lang -eq "en") {
    $T_PREFLIGHT            = "Running pre-flight checks..."
    $T_DOCKER_NOT_INSTALLED = "Docker is not installed! Install Docker Desktop (https://www.docker.com/products/docker-desktop), then re-run this script."
    $T_DOCKER_INSTALLED     = "Docker is installed."
    $T_DOCKER_NOT_RUNNING   = "Docker is not running! Please start Docker Desktop or the Docker daemon before installing."
    $T_DOCKER_RUNNING       = "Docker daemon is running."
    $T_COMPOSE_MISSING      = "Docker Compose plugin is not available! Enable the 'docker compose' v2 plugin in Docker Desktop, then re-run this script."
    $T_COMPOSE_OK           = "Docker Compose plugin is available."
    $T_CHECK_MASTER         = "Checking master server connectivity (__API_DOMAIN__)..."
    $T_MASTER_OK            = "Master server is reachable."
    $T_MASTER_WARN          = "Master server did not respond. Check your network/firewall; the agent will retry automatically once started."
    $T_DIR_TITLE            = "Select installation directory for Malaxis Fleet Client:"
    $T_DIR_1                = "[1] Documents (Default: $docs\malaxis-fleet-client)"
    $T_DIR_2                = "[2] Desktop ($desk\malaxis-fleet-client)"
    $T_DIR_3                = "[3] User Home Directory ($HOME\malaxis-fleet-client)"
    $T_DIR_4                = "[4] Custom Path"
    $T_DIR_PROMPT           = "Select [1-4, default 1]"
    $T_CUSTOM_PATH          = "Enter custom installation path"
    $T_PATH_EMPTY           = "Custom path cannot be empty."
    $T_INSTALL_DIR          = "Installation directory: "
    $T_NODE_PROMPT          = "Enter a friendly name for this device [Default: $hostName]"
    $T_SUB_PROMPT           = "Enter your 3x-ui Subscription URL (Press Enter to skip)"
    $T_MODE_TITLE           = "Select default Smart Routing Mode:"
    $T_MODE_1               = "[1] Balanced - Best stability & lowest jitter (Recommended)"
    $T_MODE_2               = "[2] Fastest - Lowest ping"
    $T_MODE_3               = "[3] Manual"
    $T_MODE_PROMPT          = "Select [1-3, default 1]"
    $T_REINSTALL            = "Existing installation detected. Performing clean re-install..."
    $T_PRESERVE             = "Preserving existing configs directory..."
    $T_OLD_CLEANED          = "Old installation cleaned."
    $T_DL_FILES             = "Downloading client files..."
    $T_DL_FILE              = "Downloading "
    $T_CONFIGS_RESTORED     = "Previous configs restored."
    $T_DL_CONFIGS           = "Downloading default proxy configs..."
    $T_DL_CLI               = "Downloading fleet-cli utility..."
    $T_STATE_WRITTEN        = "Configuration written to configs/agent_state.json (node: {0}, smart mode: {1})."
    $T_BUILD                = "Building agent image and starting services..."
    $T_COMPOSE_FAILED       = "docker compose up failed. Check Docker Desktop settings and re-run this script."
    $T_PREP_SING            = "Preparing singbox-node container..."
    $T_DONE                 = "Malaxis Fleet Agent is running!"
    $T_QUICK                = "Quick commands:"
    $T_Q_LOGS               = "View logs:"
    $T_Q_STOP               = "Stop agent:"
    $T_Q_CLI                = "CLI (Git Bash):"
} else {
    $T_PREFLIGHT            = "Проверка системных требований..."
    $T_DOCKER_NOT_INSTALLED = "Docker не установлен! Установите Docker Desktop (https://www.docker.com/products/docker-desktop), затем запустите скрипт заново."
    $T_DOCKER_INSTALLED     = "Docker установлен."
    $T_DOCKER_NOT_RUNNING   = "Docker не запущен! Пожалуйста, запустите Docker Desktop или службу Docker перед установкой."
    $T_DOCKER_RUNNING       = "Docker запущен."
    $T_COMPOSE_MISSING      = "Плагин Docker Compose недоступен! Включите плагин 'docker compose' v2 в Docker Desktop, затем запустите скрипт заново."
    $T_COMPOSE_OK           = "Плагин Docker Compose доступен."
    $T_CHECK_MASTER         = "Проверка связи с мастер-сервером (__API_DOMAIN__)..."
    $T_MASTER_OK            = "Мастер-сервер доступен."
    $T_MASTER_WARN          = "Мастер-сервер не ответил. Проверьте сеть/файрвол; агент автоматически повторит попытку после запуска."
    $T_DIR_TITLE            = "Выберите папку для установки Malaxis Fleet Client:"
    $T_DIR_1                = "[1] Документы (По умолчанию: $docs\malaxis-fleet-client)"
    $T_DIR_2                = "[2] Рабочий стол ($desk\malaxis-fleet-client)"
    $T_DIR_3                = "[3] Домашняя папка ($HOME\malaxis-fleet-client)"
    $T_DIR_4                = "[4] Ввести свой путь"
    $T_DIR_PROMPT           = "Выберите [1-4, по умолчанию 1]"
    $T_CUSTOM_PATH          = "Введите путь установки"
    $T_PATH_EMPTY           = "Путь не может быть пустым."
    $T_INSTALL_DIR          = "Папка установки: "
    $T_NODE_PROMPT          = "Введите имя устройства [По умолчанию: $hostName]"
    $T_SUB_PROMPT           = "Введите ссылку подписки 3x-ui (Enter — пропустить)"
    $T_MODE_TITLE           = "Режим балансировки по умолчанию:"
    $T_MODE_1               = "[1] Балансировка — лучшая стабильность и минимальный джиттер (Рекомендуется)"
    $T_MODE_2               = "[2] Самый быстрый — минимальный пинг"
    $T_MODE_3               = "[3] Вручную"
    $T_MODE_PROMPT          = "Выберите [1-3, по умолчанию 1]"
    $T_REINSTALL            = "Обнаружена существующая установка. Выполняется чистая переустановка..."
    $T_PRESERVE             = "Сохраняю существующие конфигурации..."
    $T_OLD_CLEANED          = "Старая установка удалена."
    $T_DL_FILES             = "Загрузка файлов клиента..."
    $T_DL_FILE              = "Загрузка "
    $T_CONFIGS_RESTORED     = "Предыдущие конфигурации восстановлены."
    $T_DL_CONFIGS           = "Загрузка стандартных конфигураций прокси..."
    $T_DL_CLI               = "Загрузка утилиты fleet-cli..."
    $T_STATE_WRITTEN        = "Конфигурация сохранена в configs/agent_state.json (узел: {0}, режим: {1})."
    $T_BUILD                = "Сборка образа агента и запуск сервисов..."
    $T_COMPOSE_FAILED       = "Не удалось выполнить docker compose up. Проверьте настройки Docker Desktop и запустите скрипт заново."
    $T_PREP_SING            = "Подготовка контейнера singbox-node..."
    $T_DONE                 = "Malaxis Fleet Agent запущен!"
    $T_QUICK                = "Быстрые команды:"
    $T_Q_LOGS               = "Логи:"
    $T_Q_STOP               = "Остановить:"
    $T_Q_CLI                = "CLI (Git Bash):"
}

Write-Host ""
Write-Host "================================================" -ForegroundColor Cyan
Write-Host "     Malaxis Fleet - Client Installer" -ForegroundColor Cyan
Write-Host "     Native Windows PowerShell" -ForegroundColor Cyan
Write-Host "================================================" -ForegroundColor Cyan
Write-Host ""

# ------------------------------------------------------------
# 1. Pre-flight dependency & resource checks
# ------------------------------------------------------------
Say $T_PREFLIGHT

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    Fail $T_DOCKER_NOT_INSTALLED
}
Say $T_DOCKER_INSTALLED

& docker info 2>$null | Out-Null
if ($LASTEXITCODE -ne 0) {
    Fail $T_DOCKER_NOT_RUNNING
}
Say $T_DOCKER_RUNNING

& docker compose version 2>$null | Out-Null
if ($LASTEXITCODE -ne 0) {
    Fail $T_COMPOSE_MISSING
}
Say $T_COMPOSE_OK

Say $T_CHECK_MASTER
$reachable = Test-NetConnection -ComputerName "__API_DOMAIN__" -Port 443 -InformationLevel Quiet -WarningAction SilentlyContinue
if ($reachable) {
    Say $T_MASTER_OK
} else {
    Warn $T_MASTER_WARN
}

# ------------------------------------------------------------
# 2. Interactive installation directory selector
# ------------------------------------------------------------
Write-Host ""
Write-Host $T_DIR_TITLE
Write-Host $T_DIR_1
Write-Host $T_DIR_2
Write-Host $T_DIR_3
Write-Host $T_DIR_4
$dirChoice = Read-Host $T_DIR_PROMPT
if ([string]::IsNullOrWhiteSpace($dirChoice)) { $dirChoice = "1" }

$baseDir = $docs
switch ($dirChoice) {
    "2" { $baseDir = $desk }
    "3" { $baseDir = $HOME }
    "4" {
        $customDir = Read-Host $T_CUSTOM_PATH
        if (-not [string]::IsNullOrWhiteSpace($customDir)) { $baseDir = $customDir }
    }
}
if ([string]::IsNullOrWhiteSpace($baseDir)) { $baseDir = $HOME }
$installDir = Join-Path $baseDir "malaxis-fleet-client"
New-Item -ItemType Directory -Force -Path (Join-Path $installDir "configs") | Out-Null
Say "$T_INSTALL_DIR$installDir"

# ------------------------------------------------------------
# 3. Interactive setup (onboarding prompts)
# ------------------------------------------------------------
$nodeName = Read-Host $T_NODE_PROMPT
if ([string]::IsNullOrWhiteSpace($nodeName)) { $nodeName = $hostName }

$subUrl = Read-Host $T_SUB_PROMPT
$subUrl = $subUrl.Trim()

Write-Host ""
Write-Host $T_MODE_TITLE
Write-Host $T_MODE_1
Write-Host $T_MODE_2
Write-Host $T_MODE_3
$modeChoice = Read-Host $T_MODE_PROMPT
$smartMode = "balanced"
switch ($modeChoice) {
    "2" { $smartMode = "fastest" }
    "3" { $smartMode = "manual" }
}

# ------------------------------------------------------------
# 4. Clean re-install detection
# ------------------------------------------------------------
$existing = $false
if (Test-Path (Join-Path $installDir "docker-compose.yml")) { $existing = $true }
$containerNames = (& docker ps --format "{{.Names}}" 2>$null) -join "`n"
if ($containerNames -match "node-agent") { $existing = $true }

if ($existing) {
    Write-Host ""
    Say $T_REINSTALL
    if (Test-Path $installDir) {
        Push-Location $installDir
        & docker compose down --remove-orphans 2>$null | Out-Null
        & docker rm -f node-agent xray-node singbox-node 2>$null | Out-Null
        Pop-Location
    }
    if (Test-Path (Join-Path $installDir "configs")) {
        Say $T_PRESERVE
        $backup = Join-Path $env:TEMP "fleet-config-backup"
        if (Test-Path $backup) { Remove-Item -Recurse -Force $backup }
        Copy-Item -Recurse -Force (Join-Path $installDir "configs") $backup
    }
    if (Test-Path $installDir) { Remove-Item -Recurse -Force $installDir }
    Say $T_OLD_CLEANED
}
New-Item -ItemType Directory -Force -Path (Join-Path $installDir "configs") | Out-Null

# ------------------------------------------------------------
# 5. Download client payloads
# ------------------------------------------------------------
$subBase = "https://__SUB_DOMAIN__"
$apiBase = "https://__API_DOMAIN__"
$joinBase = "https://__JOIN_DOMAIN__"
$token = "__SECRET_TOKEN__"

function Download-File([string]$Url, [string]$Dest) {
    Write-Host "$T_DL_FILE$(Split-Path $Dest -Leaf)..."
    Invoke-WebRequest -UseBasicParsing -Uri $Url -OutFile $Dest
}

Write-Host ""
Say $T_DL_FILES
Download-File "$subBase/docker-compose.yml?t=$token" (Join-Path $installDir "docker-compose.yml")
Download-File "$subBase/Dockerfile.client?t=$token" (Join-Path $installDir "Dockerfile")
Download-File "$subBase/requirements.txt?t=$token" (Join-Path $installDir "requirements.txt")
Download-File "$subBase/entrypoint.sh?t=$token" (Join-Path $installDir "entrypoint.sh")
Download-File "$apiBase/api/agent/latest?t=$token" (Join-Path $installDir "node_agent.py")

# Restore configs if backed up
$backup = Join-Path $env:TEMP "fleet-config-backup"
if (Test-Path $backup) {
    Copy-Item -Recurse -Force (Join-Path $backup "*") (Join-Path $installDir "configs\") -ErrorAction SilentlyContinue
    Remove-Item -Recurse -Force $backup
    Say $T_CONFIGS_RESTORED
}

# Download default configs so containers start cleanly
Write-Host ""
Say $T_DL_CONFIGS
try {
    Download-File "$subBase/configs/xray_config.json?t=$token" (Join-Path $installDir "configs\xray_config.json")
} catch {
    Set-Content -Path (Join-Path $installDir "configs\xray_config.json") -Value @'
{
  "log": { "loglevel": "warning" },
  "dns": { "servers": ["https://dns.google/dns-query", "https://cloudflare-dns.com/dns-query", "8.8.8.8", "1.1.1.1"], "queryStrategy": "UseIPv4" },
  "inbounds": [
    { "port": 6357, "listen": "0.0.0.0", "protocol": "socks", "settings": { "auth": "noauth", "udp": true }, "sniffing": { "enabled": true, "destOverride": ["http", "tls", "quic"], "routeOnly": true }, "tag": "socks-in", "sockopt": { "tcpNoDelay": true, "tcpKeepAliveInterval": 15, "tcpKeepAliveIdle": 15 } },
    { "port": 6358, "listen": "0.0.0.0", "protocol": "http", "settings": { "timeout": 0 }, "tag": "http-in", "sockopt": { "tcpNoDelay": true, "tcpKeepAliveInterval": 15, "tcpKeepAliveIdle": 15 } }
  ],
  "outbounds": [ { "protocol": "freedom", "tag": "direct" } ],
  "routing": {
    "domainStrategy": "IPIfNonMatch",
    "rules": [
      { "type": "field", "port": 53, "network": "udp", "outboundTag": "direct" },
      { "type": "field", "ip": ["91.108.0.0/16", "149.154.160.0/20", "185.76.151.0/24"], "outboundTag": "direct" }
    ]
  }
}
'@ -Encoding UTF8
}
try {
    Download-File "$subBase/configs/singbox_config.json?t=$token" (Join-Path $installDir "configs\singbox_config.json")
} catch {
    Set-Content -Path (Join-Path $installDir "configs\singbox_config.json") -Value @'
{
  "log": { "level": "warn" },
  "dns": {
    "servers": [
      { "tag": "resolver", "address": "https://1.1.1.1/dns-query", "detour": "direct", "strategy": "ipv4_only" },
      { "tag": "block", "address": "rcode://success" }
    ],
    "final": "resolver",
    "independent_cache": true
  },
  "inbounds": [
    { "type": "socks", "tag": "socks-in", "listen": "0.0.0.0", "listen_port": 6357, "udp": true, "users": [], "sniff": { "enabled": true, "override_destination": false, "route_only": true } },
    { "type": "http", "tag": "http-in", "listen": "0.0.0.0", "listen_port": 6358, "users": [], "sniff": { "enabled": true, "override_destination": true, "route_only": true } }
  ],
  "route": { "final": "direct", "auto_detect_interface": true },
  "outbounds": [ { "type": "direct", "tag": "direct" } ]
}
'@ -Encoding UTF8
}

# Download fleet-cli utility
Write-Host ""
Say $T_DL_CLI
Download-File "$joinBase/fleet-cli?t=$token" (Join-Path $installDir "fleet-cli.sh")

# ------------------------------------------------------------
# 6. Persist onboarding choices BEFORE starting the stack
# ------------------------------------------------------------
$state = @{
    sub_url     = $subUrl
    node_name   = $nodeName
    active_mode = $smartMode
} | ConvertTo-Json -Compress
Set-Content -Path (Join-Path $installDir "configs\agent_state.json") -Value $state -Encoding UTF8
Say ($T_STATE_WRITTEN -f $nodeName, $smartMode)

# ------------------------------------------------------------
# 7. Build & start
# ------------------------------------------------------------
Write-Host ""
Say $T_BUILD
Push-Location $installDir
& docker compose up -d --build
if ($LASTEXITCODE -ne 0) {
    Pop-Location
    Fail $T_COMPOSE_FAILED
}

# Create singbox-node container so the agent can manage it later via docker start/stop
Write-Host ""
Say $T_PREP_SING
& docker compose create singbox-node 2>$null | Out-Null
Pop-Location

Write-Host ""
Write-Host $T_DONE -ForegroundColor Green
Write-Host ""
Write-Host $T_QUICK
Write-Host "   $T_Q_LOGS    docker logs -f node-agent"
Write-Host "   $T_Q_STOP    cd `"$installDir`"; docker compose down"
Write-Host "   $T_Q_CLI     cd `"$installDir`"; bash fleet-cli.sh"
Write-Host ""

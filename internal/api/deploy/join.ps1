# ============================================================
#  Malaxis Fleet - Interactive Client Installer (Windows)
#  Native Windows PowerShell (5.1+).
#  Run with:  irm https://<join-domain>/join.ps1?t=<SECRET_TOKEN> | iex
# ============================================================

# Force UTF-8 so localized (Russian) text renders correctly on any
# console codepage (e.g. cp866 / cp437).
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
[Console]::InputEncoding = [System.Text.Encoding]::UTF8

$ErrorActionPreference = "Continue"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

function Say  { Write-Host "[+] $args" -ForegroundColor Green }
function Warn { Write-Host "[!] $args" -ForegroundColor Yellow }
function Fail { Write-Host "[x] $args" -ForegroundColor Red; exit 1 }

# Write UTF-8 WITHOUT a BOM so Python (json.load) and jq can parse the file.
function Write-Utf8([string]$Path, [string]$Content) {
    [System.IO.File]::WriteAllText($Path, $Content, (New-Object System.Text.UTF8Encoding($false)))
}

# Invoke-Compose runs the user-selected compose command ("docker compose" v2
# plugin or "docker-compose" v1 standalone) with the given arguments.
function Invoke-Compose([string[]]$ComposeArgs) {
    if ($script:composeCmd -eq "docker-compose") {
        & docker-compose @ComposeArgs
    } else {
        & docker compose @ComposeArgs
    }
}

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
    $T_COMPOSE_MISSING      = "Neither 'docker compose' nor 'docker-compose' is installed! Please install Docker Compose, then re-run this script."
    $T_COMPOSE_OK           = "Docker Compose is available."
    $T_COMPOSE_BOTH         = "Both Docker Compose tools detected. Select command:"
    $T_COMPOSE_OPT_V2       = "[1] docker compose (v2 plugin - Recommended)"
    $T_COMPOSE_OPT_V1       = "[2] docker-compose (v1 standalone)"
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
    $T_NAME_RETRY           = "Device name cannot be empty - please enter a name: "
    $T_NAME_DEFAULT         = "Using default device name: "
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
    $T_COMPOSE_FAILED       = "Docker Compose up failed. Check Docker Desktop settings and re-run this script."
    $T_PREP_SING            = "Preparing singbox-node container..."
    $T_DONE                 = "Malaxis Fleet Agent is running!"
    $T_QUICK                = "Quick commands:"
    $T_Q_LOGS               = "View logs:"
    $T_Q_STOP               = "Stop agent:"
    $T_Q_CLI                = "CLI:"
    $T_GLOBAL_CMD           = "You can now run 'malaxis-fleet' from any new terminal!"
    $T_LAUNCH_CLI           = "Launching fleet-cli..."
    $T_SUMMARY_TITLE        = "✅ Malaxis Fleet Agent installed successfully!"
    $T_SUMMARY_SOCKS        = "SOCKS5 Proxy"
    $T_SUMMARY_HTTP         = "HTTP Proxy"
} else {
    $T_PREFLIGHT            = "Проверка системных требований..."
    $T_DOCKER_NOT_INSTALLED = "Docker не установлен! Установите Docker Desktop (https://www.docker.com/products/docker-desktop), затем запустите скрипт заново."
    $T_DOCKER_INSTALLED     = "Docker установлен."
    $T_DOCKER_NOT_RUNNING   = "Docker не запущен! Пожалуйста, запустите Docker Desktop или службу Docker перед установкой."
    $T_DOCKER_RUNNING       = "Docker запущен."
    $T_COMPOSE_MISSING      = "Ни одна из утилит Docker Compose не установлена! Установите Docker Compose, затем запустите скрипт заново."
    $T_COMPOSE_OK           = "Docker Compose доступен."
    $T_COMPOSE_BOTH         = "Обнаружены обе утилиты Docker Compose. Выберите команду:"
    $T_COMPOSE_OPT_V2       = "[1] docker compose (v2 плагин - Рекомендуется)"
    $T_COMPOSE_OPT_V1       = "[2] docker-compose (v1 standalone)"
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
    $T_NAME_RETRY           = "Имя устройства не может быть пустым - введите имя: "
    $T_NAME_DEFAULT         = "Использую имя устройства по умолчанию: "
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
    $T_COMPOSE_FAILED       = "Не удалось выполнить Docker Compose up. Проверьте настройки Docker Desktop и запустите скрипт заново."
    $T_PREP_SING            = "Подготовка контейнера singbox-node..."
    $T_DONE                 = "Malaxis Fleet Agent запущен!"
    $T_QUICK                = "Быстрые команды:"
    $T_Q_LOGS               = "Логи:"
    $T_Q_STOP               = "Остановить:"
    $T_Q_CLI                = "CLI:"
    $T_GLOBAL_CMD           = "Теперь команду 'malaxis-fleet' можно запускать из любого нового окна терминала!"
    $T_LAUNCH_CLI           = "Запуск fleet-cli..."
    $T_SUMMARY_TITLE        = "✅ Malaxis Fleet Agent успешно установлен!"
    $T_SUMMARY_SOCKS        = "SOCKS5 Прокси"
    $T_SUMMARY_HTTP         = "HTTP Прокси"
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

& docker info 2>&1 | Out-Null
if ($LASTEXITCODE -ne 0) {
    Fail $T_DOCKER_NOT_RUNNING
}
Say $T_DOCKER_RUNNING

# Compose v2 plugin / v1 standalone detection. Both formats are supported:
#   - only v2 installed  -> "docker compose"
#   - only v1 installed  -> "docker-compose"
#   - both installed     -> ask the user which one to use
#   - neither            -> abort with a clear error
$script:composeCmd = ""
$haveComposeV2 = $false
$haveComposeV1 = $false
& docker compose version 2>&1 | Out-Null
if ($LASTEXITCODE -eq 0) { $haveComposeV2 = $true }
if (Get-Command docker-compose -ErrorAction SilentlyContinue) {
    & docker-compose version 2>&1 | Out-Null
    if ($LASTEXITCODE -eq 0) { $haveComposeV1 = $true }
}

if ($haveComposeV2 -and $haveComposeV1) {
    Write-Host $T_COMPOSE_BOTH
    Write-Host $T_COMPOSE_OPT_V2
    Write-Host $T_COMPOSE_OPT_V1
    $composeChoice = Read-Host ">"
    if ($composeChoice -eq "2") { $script:composeCmd = "docker-compose" }
    else { $script:composeCmd = "docker compose" }
} elseif ($haveComposeV2) {
    $script:composeCmd = "docker compose"
} elseif ($haveComposeV1) {
    $script:composeCmd = "docker-compose"
} else {
    Fail $T_COMPOSE_MISSING
}
Say "$T_COMPOSE_OK ($script:composeCmd)"

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
# Device name: asked on EVERY install and never silently discarded - an empty
# answer (accidental Enter) triggers a second prompt while a console is
# available; only a fully non-interactive run falls back to the hostname.
$nodeName = Read-Host $T_NODE_PROMPT
if ([string]::IsNullOrWhiteSpace($nodeName)) {
    if (-not [Console]::IsInputRedirected) {
        Warn $T_NAME_RETRY
        $nodeName = Read-Host $T_NODE_PROMPT
    }
}
if ([string]::IsNullOrWhiteSpace($nodeName)) {
    $nodeName = $hostName
    Say "$T_NAME_DEFAULT$hostName"
}

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
$containerNames = (& docker ps --format "{{.Names}}" 2>&1) -join "`n"
if ($containerNames -match "node-agent") { $existing = $true }

if ($existing) {
    Write-Host ""
    Say $T_REINSTALL
    if (Test-Path $installDir) {
        Push-Location $installDir
        Invoke-Compose down --remove-orphans 2>&1 | Out-Null
        & docker rm -f node-agent xray-node singbox-node 2>&1 | Out-Null
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
# Download and extract the modular agent package (agent_src/*.py).
Download-File "$apiBase/api/agent/latest.zip?t=$token" (Join-Path $installDir "agent_src.zip")
try {
    Expand-Archive -LiteralPath (Join-Path $installDir "agent_src.zip") -DestinationPath $installDir -Force
} catch {
    Write-Host "WARN: failed to extract agent_src.zip: $($_.Exception.Message)"
}
Remove-Item -Force (Join-Path $installDir "agent_src.zip") -ErrorAction SilentlyContinue

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
Download-File "$joinBase/fleet-cli.ps1?t=$token" (Join-Path $installDir "fleet-cli.ps1")

# Create a global "malaxis-fleet" command: a .cmd wrapper in the install dir
# plus the install dir registered in the user's PATH so the CLI works from
# any terminal window (PATH applies to newly opened terminals).
$BatPath = Join-Path $installDir "malaxis-fleet.cmd"
Write-Utf8 $BatPath "@powershell -ExecutionPolicy Bypass -File ""$installDir\fleet-cli.ps1"""
$UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($UserPath -notlike "*$installDir*") {
    if ([string]::IsNullOrEmpty($UserPath)) {
        [Environment]::SetEnvironmentVariable("Path", $installDir, "User")
    } else {
        [Environment]::SetEnvironmentVariable("Path", "$UserPath;$installDir", "User")
    }
}

# ------------------------------------------------------------
# 6. Persist onboarding choices BEFORE starting the stack
# ------------------------------------------------------------
$state = @{
    sub_url     = $subUrl
    node_name   = $nodeName
    active_mode = $smartMode
    compose_cmd = $script:composeCmd
} | ConvertTo-Json -Compress
Write-Utf8 (Join-Path $installDir "configs\agent_state.json") $state
Say ($T_STATE_WRITTEN -f $nodeName, $smartMode)

# ------------------------------------------------------------
# 7. Build & start
# ------------------------------------------------------------
Write-Host ""
Say $T_BUILD
Push-Location $installDir
Invoke-Compose up -d --build
if ($LASTEXITCODE -ne 0) {
    Pop-Location
    Fail $T_COMPOSE_FAILED
}

# Create singbox-node container so the agent can manage it later via docker start/stop
Write-Host ""
Say $T_PREP_SING
Invoke-Compose create singbox-node 2>&1 | Out-Null
Pop-Location

Write-Host ""
Write-Host $T_DONE -ForegroundColor Green
Write-Host ""

$summaryLines = @(
    $T_SUMMARY_TITLE,
    "",
    "  $($T_SUMMARY_SOCKS) : 127.0.0.1:6357",
    "  $($T_SUMMARY_HTTP)  : 127.0.0.1:6358",
    "",
    "  $T_GLOBAL_CMD"
)
$boxWidth = ($summaryLines | ForEach-Object { $_.Length } | Measure-Object -Maximum).Maximum + 4
Write-Host ("╔" + ("═" * $boxWidth) + "╗") -ForegroundColor Cyan
foreach ($l in $summaryLines) {
    $pad = $boxWidth - $l.Length + 2
    Write-Host ("║  " + $l + (" " * $pad) + "║") -ForegroundColor Cyan
}
Write-Host ("╚" + ("═" * $boxWidth) + "╝") -ForegroundColor Cyan
Write-Host ""
Write-Host $T_QUICK
Write-Host "   $T_Q_LOGS    docker logs -f node-agent"
Write-Host "   $T_Q_STOP    cd `"$installDir`"; $script:composeCmd down"
Write-Host "   $T_Q_CLI     cd `"$installDir`"; .\fleet-cli.ps1"
Write-Host ""

# Auto-launch the CLI so the subscription can be configured right away
Write-Host ""
Say $T_LAUNCH_CLI
& powershell.exe -NoProfile -ExecutionPolicy Bypass -File (Join-Path $installDir "fleet-cli.ps1")
Write-Host ""

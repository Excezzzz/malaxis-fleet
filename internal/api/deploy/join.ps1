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

Write-Host ""
Write-Host "================================================" -ForegroundColor Cyan
Write-Host "     Malaxis Fleet - Client Installer" -ForegroundColor Cyan
Write-Host "     Native Windows PowerShell" -ForegroundColor Cyan
Write-Host "================================================" -ForegroundColor Cyan
Write-Host ""

# ------------------------------------------------------------
# 1. Pre-flight dependency & resource checks
# ------------------------------------------------------------
Say "Running pre-flight checks..."

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    Fail "Docker is not installed! Install Docker Desktop (https://www.docker.com/products/docker-desktop), then re-run this script."
}
Say "Docker is installed."

& docker info 2>$null | Out-Null
if ($LASTEXITCODE -ne 0) {
    Fail "Docker is not running! Please start Docker Desktop or the Docker daemon before installing."
}
Say "Docker daemon is running."

& docker compose version 2>$null | Out-Null
if ($LASTEXITCODE -ne 0) {
    Fail "Docker Compose plugin is not available! Enable the 'docker compose' v2 plugin in Docker Desktop, then re-run this script."
}
Say "Docker Compose plugin is available."

Say "Checking master server connectivity (__API_DOMAIN__)..."
$reachable = Test-NetConnection -ComputerName "__API_DOMAIN__" -Port 443 -InformationLevel Quiet -WarningAction SilentlyContinue
if ($reachable) {
    Say "Master server is reachable."
} else {
    Warn "Master server did not respond. Check your network/firewall; the agent will retry automatically once started."
}

# ------------------------------------------------------------
# 2. Interactive installation directory selector
# ------------------------------------------------------------
$docs = [Environment]::GetFolderPath('MyDocuments')
$desk = [Environment]::GetFolderPath('Desktop')
if ([string]::IsNullOrWhiteSpace($docs)) { $docs = $HOME }
if ([string]::IsNullOrWhiteSpace($desk)) { $desk = $HOME }

Write-Host ""
Write-Host "Where would you like to install Malaxis Fleet Client?"
Write-Host "  [1] Documents (Default: $docs\malaxis-fleet-client)"
Write-Host "  [2] Desktop ($desk\malaxis-fleet-client)"
Write-Host "  [3] User Home Directory ($HOME\malaxis-fleet-client)"
Write-Host "  [4] Custom Path"
$dirChoice = Read-Host "Select [1-4, default 1]"
if ([string]::IsNullOrWhiteSpace($dirChoice)) { $dirChoice = "1" }

$baseDir = $docs
switch ($dirChoice) {
    "2" { $baseDir = $desk }
    "3" { $baseDir = $HOME }
    "4" {
        $customDir = Read-Host "Enter custom installation path"
        if (-not [string]::IsNullOrWhiteSpace($customDir)) { $baseDir = $customDir }
    }
}
if ([string]::IsNullOrWhiteSpace($baseDir)) { $baseDir = $HOME }
$installDir = Join-Path $baseDir "malaxis-fleet-client"
New-Item -ItemType Directory -Force -Path (Join-Path $installDir "configs") | Out-Null
Say "Installation directory: $installDir"

# ------------------------------------------------------------
# 3. Interactive setup (onboarding prompts)
# ------------------------------------------------------------
$hostName = $env:COMPUTERNAME
if ([string]::IsNullOrWhiteSpace($hostName)) { $hostName = "fleet-node" }
$nodeName = Read-Host "Enter a friendly name for this device [Default: $hostName]"
if ([string]::IsNullOrWhiteSpace($nodeName)) { $nodeName = $hostName }

$subUrl = Read-Host "Enter your 3x-ui Subscription URL (Press Enter to skip)"
$subUrl = $subUrl.Trim()

Write-Host ""
Write-Host "Select default Smart Routing Mode:"
Write-Host "  [1] Balanced - Best stability & lowest jitter (Recommended)"
Write-Host "  [2] Fastest - Lowest ping"
Write-Host "  [3] Manual"
$modeChoice = Read-Host "Select [1-3, default 1]"
$smartMode = "balanced"
switch ($modeChoice) {
    "2" { $smartMode = "fastest" }
    "3" { $smartMode = "manual" }
}

$autostart = Read-Host "Enable automatic startup on system boot? (Task Scheduler on Windows) [Y/n]"
if ([string]::IsNullOrWhiteSpace($autostart)) { $autostart = "y" }
$autostart = $autostart.ToLower()

# ------------------------------------------------------------
# 4. Clean re-install detection
# ------------------------------------------------------------
$existing = $false
if (Test-Path (Join-Path $installDir "docker-compose.yml")) { $existing = $true }
$containerNames = (& docker ps --format "{{.Names}}" 2>$null) -join "`n"
if ($containerNames -match "node-agent") { $existing = $true }

if ($existing) {
    Write-Host ""
    Say "Existing installation detected. Performing clean re-install..."
    if (Test-Path $installDir) {
        Push-Location $installDir
        & docker compose down --remove-orphans 2>$null | Out-Null
        & docker rm -f node-agent xray-node singbox-node 2>$null | Out-Null
        Pop-Location
    }
    if (Test-Path (Join-Path $installDir "configs")) {
        Say "Preserving existing configs directory..."
        $backup = Join-Path $env:TEMP "fleet-config-backup"
        if (Test-Path $backup) { Remove-Item -Recurse -Force $backup }
        Copy-Item -Recurse -Force (Join-Path $installDir "configs") $backup
    }
    if (Test-Path $installDir) { Remove-Item -Recurse -Force $installDir }
    Say "Old installation cleaned."
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
    Write-Host "Downloading $(Split-Path $Dest -Leaf)..."
    Invoke-WebRequest -UseBasicParsing -Uri $Url -OutFile $Dest
}

Write-Host ""
Say "Downloading client files..."
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
    Say "Previous configs restored."
}

# Download default configs so containers start cleanly
Write-Host ""
Say "Downloading default proxy configs..."
try {
    Download-File "$subBase/configs/xray_config.json?t=$token" (Join-Path $installDir "configs\xray_config.json")
} catch {
    Set-Content -Path (Join-Path $installDir "configs\xray_config.json") -Value @'
{
  "log": { "loglevel": "warning" },
  "dns": { "servers": ["https://dns.google/dns-query", "https://cloudflare-dns.com/dns-query", "8.8.8.8", "1.1.1.1"], "queryStrategy": "UseIPv4" },
  "inbounds": [
    { "port": 6357, "listen": "0.0.0.0", "protocol": "socks", "settings": { "auth": "noauth", "udp": true }, "sniffing": { "enabled": true, "destOverride": ["http", "tls", "quic"], "routeOnly": true }, "tag": "socks-in", "sockopt": { "tcpNoDelay": true, "tcpKeepAliveInterval": 15 } },
    { "port": 6358, "listen": "0.0.0.0", "protocol": "http", "settings": { "timeout": 0 }, "tag": "http-in", "sockopt": { "tcpNoDelay": true, "tcpKeepAliveInterval": 15 } }
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
Say "Downloading fleet-cli utility..."
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
Say "Configuration written to configs/agent_state.json (node: $nodeName, smart mode: $smartMode)."

# ------------------------------------------------------------
# 7. Build & start
# ------------------------------------------------------------
Write-Host ""
Say "Building agent image and starting services..."
Push-Location $installDir
& docker compose up -d --build
if ($LASTEXITCODE -ne 0) {
    Pop-Location
    Fail "docker compose up failed. Check Docker Desktop settings and re-run this script."
}

# Create singbox-node container so the agent can manage it later via docker start/stop
Write-Host ""
Say "Preparing singbox-node container..."
& docker compose create singbox-node 2>$null | Out-Null
Pop-Location

# ------------------------------------------------------------
# 8. Auto-start on boot (Task Scheduler)
# ------------------------------------------------------------
if ($autostart -eq "y" -or $autostart -eq "yes") {
    Write-Host ""
    Say "Installing Task Scheduler auto-start..."
    $starter = Join-Path $installDir "start-agent.ps1"
    Set-Content -Path $starter -Value "Set-Location -LiteralPath '$installDir'; docker compose up -d" -Encoding UTF8
    try {
        $action = New-ScheduledTaskAction -Execute "powershell.exe" -Argument "-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File `"$starter`""
        $trigger = New-ScheduledTaskTrigger -AtStartup
        Register-ScheduledTask -TaskName "MalaxisFleetAgent" -Action $action -Trigger $trigger -RunLevel Highest -Force -ErrorAction Stop | Out-Null
        Say "Scheduled task 'MalaxisFleetAgent' registered (runs at every boot)."
    } catch {
        Warn "Could not register the scheduled task (admin rights may be required). The agent keeps running while Docker Desktop is up."
    }
} else {
    Write-Host ""
    Say "Skipping auto-start on boot."
}

Write-Host ""
Write-Host "Malaxis Fleet Agent is running!" -ForegroundColor Green
Write-Host ""
Write-Host "Quick commands:"
Write-Host "   View logs:    docker logs -f node-agent"
Write-Host "   Stop agent:   cd `"$installDir`"; docker compose down"
Write-Host "   CLI (Git Bash): cd `"$installDir`"; bash fleet-cli.sh"
Write-Host ""

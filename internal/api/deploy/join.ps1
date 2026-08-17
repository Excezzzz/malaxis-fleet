# ============================================================
#  Malaxis Fleet - Zero-Touch Silent Client Installer (Windows)
#  Native Windows PowerShell (5.1+).
#  Run with:  irm https://<join-domain>/join.ps1?t=<SECRET_TOKEN> | iex
#
#  Fully non-interactive: the node registers itself under the OS
#  hostname and ALL configuration (subscription URLs, device name,
#  VPN mode) is done afterwards from the Web UI / Telegram bot.
# ============================================================

# Allow this user to run local PowerShell scripts (fleet-cli.ps1 etc.)
# without execution-policy errors on fresh Windows machines.
Set-ExecutionPolicy RemoteSigned -Scope CurrentUser -Force -ErrorAction SilentlyContinue

# Force UTF-8 so output renders correctly on any console codepage.
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8

$ErrorActionPreference = "Continue"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

function Say  { Write-Host "[+] $args" -ForegroundColor Green }
function Warn { Write-Host "[!] $args" -ForegroundColor Yellow }
function Fail { Write-Host "[x] $args" -ForegroundColor Red; exit 1 }

# Write UTF-8 WITHOUT a BOM so Python (json.load) and jq can parse the file.
function Write-Utf8([string]$Path, [string]$Content) {
    [System.IO.File]::WriteAllText($Path, $Content, (New-Object System.Text.UTF8Encoding($false)))
}

# Invoke-Compose runs the detected compose command ("docker compose" v2 plugin
# preferred, "docker-compose" v1 standalone as automatic fallback).
function Invoke-Compose([string[]]$ComposeArgs) {
    if ($script:composeCmd -eq "docker-compose") {
        & docker-compose @ComposeArgs
    } else {
        & docker compose @ComposeArgs
    }
}

# ------------------------------------------------------------
# Zero-touch defaults: no prompts anywhere in this installer.
# ------------------------------------------------------------
$installDir = Join-Path $HOME "malaxis-fleet-client"
$hostName = $env:COMPUTERNAME
if ([string]::IsNullOrWhiteSpace($hostName)) { $hostName = "fleet-node" }

Write-Host ""
Write-Host "================================================" -ForegroundColor Cyan
Write-Host "     Malaxis Fleet - Zero-Touch Client Installer" -ForegroundColor Cyan
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

& docker info 2>&1 | Out-Null
if ($LASTEXITCODE -ne 0) {
    Fail "Docker is not running! Please start Docker Desktop or the Docker daemon before installing."
}
Say "Docker daemon is running."

# Compose v2 plugin / v1 standalone detection (silent, zero-touch):
# v2 is preferred; v1 standalone is the automatic fallback.
$script:composeCmd = ""
$haveComposeV2 = $false
$haveComposeV1 = $false
& docker compose version 2>&1 | Out-Null
if ($LASTEXITCODE -eq 0) { $haveComposeV2 = $true }
if (Get-Command docker-compose -ErrorAction SilentlyContinue) {
    & docker-compose version 2>&1 | Out-Null
    if ($LASTEXITCODE -eq 0) { $haveComposeV1 = $true }
}

if ($haveComposeV2) {
    $script:composeCmd = "docker compose"
} elseif ($haveComposeV1) {
    $script:composeCmd = "docker-compose"
} else {
    Fail "Neither 'docker compose' nor 'docker-compose' is installed! Please install Docker Compose, then re-run this script."
}
Say "Docker Compose is available ($script:composeCmd)."

Say "Checking master server connectivity (__API_DOMAIN__)..."
$reachable = Test-NetConnection -ComputerName "__API_DOMAIN__" -Port 443 -InformationLevel Quiet -WarningAction SilentlyContinue
if ($reachable) {
    Say "Master server is reachable."
} else {
    Warn "Master server did not respond. Check your network/firewall; the agent will retry automatically once started."
}

# ------------------------------------------------------------
# 2. Clean re-install detection
# ------------------------------------------------------------
$existing = $false
if (Test-Path (Join-Path $installDir "docker-compose.yml")) { $existing = $true }
$containerNames = (& docker ps --format "{{.Names}}" 2>&1) -join "`n"
if ($containerNames -match "node-agent") { $existing = $true }

if ($existing) {
    Write-Host ""
    Say "Existing installation detected. Performing clean re-install..."
    if (Test-Path $installDir) {
        Push-Location $installDir
        Invoke-Compose down --remove-orphans 2>&1 | Out-Null
        & docker rm -f node-agent xray-node singbox-node 2>&1 | Out-Null
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
# 3. Download client payloads
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
    { "port": 6357, "listen": "0.0.0.0", "protocol": "socks", "settings": { "auth": "noauth", "udp": true }, "sniffing": { "enabled": true, "destOverride": ["http", "tls", "quic"] }, "tag": "socks-in", "sockopt": { "tcpNoDelay": true, "tcpKeepAliveInterval": 15, "tcpKeepAliveIdle": 15 } },
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
Say "Downloading fleet-cli utility..."
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
# 4. Persist zero-touch defaults BEFORE starting the stack:
#    the node registers under the OS hostname; subscription URLs
#    and VPN mode are configured later from the Web UI / bot.
# ------------------------------------------------------------
$state = @{
    node_name   = $hostName
    active_mode = "balanced"
    compose_cmd = $script:composeCmd
    sub_url     = ""
    sub_urls    = @()
} | ConvertTo-Json -Compress
Write-Utf8 (Join-Path $installDir "configs\agent_state.json") $state
Say "Configuration written to configs/agent_state.json (node: $hostName, smart mode: balanced)."

# ------------------------------------------------------------
# 5. Build & start
# ------------------------------------------------------------
Write-Host ""
Say "Building agent image and starting services..."
Push-Location $installDir
Invoke-Compose up -d --build
if ($LASTEXITCODE -ne 0) {
    Pop-Location
    Fail "Docker Compose up failed. Check Docker Desktop settings and re-run this script."
}

# Create singbox-node container so the agent can manage it later via docker start/stop
Write-Host ""
Say "Preparing singbox-node container..."
Invoke-Compose create singbox-node 2>&1 | Out-Null
Pop-Location

Write-Host ""
Write-Host "✅ Malaxis Fleet Agent installed successfully!" -ForegroundColor Green
Write-Host "   The node is registered as: $hostName"
Write-Host "   Subscription URLs and VPN mode are configured from the Web UI / Telegram bot."
Write-Host ""
Write-Host "Quick commands:"
Write-Host "   Logs:    docker logs -f node-agent"
Write-Host "   Stop:    cd `"$installDir`"; $script:composeCmd down"
Write-Host "   CLI:     cd `"$installDir`"; .\fleet-cli.ps1"
Write-Host ""
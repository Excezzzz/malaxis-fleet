# ============================================================
#  Malaxis Fleet CLI - Interactive Terminal Utility (Windows)
#  Native Windows PowerShell (5.1+).
#  Run from the install directory:  .\fleet-cli.ps1
# ============================================================

# Force UTF-8 so localized text renders correctly on any console codepage.
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
[Console]::InputEncoding = [System.Text.Encoding]::UTF8

$AGENT_DIR = Split-Path -Parent $MyInvocation.MyCommand.Path
$CONFIG_DIR = Join-Path $AGENT_DIR "configs"
$STATE_FILE = Join-Path $CONFIG_DIR "agent_state.json"
$SUBCACHE_FILE = Join-Path $CONFIG_DIR "subscription_cache.json"
$DOCKER_COMPOSE_FILE = Join-Path $AGENT_DIR "docker-compose.yml"

function Write-Utf8([string]$Path, [string]$Content) {
    [System.IO.File]::WriteAllText($Path, $Content, (New-Object System.Text.UTF8Encoding($false)))
}

function Read-State {
    if (Test-Path -LiteralPath $STATE_FILE) {
        try {
            $obj = Get-Content -LiteralPath $STATE_FILE -Raw | ConvertFrom-Json
            if ($null -ne $obj) {
                $h = @{}
                $obj.PSObject.Properties | ForEach-Object { $h[$_.Name] = $_.Value }
                return $h
            }
        } catch { }
    }
    return @{}
}

function Get-State([string]$Key, [string]$Fallback = "") {
    $h = Read-State
    if ($h.ContainsKey($Key) -and $null -ne $h[$Key]) { return [string]$h[$Key] }
    return $Fallback
}

function Set-State([string]$Key, [string]$Value) {
    $h = Read-State
    $h[$Key] = $Value
    Write-Utf8 $STATE_FILE ($h | ConvertTo-Json -Compress)
}

# Resolve the docker compose command to use: the choice saved by the installer
# in agent_state.json wins, otherwise auto-detect (v2 plugin first, then v1
# standalone). Falls back to the v2 plugin.
$script:composeCmd = $null
function Get-ComposeCmd {
    if ($null -eq $script:composeCmd) {
        $saved = Get-State "compose_cmd" ""
        if ($saved -eq "docker compose" -or $saved -eq "docker-compose") {
            $script:composeCmd = $saved
        } else {
            & docker compose version 2>$null | Out-Null
            if ($LASTEXITCODE -eq 0) {
                $script:composeCmd = "docker compose"
            } elseif (Get-Command docker-compose -ErrorAction SilentlyContinue) {
                $script:composeCmd = "docker-compose"
            } else {
                $script:composeCmd = "docker compose"
            }
        }
    }
    return $script:composeCmd
}

# Invoke-Compose runs the resolved compose command ("docker compose" v2 plugin
# or "docker-compose" v1 standalone) with the given arguments.
function Invoke-Compose([string[]]$ComposeArgs) {
    if ((Get-ComposeCmd) -eq "docker-compose") {
        & docker-compose @ComposeArgs
    } else {
        & docker compose @ComposeArgs
    }
}

function Cache-Count {
    if (Test-Path -LiteralPath $SUBCACHE_FILE) {
        try { return @(Get-Content -LiteralPath $SUBCACHE_FILE -Raw | ConvertFrom-Json).Count } catch { }
    }
    return 0
}

# Detect a terminated / rejected node: an explicit marker written by the agent
# during self-destruct, or the agent container being gone entirely while Docker
# is up (Docker being down just means the regular "Start Docker" menu applies).
function Is-Terminated {
    if ((Get-State "terminated" "false") -eq "true") { return $true }
    $names = & docker ps -a --filter "name=node-agent" --format "{{.Names}}" 2>$null
    if ($LASTEXITCODE -ne 0) { return $false }
    if (($names -join "`n") -match "node-agent") { return $false }
    & docker info 2>$null | Out-Null
    return $LASTEXITCODE -eq 0
}

function Has-Sub {
    $u = Get-State "sub_url" ""
    return (-not [string]::IsNullOrWhiteSpace($u)) -and ($u -ne "null")
}

function Show-Menu {
    Clear-Host
    Write-Host "==========================================" -ForegroundColor Cyan
    Write-Host "     Malaxis Fleet Agent CLI" -ForegroundColor Cyan
    Write-Host "==========================================" -ForegroundColor Cyan
    Write-Host ""

    if (Is-Terminated) {
        Write-Host " [TERMINATED] This node was terminated or rejected by the admin." -ForegroundColor Red
        Write-Host " You can wipe the local identity and re-register it as a new device."
        Write-Host ""
        Write-Host " 1) Send Re-join Request"
        Write-Host " 2) Exit"
        Write-Host ""
        $choice = Read-Host "Select option [1-2]"
        switch ($choice) {
            "1" { Rejoin }
            "2" { Clear-Host; exit 0 }
            default { Show-Menu }
        }
        return
    }

    if (-not (Has-Sub)) {
        Write-Host " [SETUP] No subscription URL configured yet." -ForegroundColor Yellow
        Write-Host " Set your 3x-ui subscription URL to start using the fleet."
        Write-Host ""
        Write-Host " 1) Set Subscription URL"
        Write-Host " 2) Exit"
        Write-Host ""
        $choice = Read-Host "Select option [1-2]"
        switch ($choice) {
            "1" { Update-Subscription }
            "2" { Clear-Host; exit 0 }
            default { Show-Menu }
        }
        return
    }

    & docker ps 2>$null | Out-Null
    if ($LASTEXITCODE -ne 0) {
        Write-Host "Docker is not running!" -ForegroundColor Red
        Write-Host ""
        Write-Host " 1) Start Docker & Agent"
        Write-Host " 2) Exit"
        Write-Host ""
        $choice = Read-Host "Select option [1-2]"
        switch ($choice) {
            "1" {
                try { Start-Process "Docker Desktop" -ErrorAction SilentlyContinue } catch { }
                Start-Sleep 5
                Push-Location $AGENT_DIR
                Invoke-Compose up -d 2>$null | Out-Null
                Pop-Location
                Start-Sleep 2
                Show-Menu
            }
            "2" { Clear-Host; exit 0 }
            default { Show-Menu }
        }
        return
    }

    $node_status = (& docker ps --filter "name=node-agent" --format "{{.Status}}" 2>$null) -join " "
    $xray_status = (& docker ps --filter "name=xray-node" --format "{{.Status}}" 2>$null) -join " "
    $singbox_status = (& docker ps --filter "name=singbox-node" --format "{{.Status}}" 2>$null) -join " "

    $node_up = $node_status -match "Up"
    $xray_up = $xray_status -match "Up"
    $singbox_up = $singbox_status -match "Up"

    $active_server = Get-State "active_server" "Not selected (Use Option 3)"
    if ([string]::IsNullOrWhiteSpace($active_server) -or $active_server -eq "null") {
        $active_server = "Not selected (Use Option 3)"
    }
    $active_proto = Get-State "active_proto" "N/A"
    $active_mode = Get-State "active_mode" "manual"
    $last_seen = Get-State "last_seen" "N/A"
    $server_count = Cache-Count

    Write-Host "------------------------------------------"
    if ($node_up) { Write-Host " [ON] node-agent     $node_status" -ForegroundColor Green }
    else { Write-Host " [OFF] node-agent     Not running" -ForegroundColor Red }
    if ($xray_up) { Write-Host " [ON] xray-node      $xray_status" -ForegroundColor Green }
    else { Write-Host " [OFF] xray-node      Not running" -ForegroundColor Red }
    if ($singbox_up) { Write-Host " [ON] singbox-node   $singbox_status" -ForegroundColor Green }
    else { Write-Host " [OFF] singbox-node   Not running" -ForegroundColor Red }
    Write-Host "------------------------------------------"
    Write-Host ""
    Write-Host " Active Server:    $active_server" -ForegroundColor Cyan
    Write-Host " Active Protocol:  $active_proto" -ForegroundColor Cyan
    Write-Host " Selection Mode:   $active_mode" -ForegroundColor Cyan
    Write-Host " SOCKS5 Proxy:     127.0.0.1:6357" -ForegroundColor Cyan
    Write-Host " HTTP Proxy:       127.0.0.1:6358" -ForegroundColor Cyan
    Write-Host " Last Update:      $last_seen" -ForegroundColor Cyan
    if ($server_count -gt 0) {
        Write-Host " Cached Servers:   $server_count available"
    }
    Write-Host ""
    Write-Host "------------------------------------------"
    Write-Host " 1) Set / Update Subscription URL"
    Write-Host " 2) Update Client Files"
    Write-Host " 3) Switch Server"
    Write-Host " 4) Toggle Auto-Update"
    Write-Host " 5) View Agent Logs"
    Write-Host " 6) Rename Node"
    Write-Host " 7) Terminate & Self-Destruct"
    Write-Host " 8) Exit"
    Write-Host "------------------------------------------"
    Write-Host ""
    $choice = Read-Host "Select option [1-8]"
    switch ($choice) {
        "1" { Update-Subscription }
        "2" { Update-ClientFiles }
        "3" { Switch-Server }
        "4" { Toggle-AutoUpdate }
        "5" { View-Logs }
        "6" { Rename-Node }
        "7" { Terminate-Node }
        "8" { Clear-Host; exit 0 }
        default { Show-Menu }
    }
}

function Rejoin {
    Write-Host ""
    Write-Host "--- Send Re-join Request ---" -ForegroundColor Cyan
    Write-Host "WARNING: This wipes the local identity and re-registers this" -ForegroundColor Yellow
    Write-Host "device with the fleet as a brand-new node. This cannot be undone." -ForegroundColor Yellow
    Write-Host ""
    $confirm = Read-Host "Continue? [y/N]"
    if ($confirm -notmatch "^[yY]") {
        Write-Host "Cancelled." -ForegroundColor Yellow
        Start-Sleep 2
        Show-Menu
        return
    }
    Remove-Item -LiteralPath @($STATE_FILE, $SUBCACHE_FILE, (Join-Path $CONFIG_DIR "node_id.txt")) -Force -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Force -Path $CONFIG_DIR | Out-Null
    & docker rm -f node-agent 2>$null | Out-Null
    Push-Location $AGENT_DIR
    Invoke-Compose up -d --force-recreate node-agent 2>$null | Out-Null
    Pop-Location
    Write-Host "Re-join request sent. The agent is re-registering with a fresh identity..." -ForegroundColor Green
    Start-Sleep 3
    Clear-Host
    Show-Menu
}

function Update-Subscription {
    Write-Host ""
    Write-Host "--- Update Subscription ---" -ForegroundColor Cyan

    $current_url = Get-State "sub_url" ""
    if (-not [string]::IsNullOrWhiteSpace($current_url) -and $current_url -ne "null") {
        Write-Host "Current URL: $current_url"
        $sub_url = Read-Host "Enter new subscription URL (or press Enter to keep current)"
        $sub_url = ($sub_url -replace "\s", "").Trim()
        if ([string]::IsNullOrWhiteSpace($sub_url)) { $sub_url = $current_url }
    } else {
        $sub_url = (Read-Host "Enter your 3x-ui subscription URL").Trim()
    }

    if ([string]::IsNullOrWhiteSpace($sub_url)) {
        Write-Host "No URL entered. Cancelled." -ForegroundColor Yellow
        Start-Sleep 2
        Show-Menu
        return
    }

    Set-State "sub_url" $sub_url
    Write-Host "Subscription URL saved. Fetching subscription now..." -ForegroundColor Green
    & docker exec node-agent python3 -c "import agent_src.main; agent_src.main.update_subscription_cli('$sub_url')" 2>$null | Out-Null
    Write-Host "Subscription URL synced to the web dashboard." -ForegroundColor Green
    Start-Sleep 2

    $count = Cache-Count
    if ($count -gt 0) {
        Write-Host "Found $count servers in subscription!" -ForegroundColor Green
        Write-Host "Use Option 3 to select a server."
    } else {
        Write-Host "No servers found yet. The agent will fetch in background." -ForegroundColor Yellow
    }

    Start-Sleep 2
    Show-Menu
}

function Update-ClientFiles {
    Write-Host ""
    Write-Host "--- Updating Client Files ---" -ForegroundColor Cyan

    $backup = Join-Path $env:TEMP "agent_state_backup.json"
    if (Test-Path -LiteralPath $STATE_FILE) { Copy-Item -LiteralPath $STATE_FILE -Destination $backup -Force }

    try {
        Invoke-WebRequest -UseBasicParsing -Uri "https://__SUB_DOMAIN__/docker-compose.yml?t=__SECRET_TOKEN__" -OutFile $DOCKER_COMPOSE_FILE
    } catch { }

    if (Test-Path -LiteralPath $backup) {
        Copy-Item -LiteralPath $backup -Destination $STATE_FILE -Force
        Remove-Item -LiteralPath $backup -Force -ErrorAction SilentlyContinue
    }

    Push-Location $AGENT_DIR
    Invoke-Compose up -d --force-recreate node-agent 2>$null | Out-Null
    Pop-Location

    Write-Host "Client files updated!" -ForegroundColor Green
    Start-Sleep 2
    Show-Menu
}

function Rename-Node {
    Write-Host ""
    Write-Host "--- Rename Node ---" -ForegroundColor Cyan

    $current_name = Get-State "node_name" $env:COMPUTERNAME
    if ([string]::IsNullOrWhiteSpace($current_name)) { $current_name = $env:COMPUTERNAME }
    Write-Host "Current name: $current_name"
    $new_name = (Read-Host "Enter new name for this node").Trim()
    if ([string]::IsNullOrWhiteSpace($new_name)) {
        Write-Host "No name entered. Cancelled." -ForegroundColor Yellow
        Start-Sleep 2
        Show-Menu
        return
    }

    Set-State "node_name" $new_name
    & docker exec node-agent python3 -c "import node_agent; node_agent.report(status='Renamed', message='Node renamed via CLI')" 2>$null | Out-Null
    Write-Host "Node renamed to '$new_name'. The dashboard will pick it up shortly." -ForegroundColor Green
    Start-Sleep 2
    Show-Menu
}

function Terminate-Node {
    Write-Host ""
    Write-Host "--- Terminate & Self-Destruct ---" -ForegroundColor Red
    Write-Host "WARNING: This will tear down the VPN containers, wipe this node's local" -ForegroundColor Red
    Write-Host "configuration, and remove the agent. This cannot be undone." -ForegroundColor Red
    Write-Host ""
    $confirm_word = Read-Host "Type TERMINATE to confirm, or anything else to cancel"
    if ($confirm_word -ne "TERMINATE") {
        Write-Host "Cancelled." -ForegroundColor Yellow
        Start-Sleep 2
        Show-Menu
        return
    }
    Write-Host "Terminating..." -ForegroundColor Red
    & docker exec node-agent python3 -c "import node_agent; node_agent.enqueue('terminate')" 2>$null | Out-Null
    Write-Host "Self-destruct initiated. Goodbye." -ForegroundColor Red
    exit 0
}

function Switch-Server {
    Write-Host ""
    Write-Host "--- Server Switcher ---" -ForegroundColor Cyan

    Write-Host ""
    Write-Host "Available VPN Servers:"
    Write-Host ""
    & docker exec node-agent python3 -c "import node_agent; node_agent.print_server_list()" 2>$null
    if ($LASTEXITCODE -ne 0) {
        Write-Host "Failed to get server list from agent." -ForegroundColor Yellow
        Write-Host "Make sure node-agent is running and subscription is configured (Option 1)."
        Start-Sleep 2
        Show-Menu
        return
    }

    $server_count = 0
    try {
        $server_count = [int](& docker exec node-agent python3 -c "import json; print(len(json.load(open('/app/configs/subscription_cache.json'))))" 2>$null | Select-Object -First 1)
    } catch { $server_count = 0 }

    if ($server_count -le 0) {
        Write-Host "No servers in cache." -ForegroundColor Yellow
        Start-Sleep 2
        Show-Menu
        return
    }

    Write-Host ""
    Write-Host "  F) Fastest   (Auto-select lowest ping)"
    Write-Host "  B) Balanced  (Auto-select highest stability & lowest jitter)"
    Write-Host "  0) Cancel"
    Write-Host ""
    $choice = Read-Host "Select [F/B/1-$server_count] or 0 to Cancel"

    if ($choice -eq "0") {
        Show-Menu
        return
    }

    if ($choice -match "^[fF]$") {
        Write-Host ""
        Write-Host "Running benchmark and selecting fastest server..."
        & docker exec node-agent python3 -c "import node_agent; exit(node_agent.select_mode('fastest'))" 2>$null
        Start-Sleep 2
        Show-Menu
        return
    }

    if ($choice -match "^[bB]$") {
        Write-Host ""
        Write-Host "Running benchmark and selecting most stable server..."
        & docker exec node-agent python3 -c "import node_agent; exit(node_agent.select_mode('balanced'))" 2>$null
        Start-Sleep 2
        Show-Menu
        return
    }

    $idx = -1
    try { $idx = [int]$choice - 1 } catch { }
    if ($idx -lt 0 -or $idx -ge $server_count) {
        Write-Host "Invalid selection." -ForegroundColor Yellow
        Start-Sleep 2
        Switch-Server
        return
    }

    Write-Host ""
    Write-Host "Switching to server $($idx + 1)..."
    & docker exec node-agent python3 -c "import node_agent; exit(node_agent.select_server($idx))" 2>$null

    Start-Sleep 2
    Show-Menu
}

function Toggle-AutoUpdate {
    $current = Get-State "auto_update" "true"
    if ($current -eq "true") {
        Set-State "auto_update" "false"
        Write-Host "Auto-update disabled. You take full responsibility for non-working proxy configurations." -ForegroundColor Yellow
    } else {
        Set-State "auto_update" "true"
        Write-Host "Auto-update enabled." -ForegroundColor Green
    }
    Start-Sleep 3
    Show-Menu
}

function View-Logs {
    Write-Host ""
    Write-Host "--- Last 30 lines of node-agent logs ---" -ForegroundColor Cyan
    Write-Host "(Press Ctrl+C to return to menu)"
    Write-Host ""
    & docker logs node-agent --tail 30 2>$null
    if ($LASTEXITCODE -ne 0) { Write-Host "No logs available." -ForegroundColor Yellow }
    Write-Host ""
    Read-Host "Press Enter to return to menu"
    Show-Menu
}

if (-not (Test-Path -LiteralPath $DOCKER_COMPOSE_FILE)) {
    Write-Host "Fleet Agent not found at $AGENT_DIR" -ForegroundColor Red
    Write-Host "Please run the installation script first:"
    Write-Host "  irm https://__JOIN_DOMAIN__/join.ps1?t=__SECRET_TOKEN__ | iex"
    exit 1
}

# The CLI is interactive: when stdin is redirected (piped/cron/CI) Read-Host
# gets no real console input, so report it and exit instead of silently
# swallowing every prompt.
if ([Console]::IsInputRedirected) {
    Write-Host "No interactive terminal detected. Run the CLI from a real terminal:" -ForegroundColor Yellow
    Write-Host "  .\fleet-cli.ps1"
    exit 0
}

Show-Menu

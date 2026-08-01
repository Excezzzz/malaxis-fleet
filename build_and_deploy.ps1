# Malaxis Fleet - Build & Deploy Script
#
# Usage:
#   .\build_and_deploy.ps1 user@server_ip
#   .\build_and_deploy.ps1 (will prompt for target)
#
param (
    [string]$VpsTarget
)

# --- Configuration ---
$RemotePath = "~/malaxis-fleet"
$BinaryName = "master_server"
$WebDir = "internal/api/web"

# Function to check for errors and exit
function Check-Error {
    if ($LASTEXITCODE -ne 0) {
        Write-Host "❌ [FAILED]" -ForegroundColor Red
        exit 1
    }
}

# --- Pre-flight Checks ---
Write-Host ">>> Performing pre-flight checks..." -ForegroundColor Cyan

# Check for SSH target
if ([string]::IsNullOrEmpty($VpsTarget)) {
    $VpsTarget = Read-Host "Please enter the SSH target (e.g., user@server_ip)"
    if ([string]::IsNullOrEmpty($VpsTarget)) {
        Write-Host "SSH target cannot be empty." -ForegroundColor Red
        exit 1
    }
}

# Check for .env file
if (-not (Test-Path ".env")) {
    Write-Host "ERROR: .env file not found. Please create it from .env.example." -ForegroundColor Red
    exit 1
}

# --- 1. Build Vue Frontend ---
Write-Host ">>> Building Vue 3 Frontend..." -ForegroundColor Cyan
Push-Location $WebDir
npm install
Check-Error
npm run build
Check-Error
Pop-Location
Write-Host ">>> Vue build successful." -ForegroundColor Green

# --- 2. Cross-compile Go Master Server ---
Write-Host ">>> Cross-compiling Go Master Server for Linux..." -ForegroundColor Cyan
$env:GOOS = "linux"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"
go build -ldflags="-s -w" -o $BinaryName ./cmd/server/main.go
Check-Error
Write-Host ">>> Go build successful: $BinaryName" -ForegroundColor Green

# --- 3. Deploy to VPS ---
Write-Host ">>> Deploying to $VpsTarget..." -ForegroundColor Cyan

# Create remote directory
Write-Host ">>> Creating remote directory..."
ssh -t $VpsTarget "mkdir -p $RemotePath"
Check-Error

# Upload files via SCP
Write-Host ">>> Uploading files..."
scp ./$BinaryName "$VpsTarget`:$RemotePath/"
Check-Error
scp ./Dockerfile "$VpsTarget`:$RemotePath/"
Check-Error
scp ./docker-compose.yml "$VpsTarget`:$RemotePath/"
Check-Error
scp ./.env "$VpsTarget`:$RemotePath/"
Check-Error

# --- 4. Remote Execution ---
Write-Host ">>> Restarting services on remote host..."
$sshCommand = "cd $RemotePath && docker compose down && docker compose up -d --build && docker builder prune -a -f && docker system prune -a --volumes -f"
ssh -t $VpsTarget $sshCommand
Check-Error

# --- 5. Cleanup ---
Write-Host ">>> Cleaning up local binary..." -ForegroundColor Cyan
Remove-Item -Path $BinaryName
Check-Error

# --- Success ---
Write-Host "🎉 [SUCCESS] Deployment complete." -ForegroundColor Green

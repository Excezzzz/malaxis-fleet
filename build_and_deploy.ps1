# Malaxis Fleet - Build & Deploy Script
#
# Usage:
#   .\build_and_deploy.ps1 user@server_ip
#   .\build_and_deploy.ps1 (will prompt for target)
#
# Ships the full source tree to the VPS and lets the multi-stage Dockerfile
# build the Vue dashboard and the Go backend inside the container.
#
param (
    [string]$VpsTarget
)

# --- Configuration ---
$RemotePath = "~/malaxis-fleet"

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

# --- 1. Sanity-check the frontend build locally ---
Write-Host ">>> Building Vue 3 Frontend..." -ForegroundColor Cyan
Push-Location "internal/api/web"
npm install
Check-Error
npm run build
Check-Error
Pop-Location
Write-Host ">>> Vue build successful." -ForegroundColor Green

# --- 2. Deploy source tree to VPS ---
Write-Host ">>> Deploying to $VpsTarget..." -ForegroundColor Cyan

# Create remote directory
Write-Host ">>> Creating remote directory..."
ssh -t $VpsTarget "mkdir -p $RemotePath"
Check-Error

# Stage the working tree (excluding VCS, data, deps, secrets, build artifacts)
# and ship it as a single tarball. The remote ./data and ./backups stay intact.
Write-Host ">>> Staging source tree..."
$Staging = Join-Path $env:TEMP "fleet-deploy-$PID"
if (Test-Path $Staging) { Remove-Item -Recurse -Force $Staging }
New-Item -ItemType Directory -Path $Staging | Out-Null
robocopy . $Staging /E /XD .git data backups node_modules dist /XF .env master_server | Out-Null
if ($LASTEXITCODE -ge 8) {
    Write-Host "Staging failed." -ForegroundColor Red
    exit 1
}
tar -cf (Join-Path $Staging "fleet-src.tar") -C $Staging .
Check-Error

Write-Host ">>> Uploading source tree..."
scp (Join-Path $Staging "fleet-src.tar") "$VpsTarget`:$RemotePath/"
Check-Error
ssh -t $VpsTarget "cd $RemotePath && tar -xf fleet-src.tar && rm fleet-src.tar"
Check-Error

# Upload .env separately (secrets stay out of the archive)
scp ./.env "$VpsTarget`:$RemotePath/"
Check-Error

# --- 3. Remote Execution ---
Write-Host ">>> Restarting services on remote host..."
$sshCommand = "cd $RemotePath && docker compose down && docker compose up -d --build && docker builder prune -a -f && docker system prune -a --volumes -f"
ssh -t $VpsTarget $sshCommand
Check-Error

# --- 4. Cleanup local build artifacts ---
Write-Host ">>> Cleaning up local build artifacts..." -ForegroundColor Cyan
Remove-Item -Path "internal/api/web/dist" -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item -Path $Staging -Recurse -Force -ErrorAction SilentlyContinue

# --- Success ---
Write-Host "🎉 [SUCCESS] Deployment complete." -ForegroundColor Green

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

# --- Version (single source of truth: root VERSION file) ---
if (-not (Test-Path "VERSION")) {
    Write-Host "ERROR: VERSION file not found. It must exist at the repository root." -ForegroundColor Red
    exit 1
}
$Version = (Get-Content "VERSION").Trim()

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
Write-Host ">>> Deploying Malaxis Fleet $Version to $VpsTarget..." -ForegroundColor Cyan

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
robocopy . $Staging /E /XD .git data backups node_modules /XF .env master_server | Out-Null
if ($LASTEXITCODE -ge 8) {
    Write-Host "Staging failed." -ForegroundColor Red
    exit 1
}
tar -cf (Join-Path $Staging "fleet-src.tar") -C $Staging .
Check-Error

Write-Host ">>> Uploading source tree..."
scp (Join-Path $Staging "fleet-src.tar") "$VpsTarget`:$RemotePath/"
Check-Error
# Extract on the remote. Plain `tar -xf` never deletes files that are gone
# from the archive (e.g. files removed by a refactor), so the remote tree is
# first purged of everything except the persistent ./data and ./backups
# directories, then repopulated from the fresh tarball.
Write-Host ">>> Extracting and syncing source tree (purge stale files)..."
ssh -t $VpsTarget "cd $RemotePath && (test -d data && mv data /tmp/fleet-deploy-data) ; (test -d backups && mv backups /tmp/fleet-deploy-backups) ; find . -mindepth 1 -maxdepth 1 ! -name fleet-src.tar -exec rm -rf {} + ; (test -d /tmp/fleet-deploy-data && mv /tmp/fleet-deploy-data data) ; (test -d /tmp/fleet-deploy-backups && mv /tmp/fleet-deploy-backups backups) ; tar -xf fleet-src.tar && rm fleet-src.tar"
Check-Error

# Upload .env separately (secrets stay out of the archive)
scp ./.env "$VpsTarget`:$RemotePath/"
Check-Error

# --- 3. Remote Execution ---
# IMPORTANT: `docker compose up --build` replaces `down && up`. A full `down`
# deletes the project network (malaxis-fleet_default) while the external caddy
# container stays attached to it, so caddy loses DNS + routing to fleet-master
# for the whole image build and the dashboard/API return 502s. Compose's `up`
# recreates containers in place and keeps the shared network alive.
# If the local build needs a custom npm registry (e.g. npmjs.org is slow or
# blocked on this network), it is forwarded to the remote build via
# NPM_REGISTRY (see Dockerfile ARG + docker-compose.yml build args).
Write-Host ">>> Restarting services on remote host..."
$envPrefix = ""
if ($env:NPM_REGISTRY) {
    $envPrefix = "NPM_REGISTRY='$($env:NPM_REGISTRY)' "
}
# IMPORTANT: never pass --volumes to docker system prune: it would destroy
# named volumes belonging to OTHER projects on the same VPS.
$sshCommand = "cd $RemotePath && ${envPrefix}docker compose up -d --build --remove-orphans && docker builder prune -a -f && docker system prune -a -f"
ssh -t $VpsTarget $sshCommand
Check-Error

# --- 4. Cleanup local build artifacts ---
Write-Host ">>> Cleaning up local build artifacts..." -ForegroundColor Cyan
Remove-Item -Path "internal/api/web/dist" -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item -Path $Staging -Recurse -Force -ErrorAction SilentlyContinue

# --- Success ---
Write-Host "🎉 [SUCCESS] Deployment of $Version complete." -ForegroundColor Green

$ErrorActionPreference = 'SilentlyContinue'

$root = Split-Path -Parent $PSScriptRoot
$backendDir = Join-Path $root 'backend'

Write-Host "Stopping backend via server_control.py..." -ForegroundColor Cyan
try {
  Set-Location $backendDir
  python server_control.py stop | Out-Null
} catch {
  Write-Host "Backend stop failed or not running." -ForegroundColor Yellow
}

Write-Host "Stopping frontend dev server (port 5173)..." -ForegroundColor Cyan
try {
  $pids = (Get-NetTCPConnection -LocalPort 5173 -State Listen).OwningProcess | Select-Object -Unique
  foreach ($pid in $pids) {
    Stop-Process -Id $pid -Force
  }
} catch {
  Write-Host "No frontend dev server found on port 5173." -ForegroundColor Yellow
}

Write-Host "Stop sequence complete." -ForegroundColor Green

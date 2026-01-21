$ErrorActionPreference = 'SilentlyContinue'

$root = Split-Path -Parent $PSScriptRoot
$backendDir = Join-Path $root 'backend'
$envFile = Join-Path $root '.env'

Write-Host "Stopping backend via server_control.py..." -ForegroundColor Cyan
try {
  Set-Location $backendDir
  python server_control.py stop | Out-Null
} catch {
  Write-Host "Backend stop failed or not running." -ForegroundColor Yellow
}

function Get-FrontendPort() {
  if (Test-Path $envFile) {
    $line = Select-String -Path $envFile -Pattern '^VITE_PORT=' -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($line) {
      $value = $line.Line.Substring('VITE_PORT='.Length)
      if ($value) { return [int]$value }
    }
  }
  return 5173
}

$frontendPort = Get-FrontendPort
Write-Host "Stopping frontend dev server (port $frontendPort)..." -ForegroundColor Cyan
try {
  $pids = (Get-NetTCPConnection -LocalPort $frontendPort -State Listen).OwningProcess | Select-Object -Unique
  foreach ($pid in $pids) {
    Stop-Process -Id $pid -Force
  }
} catch {
  Write-Host "No frontend dev server found on port $frontendPort." -ForegroundColor Yellow
}

Write-Host "Stop sequence complete." -ForegroundColor Green

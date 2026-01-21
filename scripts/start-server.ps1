$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
$backendDir = Join-Path $root 'backend'
$frontendDir = Join-Path $root 'frontend'
$envFile = Join-Path $root '.env'

function Get-OrCreateSecret([string]$name) {
  $value = [Environment]::GetEnvironmentVariable($name)
  if ($value) { return $value }

  if (Test-Path $envFile) {
    $line = Select-String -Path $envFile -Pattern "^$name=" -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($line) {
      $value = $line.Line.Substring($name.Length + 1)
      if ($value) { return $value }
    }
  }

  $bytes = New-Object byte[] 32
  [System.Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes)
  return [Convert]::ToBase64String($bytes)
}

function Ensure-EnvFileSecrets() {
  $jwt = Get-OrCreateSecret 'JWT_SECRET'
  $enc = Get-OrCreateSecret 'ENCRYPTION_KEY'

  $lines = @()
  if (Test-Path $envFile) {
    $lines = Get-Content $envFile
  }

  if (-not ($lines | Where-Object { $_ -match '^JWT_SECRET=' })) {
    $lines += "JWT_SECRET=$jwt"
  }
  if (-not ($lines | Where-Object { $_ -match '^ENCRYPTION_KEY=' })) {
    $lines += "ENCRYPTION_KEY=$enc"
  }

  $lines | Set-Content -Path $envFile -Encoding UTF8

  $env:JWT_SECRET = $jwt
  $env:ENCRYPTION_KEY = $enc
}

Ensure-EnvFileSecrets

Write-Host "Starting backend (go run) in a new PowerShell window..." -ForegroundColor Cyan
Start-Process pwsh -ArgumentList @(
  '-NoExit',
  '-Command',
  "& { Set-Location '$backendDir'; `$env:JWT_SECRET = '$($env:JWT_SECRET)'; `$env:ENCRYPTION_KEY = '$($env:ENCRYPTION_KEY)'; python server_control.py start --foreground --go }"
)

Write-Host "Starting frontend (npm run dev) in a new PowerShell window..." -ForegroundColor Cyan
Start-Process pwsh -ArgumentList @(
  '-NoExit',
  '-Command',
  "Set-Location '$frontendDir'; npm run dev"
)

Write-Host "Windows launched. Close the windows or run scripts/stop-dev.ps1 to stop services." -ForegroundColor Green

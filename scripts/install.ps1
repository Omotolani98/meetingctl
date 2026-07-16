# meetingctl per-user installer (Windows)
# Usage: irm https://raw.githubusercontent.com/Omotolani98/meetingctl/main/scripts/install.ps1 | iex
# Or from repo: powershell -ExecutionPolicy Bypass -File scripts/install.ps1

$ErrorActionPreference = "Stop"
$Version = if ($env:MEETINGCTL_VERSION) { $env:MEETINGCTL_VERSION } else { "latest" }
$Prefix = if ($env:MEETINGCTL_PREFIX) { $env:MEETINGCTL_PREFIX } else { Join-Path $env:LOCALAPPDATA "Programs\meetingctl" }
$BinDir = Join-Path $Prefix "bin"
$DataDir = if ($env:MEETINGCTL_DATA_DIR) { $env:MEETINGCTL_DATA_DIR } else { Join-Path $env:USERPROFILE ".meetingctl" }

New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
New-Item -ItemType Directory -Force -Path $DataDir | Out-Null

$RepoRoot = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
if (Test-Path (Join-Path $RepoRoot "go.mod") -and ($env:MEETINGCTL_INSTALL_FROM_SOURCE -ne "0")) {
  Write-Host "==> building from source"
  Push-Location $RepoRoot
  go build -o (Join-Path $BinDir "meetingctl.exe") ./cmd/meetingctl
  go build -o (Join-Path $BinDir "meetingd.exe") ./cmd/meetingd
  go build -o (Join-Path $BinDir "meeting-mcp.exe") ./cmd/meeting-mcp
  Pop-Location
} elseif (Get-Command go -ErrorAction SilentlyContinue) {
  Write-Host "==> installing from module source"
  $env:GOBIN = $BinDir
  go install "github.com/Omotolani98/meetingctl/cmd/meetingctl@$Version"
  go install "github.com/Omotolani98/meetingctl/cmd/meetingd@$Version"
  go install "github.com/Omotolani98/meetingctl/cmd/meeting-mcp@$Version"
} else {
  throw "go is required to install meetingctl"
}

$KeyFile = Join-Path $DataDir "encryption.key"
if (-not (Test-Path $KeyFile)) {
  $key = & (Join-Path $BinDir "meetingctl.exe") keygen
  Set-Content -Path $KeyFile -Value $key -NoNewline
}

$env:MEETINGCTL_DATA_DIR = $DataDir
$env:MEETINGCTL_ENCRYPTION_KEY = (Get-Content $KeyFile -Raw).Trim()

# Per-user logon task (not a Windows Service — audio needs user session)
$TaskName = "meetingctl-meetingd"
$Exe = Join-Path $BinDir "meetingd.exe"
$Action = New-ScheduledTaskAction -Execute $Exe
$Trigger = New-ScheduledTaskTrigger -AtLogOn
$Settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1)
Register-ScheduledTask -TaskName $TaskName -Action $Action -Trigger $Trigger -Settings $Settings -Force | Out-Null
Start-Process -FilePath $Exe -WindowStyle Hidden

Write-Host "==> installed to $BinDir"
Write-Host "==> data dir $DataDir"
Write-Host "Add $BinDir to PATH, then run: meetingctl doctor"

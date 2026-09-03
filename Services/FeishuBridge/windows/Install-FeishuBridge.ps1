param(
  [ValidatePattern('^[A-Za-z0-9._-]{1,80}$')]
  [string]$TaskName = 'FeishuBotBridge',
  [string]$NodePath = ''
)

$ErrorActionPreference = 'Stop'
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$runScript = Join-Path $PSScriptRoot 'Run-FeishuBridge.ps1'
$resolvedNode = if ($NodePath) { [IO.Path]::GetFullPath($NodePath) } else { (Get-Command node.exe -ErrorAction Stop).Source }
if (-not (Test-Path -LiteralPath $resolvedNode -PathType Leaf)) { throw 'Node runtime is missing.' }
$packagePath = Join-Path $projectRoot 'node_modules\@larksuite\cli\scripts\run.js'
if (-not (Test-Path -LiteralPath $packagePath -PathType Leaf)) {
  throw "Dependencies are missing. Run 'npm ci' in $projectRoot first."
}

. (Join-Path $PSScriptRoot 'Protect-FeishuBridgeData.ps1')
$migration = & (Join-Path $PSScriptRoot 'Migrate-FeishuBridgeData.ps1')
$dataRoot = Get-FeishuBridgeDataRoot -ProjectRoot $projectRoot
Protect-FeishuBridgeDataRoot -Path $dataRoot
Protect-FeishuBridgeDataRoot -Path (Join-Path $projectRoot 'node_modules\.cache\feishu-bridge')

$powerShell = Join-Path $PSHOME 'powershell.exe'
$arguments = '-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "{0}" -NodePath "{1}"' -f $runScript, $resolvedNode
$existing = Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
if ($existing) { Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue }
$taskAction = New-ScheduledTaskAction -Execute $powerShell -Argument $arguments -WorkingDirectory $projectRoot
$currentUser = [Security.Principal.WindowsIdentity]::GetCurrent().Name
$trigger = New-ScheduledTaskTrigger -AtLogOn -User $currentUser
$principal = New-ScheduledTaskPrincipal -UserId $currentUser -LogonType Interactive -RunLevel Limited
$settings = New-ScheduledTaskSettingsSet `
  -AllowStartIfOnBatteries `
  -DontStopIfGoingOnBatteries `
  -StartWhenAvailable `
  -MultipleInstances IgnoreNew `
  -RestartCount 999 `
  -RestartInterval (New-TimeSpan -Minutes 1) `
  -ExecutionTimeLimit ([TimeSpan]::Zero)

Register-ScheduledTask -TaskName $TaskName -Action $taskAction -Trigger $trigger `
  -Principal $principal -Settings $settings -Description 'Feishu Bridge user service' -Force | Out-Null
Start-ScheduledTask -TaskName $TaskName
Write-Host "Feishu Bridge Scheduled Task installed and started: $TaskName"
Write-Host "Private data root: $dataRoot"
Write-Host "Private data migration: $migration"

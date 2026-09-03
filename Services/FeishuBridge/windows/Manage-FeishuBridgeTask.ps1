$ErrorActionPreference = 'Stop'

$action = [string]$env:FEISHU_BRIDGE_TASK_ACTION
$taskName = [string]$env:FEISHU_BRIDGE_TASK_NAME
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$bridgeScripts = @(
  [IO.Path]::GetFullPath((Join-Path $projectRoot 'scripts\start-bridge.js')),
  [IO.Path]::GetFullPath((Join-Path $projectRoot 'bot-bridge.js'))
)
if ($action -notin @('status', 'start', 'restart')) { throw 'Invalid bridge task action.' }
if ($taskName -notmatch '^[A-Za-z0-9._-]{1,80}$') { throw 'Invalid bridge task name.' }

function Get-CurrentBridgeProcesses {
  @(Get-CimInstance Win32_Process -Filter "Name = 'node.exe'" -ErrorAction SilentlyContinue | Where-Object {
    $commandLine = [string]$_.CommandLine
    $bridgeScripts | Where-Object {
      $commandLine.IndexOf($_, [StringComparison]::OrdinalIgnoreCase) -ge 0
    }
  })
}

function Stop-CurrentBridgeProcesses {
  $matches = @(Get-CurrentBridgeProcesses | Sort-Object ProcessId -Descending)
  foreach ($process in $matches) {
    Stop-Process -Id $process.ProcessId -Force -ErrorAction Stop
  }
  if ($matches.Count -gt 0) {
    $deadline = [DateTime]::UtcNow.AddSeconds(5)
    do {
      if ((Get-CurrentBridgeProcesses).Count -eq 0) { break }
      Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $deadline)
    if ((Get-CurrentBridgeProcesses).Count -ne 0) {
      throw 'Validated Feishu Bridge processes did not stop within 5 seconds.'
    }
  }
  $matches.Count
}

$task = Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
if (-not $task) {
  if ($action -ne 'status') { throw "Scheduled Task '$taskName' is not installed." }
  @{ taskName = $taskName; loaded = $false; running = $false } | ConvertTo-Json -Compress
  exit 0
}

if ($action -eq 'restart') {
  if ($task.State -eq 'Running') { Stop-ScheduledTask -TaskName $taskName }
  $stoppedProcessCount = Stop-CurrentBridgeProcesses
  Start-ScheduledTask -TaskName $taskName
} elseif ($action -eq 'start') {
  Start-ScheduledTask -TaskName $taskName
}

if ($action -ne 'status') {
  $deadline = [DateTime]::UtcNow.AddSeconds(10)
  do {
    $runningBridgeProcesses = @(Get-CurrentBridgeProcesses | Where-Object {
      ([string]$_.CommandLine).IndexOf($bridgeScripts[1], [StringComparison]::OrdinalIgnoreCase) -ge 0
    })
    if ($runningBridgeProcesses.Count -gt 0) {
      $bridgeProcessAlive = $true
      break
    }
    Start-Sleep -Milliseconds 200
  } while ([DateTime]::UtcNow -lt $deadline)
  if (-not $bridgeProcessAlive) { throw 'Feishu Bridge process did not become ready within 10 seconds.' }
}

$current = Get-ScheduledTask -TaskName $taskName
@{
  taskName = $taskName
  loaded = $true
  running = ($current.State -eq 'Running')
  state = [string]$current.State
  bridgeProcessAlive = [bool]$bridgeProcessAlive
  stoppedProcessCount = [int]$stoppedProcessCount
} | ConvertTo-Json -Compress

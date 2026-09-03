param(
  [ValidatePattern('^[A-Za-z0-9._-]{1,80}$')]
  [string]$TaskName = 'FeishuBotBridge'
)

$ErrorActionPreference = 'Stop'
$task = Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
if ($task) {
  if ($task.State -eq 'Running') { Stop-ScheduledTask -TaskName $TaskName }
  Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
}
Write-Host "Feishu Bridge Scheduled Task removed: $TaskName"
Write-Host 'Private data and credentials were preserved.'

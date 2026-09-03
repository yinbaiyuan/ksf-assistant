param([string]$NodePath = '')

$ErrorActionPreference = 'Stop'
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$entry = Join-Path $projectRoot 'scripts\start-bridge.js'
$node = if ($NodePath) { [IO.Path]::GetFullPath($NodePath) } else { (Get-Command node.exe -ErrorAction Stop).Source }
if (-not (Test-Path -LiteralPath $node -PathType Leaf)) { throw 'Node runtime is missing.' }
. (Join-Path $PSScriptRoot 'Protect-FeishuBridgeData.ps1')
$dataRoot = Get-FeishuBridgeDataRoot -ProjectRoot $projectRoot
Protect-FeishuBridgeDataRoot -Path $dataRoot
Protect-FeishuBridgeDataRoot -Path (Join-Path $projectRoot 'node_modules\.cache\feishu-bridge')
$hostLogDir = Join-Path $dataRoot 'logs\host'
Protect-FeishuBridgeDataRoot -Path $hostLogDir
$env:FEISHU_BRIDGE_HOST_LOG_DIR = $hostLogDir
Set-Location -LiteralPath $projectRoot
& $node $entry
exit $LASTEXITCODE

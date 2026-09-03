$ErrorActionPreference = 'Stop'

$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
. (Join-Path $PSScriptRoot 'Protect-FeishuBridgeData.ps1')

$sourceRoot = [IO.Path]::GetFullPath((Join-Path $env:LOCALAPPDATA 'FeishuBridge'))
$destinationRoot = Get-FeishuBridgeDataRoot -ProjectRoot $projectRoot
if ($sourceRoot -eq $destinationRoot -or -not (Test-Path -LiteralPath $sourceRoot -PathType Container)) {
  @{ copied = 0; skipped = 0; sourceExists = (Test-Path -LiteralPath $sourceRoot -PathType Container) } |
    ConvertTo-Json -Compress
  exit 0
}

Protect-FeishuBridgeDataRoot -Path $destinationRoot
$copied = 0
$skipped = 0
foreach ($item in Get-ChildItem -LiteralPath $sourceRoot -Force) {
  $target = Join-Path $destinationRoot $item.Name
  if (Test-Path -LiteralPath $target) {
    $skipped += 1
    continue
  }
  Copy-Item -LiteralPath $item.FullName -Destination $target -Recurse
  $copied += 1
}
Protect-FeishuBridgeDataRoot -Path $destinationRoot
@{ copied = $copied; skipped = $skipped; sourceExists = $true } | ConvertTo-Json -Compress

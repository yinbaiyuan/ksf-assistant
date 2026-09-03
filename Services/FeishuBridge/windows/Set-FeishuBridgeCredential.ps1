param(
  [Parameter(Mandatory = $true)]
  [ValidatePattern('^[A-Za-z0-9_-]{3,128}$')]
  [string]$AppId,
  [ValidateSet('feishu', 'lark')]
  [string]$Brand = 'feishu',
  [switch]$AppSecretStdin
)

$ErrorActionPreference = 'Stop'
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
. (Join-Path $PSScriptRoot 'Protect-FeishuBridgeData.ps1')
$dataRoot = Get-FeishuBridgeDataRoot -ProjectRoot $projectRoot
$credentialDir = Join-Path $dataRoot 'credentials'
$credentialPath = Join-Path $credentialDir 'official-sdk.json'
Protect-FeishuBridgeDataRoot -Path $dataRoot
New-Item -ItemType Directory -Path $credentialDir -Force | Out-Null

if ($AppSecretStdin) {
  $plainSecret = [Console]::In.ReadToEnd().Trim()
  if ([string]::IsNullOrWhiteSpace($plainSecret)) {
    throw 'Feishu App Secret stdin is empty'
  }
  $secret = ConvertTo-SecureString $plainSecret -AsPlainText -Force
  $plainSecret = $null
} else {
  $secret = Read-Host 'Feishu App Secret' -AsSecureString
}
$protectedSecret = ConvertFrom-SecureString $secret
@{
  schemaVersion = 1
  appId = $AppId
  brand = $Brand
  protectedSecret = $protectedSecret
} | ConvertTo-Json | Set-Content -LiteralPath $credentialPath -Encoding UTF8
Write-Host "Official SDK credential stored for the current Windows user: $credentialPath"

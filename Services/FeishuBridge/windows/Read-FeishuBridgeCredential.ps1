$ErrorActionPreference = 'Stop'
if ($PSVersionTable.PSEdition -eq 'Desktop' -and $env:PSModulePath) {
  $safeModulePaths = $env:PSModulePath -split [IO.Path]::PathSeparator |
    Where-Object {
      $_ -match '\\WindowsPowerShell\\Modules\\?$' -or
      $_ -match '\\WindowsPowerShell\\v1\.0\\Modules\\?$'
    }
  if ($safeModulePaths.Count -gt 0) {
    $env:PSModulePath = $safeModulePaths -join [IO.Path]::PathSeparator
  }
}
Import-Module Microsoft.PowerShell.Security -ErrorAction Stop

$credentialPath = [string]$env:FEISHU_BRIDGE_CREDENTIAL_FILE
if (-not $credentialPath -or -not (Test-Path -LiteralPath $credentialPath -PathType Leaf)) {
  throw 'Windows bridge credential file is unavailable.'
}
$stored = Get-Content -LiteralPath $credentialPath -Raw | ConvertFrom-Json
$secure = ConvertTo-SecureString ([string]$stored.protectedSecret)
$pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
try {
  $secret = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer)
  @{
    appId = [string]$stored.appId
    appSecret = $secret
    brand = [string]$stored.brand
  } | ConvertTo-Json -Compress
} finally {
  if ($pointer -ne [IntPtr]::Zero) { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer) }
  $secret = $null
}

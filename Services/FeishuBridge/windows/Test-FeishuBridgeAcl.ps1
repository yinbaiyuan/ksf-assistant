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

$dataRoot = [string]$env:FEISHU_BRIDGE_DATA_DIR
if (-not $dataRoot -or -not (Test-Path -LiteralPath $dataRoot -PathType Container)) {
  @{ exists = $false; secure = $false; protected = $false } | ConvertTo-Json -Compress
  exit 0
}

$acl = Get-Acl -LiteralPath $dataRoot
$required = @(
  [Security.Principal.WindowsIdentity]::GetCurrent().User.Value,
  'S-1-5-18',
  'S-1-5-32-544'
)
$allowed = @{}
$unexpected = $false
foreach ($entry in $acl.Access) {
  try {
    $sid = $entry.IdentityReference.Translate([Security.Principal.SecurityIdentifier]).Value
    $fullControl = [Security.AccessControl.FileSystemRights]::FullControl
    if ($entry.AccessControlType -eq 'Allow' -and (($entry.FileSystemRights -band $fullControl) -eq $fullControl)) {
      $allowed[$sid] = $true
    }
    if ($sid -notin $required -or $entry.AccessControlType -ne 'Allow') { $unexpected = $true }
  } catch {}
}
$hasRequired = ($required | Where-Object { -not $allowed.ContainsKey($_) }).Count -eq 0
@{
  exists = $true
  protected = $acl.AreAccessRulesProtected
  secure = ($acl.AreAccessRulesProtected -and $hasRequired -and -not $unexpected)
  requiredPrincipalCount = $required.Count
} | ConvertTo-Json -Compress

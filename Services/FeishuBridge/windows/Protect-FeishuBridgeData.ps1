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

function Protect-FeishuBridgeDataRoot {
  param([Parameter(Mandatory = $true)][string]$Path)

  New-Item -ItemType Directory -Path $Path -Force | Out-Null

  $currentSid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
  $grantRules = @(
    "*${currentSid}:(OI)(CI)F",
    '*S-1-5-18:(OI)(CI)F',
    '*S-1-5-32-544:(OI)(CI)F'
  )
  $icacls = Join-Path $env:SystemRoot 'System32\icacls.exe'
  $result = & $icacls $Path /inheritance:r /grant:r $grantRules 2>&1
  if ($LASTEXITCODE -ne 0) {
    throw "icacls failed: $result"
  }
}

function Get-FeishuBridgeDataRoot {
  param([Parameter(Mandatory = $true)][string]$ProjectRoot)

  $configured = [string]$env:FEISHU_BRIDGE_DATA_DIR
  if (-not $configured) {
    $envFile = Join-Path $ProjectRoot '.env.local'
    if (Test-Path -LiteralPath $envFile -PathType Leaf) {
      foreach ($line in Get-Content -LiteralPath $envFile) {
        $trimmed = $line.Trim()
        if (-not $trimmed -or $trimmed.StartsWith('#')) { continue }
        $separator = $trimmed.IndexOf('=')
        if ($separator -lt 1) { continue }
        if ($trimmed.Substring(0, $separator).Trim() -ne 'FEISHU_BRIDGE_DATA_DIR') { continue }
        $configured = $trimmed.Substring($separator + 1).Trim()
        if (($configured.StartsWith('"') -and $configured.EndsWith('"')) -or
            ($configured.StartsWith("'") -and $configured.EndsWith("'"))) {
          $configured = $configured.Substring(1, $configured.Length - 2)
        }
        break
      }
    }
  }
  if (-not $configured) { return Join-Path $env:USERPROFILE '.config\feishu-bridge' }
  if (-not [IO.Path]::IsPathRooted($configured)) { $configured = Join-Path $ProjectRoot $configured }
  return [IO.Path]::GetFullPath($configured)
}

$ErrorActionPreference = 'Stop'
$endpoint = [string]$env:CODEX_DESKTOP_IPC_PATH
if ($endpoint -notmatch '^\\\\\.\\pipe\\[A-Za-z0-9._-]{1,200}$') {
  @{ ready = $false } | ConvertTo-Json -Compress
  exit 0
}
$pipeName = $endpoint.Substring('\\.\pipe\'.Length)
$ready = $false
try {
  $ready = [IO.Directory]::GetFiles('\\.\pipe\') |
    ForEach-Object { [IO.Path]::GetFileName($_) } |
    Where-Object { $_ -eq $pipeName } |
    Select-Object -First 1
  $ready = [bool]$ready
} catch {
  $ready = $false
}
@{ ready = $ready } | ConvertTo-Json -Compress

$ErrorActionPreference = 'Stop'

$url = [string]$env:CODEX_TASK_URL
if ($url -notmatch '^codex://threads/[A-Za-z0-9%._~-]+$') { throw 'Invalid Codex task URL.' }
Start-Process -FilePath $url

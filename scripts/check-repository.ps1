$ErrorActionPreference = 'Stop'

$tracked = @(git ls-files)
$violations = @(
  $tracked | Where-Object { $_ -match '(^|/)docs/superpowers(/|$)' }
  $tracked | Where-Object { $_ -notmatch '^testdata/' -and $_ -match '(?i)(^|/)[^/]+\.(exe|rewind|pem|key)$' }
  $tracked | Where-Object { $_ -match '(^|/)(rewind\.yaml|\.env(?:\.|$)|coverage\.out|coverage\.html)$' }
  $tracked | Where-Object { $_ -match '(?i)(^|/)(codex_chat|promt|prompt|agent[-_ ]chat)(\.md)?$' }
)

$violations = @($violations | Where-Object { $_ } | Sort-Object -Unique)
if ($violations.Count -gt 0) {
  Write-Host 'Repository hygiene check failed. Remove these tracked local/process artifacts:' -ForegroundColor Red
  $violations | ForEach-Object { Write-Host " - $_" }
  exit 1
}

Write-Host "Repository hygiene check passed ($($tracked.Count) tracked paths inspected)." -ForegroundColor Green

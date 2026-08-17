$ErrorActionPreference = 'Stop'

$required = @(
  'README.md',
  'docs/getting-started.md',
  'docs/investigation-workflow.md',
  'docs/architecture.md',
  'docs/config-reference.md',
  'docs/bundle-spec.md',
  'docs/operations.md',
  'docs/assets/architecture.svg',
  'docs/assets/investigation-flow.svg',
  'docs/assets/ui-demo.svg'
)

$missing = @($required | Where-Object { -not (Test-Path -LiteralPath $_) })
if ($missing.Count -gt 0) {
  Write-Host 'Documentation check failed. Missing required files:' -ForegroundColor Red
  $missing | ForEach-Object { Write-Host " - $_" }
  exit 1
}

$broken = @()
Get-ChildItem -Path . -Recurse -File -Filter '*.md' |
  Where-Object { $_.FullName -notmatch '[\\/]node_modules[\\/]' -and $_.FullName -notmatch '[\\/]\.git[\\/]' } |
  ForEach-Object {
  $file = $_
  $content = Get-Content -LiteralPath $file.FullName -Raw
  $matches = [regex]::Matches($content, '\[[^\]]+\]\(([^)]+)\)')
  foreach ($match in $matches) {
    $target = $match.Groups[1].Value.Trim()
    if ($target -match '^(https?|mailto):' -or $target.StartsWith('#')) { continue }
    $pathPart = ($target -split '#', 2)[0]
    if (-not $pathPart) { continue }
    $candidate = Join-Path $file.DirectoryName $pathPart
    if (-not (Test-Path -LiteralPath $candidate)) { $broken += "$($file.FullName): $target" }
  }
}

if ($broken.Count -gt 0) {
  Write-Host 'Documentation check failed. Broken local links:' -ForegroundColor Red
  $broken | ForEach-Object { Write-Host " - $_" }
  exit 1
}

Write-Host "Documentation check passed ($($required.Count) required assets and local links inspected)." -ForegroundColor Green

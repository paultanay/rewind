[CmdletBinding()]
param(
    [switch]$Keep,
    [int]$PortOffset = 0
)

$ErrorActionPreference = 'Stop'
$root = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$compose = Join-Path $PSScriptRoot 'docker-compose.yml'
$config = Join-Path $PSScriptRoot 'rewind.yaml'
$runDir = Join-Path ([IO.Path]::GetTempPath()) ('rewind-practical-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $runDir | Out-Null
$bundle = Join-Path $runDir 'incident.rewind'
$liveJSON = Join-Path $runDir 'live.json'
$replayJSON = Join-Path $runDir 'replay.json'
$stderr = Join-Path $runDir 'investigate.stderr'
$ports = @{
    checkout = 18080 + $PortOffset
    payments = 18081 + $PortOffset
    prometheus = 19090 + $PortOffset
    alertmanager = 19093 + $PortOffset
    loki = 13100 + $PortOffset
    tempo = 13200 + $PortOffset
}
$envNames = @(
    'REWIND_PRACTICAL_CHECKOUT_PORT', 'REWIND_PRACTICAL_PAYMENTS_PORT',
    'REWIND_PRACTICAL_PROMETHEUS_PORT', 'REWIND_PRACTICAL_ALERTMANAGER_PORT',
    'REWIND_PRACTICAL_LOKI_PORT', 'REWIND_PRACTICAL_TEMPO_PORT',
    'REWIND_PROMETHEUS_URL', 'REWIND_LOKI_URL', 'REWIND_TEMPO_URL',
    'REWIND_ALERTMANAGER_URL'
)
$previousEnv = @{}
foreach ($name in $envNames) { $previousEnv[$name] = [Environment]::GetEnvironmentVariable($name, 'Process') }
$env:REWIND_PRACTICAL_CHECKOUT_PORT = $ports.checkout
$env:REWIND_PRACTICAL_PAYMENTS_PORT = $ports.payments
$env:REWIND_PRACTICAL_PROMETHEUS_PORT = $ports.prometheus
$env:REWIND_PRACTICAL_ALERTMANAGER_PORT = $ports.alertmanager
$env:REWIND_PRACTICAL_LOKI_PORT = $ports.loki
$env:REWIND_PRACTICAL_TEMPO_PORT = $ports.tempo
$env:REWIND_PROMETHEUS_URL = "http://localhost:$($ports.prometheus)"
$env:REWIND_LOKI_URL = "http://localhost:$($ports.loki)"
$env:REWIND_TEMPO_URL = "http://localhost:$($ports.tempo)"
$env:REWIND_ALERTMANAGER_URL = "http://localhost:$($ports.alertmanager)"

function Wait-Http([string]$uri) {
    for ($i = 0; $i -lt 30; $i++) {
        try {
            $response = Invoke-WebRequest -Uri $uri -UseBasicParsing -TimeoutSec 2
            if ($response.StatusCode -ge 200 -and $response.StatusCode -lt 500) { return }
        } catch { }
        Start-Sleep -Seconds 2
    }
    throw "Timed out waiting for $uri"
}

function Wait-Seconds([int]$seconds) {
    for ($i = 0; $i -lt $seconds; $i += 5) { Start-Sleep -Seconds 5 }
}

function Assert-Equal($actual, $expected, [string]$label) {
    if ($actual -ne $expected) { throw "${label}: got $actual, want $expected" }
}

Push-Location $root
try {
    Write-Host 'Starting practical observability stack...'
    docker compose -f $compose up -d --build
    if ($LASTEXITCODE -ne 0) { throw "docker compose failed to start the practical stack (exit code $LASTEXITCODE)" }
    Wait-Http "http://localhost:$($ports.checkout)/health"
    Wait-Http "http://localhost:$($ports.payments)/health"
    Wait-Http "http://localhost:$($ports.prometheus)/-/ready"
    Wait-Http "http://localhost:$($ports.alertmanager)/-/ready"
    Wait-Http "http://localhost:$($ports.loki)/ready"
    Wait-Http "http://localhost:$($ports.tempo)/api/echo"

    $from = [DateTime]::UtcNow.AddSeconds(-5)
    Write-Host 'Collecting baseline telemetry for 60 seconds...'
    Wait-Seconds 60

    Write-Host 'Injecting payments failure...'
    Invoke-WebRequest -Method Post -Uri "http://localhost:$($ports.payments)/admin/fail?enabled=true" -UseBasicParsing | Out-Null
    $failureAt = [DateTime]::UtcNow
    $alertObject = @{
        labels = @{ alertname = 'CheckoutHighErrorRate'; namespace = 'shop'; service = 'checkout'; app = 'checkout'; severity = 'critical' }
        annotations = @{ summary = 'checkout is returning downstream errors'; description = 'Injected failure in practical test' }
        startsAt = $failureAt.ToString('o')
        endsAt = $failureAt.AddHours(1).ToString('o')
        generatorURL = 'http://prometheus:9090/graph'
    }
    $alert = ConvertTo-Json -InputObject @($alertObject) -Depth 8
    Invoke-RestMethod -Method Post -Uri "http://localhost:$($ports.alertmanager)/api/v2/alerts" -ContentType 'application/json' -Body $alert | Out-Null
    Write-Host 'Collecting failure telemetry for 90 seconds...'
    Wait-Seconds 90
    $to = [DateTime]::UtcNow.AddSeconds(5)

    $fromArg = $from.ToString('yyyy-MM-ddTHH:mm:ssZ')
    $toArg = $to.ToString('yyyy-MM-ddTHH:mm:ssZ')
    Write-Host 'Running Rewind live investigation...'
    $nativeErrorAction = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    & go run ./cmd/rewind --config $config investigate --from $fromArg --to $toArg --namespace shop --service checkout --format json --output $bundle 1> $liveJSON 2> $stderr
    $exitCode = $LASTEXITCODE
    $ErrorActionPreference = $nativeErrorAction
    if ($exitCode -ne 0 -and $exitCode -ne 1) { throw "live investigation failed with exit code $exitCode`n$(Get-Content $stderr -Raw)" }
    $live = Get-Content $liveJSON -Raw | ConvertFrom-Json

    $prom = @($live.sources | Where-Object name -eq 'prometheus') | Select-Object -First 1
    $am = @($live.sources | Where-Object name -eq 'alertmanager') | Select-Object -First 1
    if ($null -eq $prom -or $prom.status -notin @('ok','partial') -or [int]$prom.signalCount -lt 1) { throw 'Prometheus did not produce usable signals' }
    if ($null -eq $am -or $am.status -notin @('ok','partial') -or [int]$am.eventCount -lt 1) { throw 'Alertmanager did not produce the injected alert' }
    if ([int]$live.events.Count -lt 1 -or [int]$live.signals.Count -lt 1) { throw 'Live incident has no collected evidence' }
    Write-Host ("Live evidence: {0} entities, {1} events, {2} signals" -f $live.entities.Count, $live.events.Count, $live.signals.Count)

    $entries = @(tar -tzf $bundle)
    foreach ($required in @('incident.json', 'sources/prometheus.json', 'sources/alertmanager.json')) {
        if ($entries -notcontains $required) { throw "Bundle is missing $required" }
    }

    Write-Host 'Stopping observability stack to prove replay is offline...'
    docker compose -f $compose stop | Out-Null
    $nativeErrorAction = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    & go run ./cmd/rewind --config $config investigate --replay $bundle --format json 1> $replayJSON 2> $stderr
    $replayExit = $LASTEXITCODE
    $ErrorActionPreference = $nativeErrorAction
    if ($replayExit -ne 0 -and $replayExit -ne 1) { throw "offline replay failed with exit code $replayExit`n$(Get-Content $stderr -Raw)" }
    $replay = Get-Content $replayJSON -Raw | ConvertFrom-Json
    Assert-Equal $replay.entities.Count $live.entities.Count 'replayed entity count'
    Assert-Equal $replay.events.Count $live.events.Count 'replayed event count'
    Assert-Equal $replay.signals.Count $live.signals.Count 'replayed signal count'
    Write-Host 'OFFLINE_REPLAY_OK'
    Write-Host ("Artifacts: $runDir")
} finally {
    if (-not $Keep) { docker compose -f $compose down --remove-orphans -v | Out-Null }
    foreach ($name in $envNames) {
        if ($null -eq $previousEnv[$name]) { Remove-Item "Env:$name" -ErrorAction SilentlyContinue }
        else { Set-Item "Env:$name" $previousEnv[$name] }
    }
    Pop-Location
}

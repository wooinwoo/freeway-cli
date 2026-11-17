# Run this checkout's installer, authentication, discovery, and a real free-model probe.
$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$provider = if ($env:FREEWAY_PROVIDER) { $env:FREEWAY_PROVIDER } else { 'kilo' }
$model = $env:FREEWAY_MODEL
if (-not $model) {
  if ($provider -ne 'kilo') { throw 'Set FREEWAY_MODEL to a free model from the chosen provider.' }
  $model = 'reflection/beam-free'
}

& (Join-Path $repo 'scripts\install\providers.ps1') -Action install
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
$providerHome = if ($env:FREEWAY_PROVIDER_HOME) { $env:FREEWAY_PROVIDER_HOME } else { Join-Path $HOME '.freeway\providers' }
$providerCli = Join-Path $providerHome 'bin\freeway-providers.ps1'

function Invoke-Provider([string[]]$Arguments) {
  & $providerCli @Arguments
  if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}

Invoke-Provider -Arguments @('auth', $provider)
Invoke-Provider -Arguments @('models', $provider)
Invoke-Provider -Arguments @('quota', $provider)

$probe = @('probe', $provider, '--model', $model, '--timeout', '120')
if ($env:FREEWAY_REASONING_EFFORT) { $probe += @('--reasoning-effort', $env:FREEWAY_REASONING_EFFORT) }
# A wrong response, authentication failure, or exhausted quota must fail this example.
Invoke-Provider -Arguments $probe

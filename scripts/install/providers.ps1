# Build the standalone Go provider tool; vendor installation runs entirely inside Go.
param(
  [ValidateSet('install','update','check')][string]$Action = 'install',
  [string]$Root = $(if ($env:FREEWAY_PROVIDER_HOME) { $env:FREEWAY_PROVIDER_HOME } else { Join-Path $HOME '.freeway\providers' }),
  [switch]$ApiOnly,
  [ValidateSet('opencode','cline')][string]$Only,
  [switch]$Help
)
$ErrorActionPreference = 'Stop'
if ($Help) {
  Write-Output 'Usage: providers.ps1 [-Action install|update|check] [-Root PATH] [-ApiOnly|-Only opencode|cline]'
  Write-Output 'Source checkout requires Go 1.26+. Node and npm are not used.'
  exit 0
}
if ($env:OS -ne 'Windows_NT') { throw 'Use providers.sh on Linux, WSL, or macOS.' }
if ($ApiOnly -and $Only) { throw 'Choose ApiOnly or Only.' }
$Root = [IO.Path]::GetFullPath($Root)
if (Test-Path -LiteralPath $Root) {
  if ((Get-Item -LiteralPath $Root).LinkType) { throw 'Use a dedicated real directory, not a link.' }
}
$repo = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
if ($Root.TrimEnd('\') -eq [IO.Path]::GetPathRoot($Root).TrimEnd('\') -or $Root.TrimEnd('\') -eq $HOME.TrimEnd('\') -or $Root.TrimEnd('\') -eq $repo.TrimEnd('\')) { throw 'Choose a dedicated install directory.' }
$env:FREEWAY_PROVIDER_HOME = $Root
$bin = Join-Path $Root 'bin'
$binary = Join-Path $bin 'freeway-providers.exe'
$arguments = @($Action,'--root',$Root)
if ($ApiOnly) { $arguments += '--api-only' }
if ($Only) { $arguments += @('--only',$Only) }
if ($Action -ne 'check') {
  if (-not (Get-Command go -ErrorAction SilentlyContinue)) { throw 'Install Go 1.26+ from https://go.dev/doc/install/ and rerun this command.' }
  New-Item -ItemType Directory -Path $bin -Force | Out-Null
  $stage = Join-Path $bin ('.provider-build-'+[Guid]::NewGuid().ToString('N'))
  New-Item -ItemType Directory -Path $stage | Out-Null
  try {
    $previousPreference = $ErrorActionPreference
    try {
      $ErrorActionPreference = 'Continue'
      $global:LASTEXITCODE = 1
      & go -C (Join-Path $repo 'backend') build -o (Join-Path $stage 'freeway-providers.exe') ./cmd/providerctl
      $buildExit = $global:LASTEXITCODE
    } finally { $ErrorActionPreference = $previousPreference }
    if ($buildExit -ne 0) { throw 'Go provider tool build failed.' }
    Move-Item -LiteralPath (Join-Path $stage 'freeway-providers.exe') -Destination $binary -Force
  } finally { Remove-Item -LiteralPath $stage -Recurse -Force }
  @'
param([Parameter(ValueFromRemainingArguments=$true)][string[]]$ForwardArgs)
$ErrorActionPreference = 'Continue'
$global:LASTEXITCODE = 1
& (Join-Path $PSScriptRoot 'freeway-providers.exe') @ForwardArgs
exit $global:LASTEXITCODE
'@ | Set-Content -LiteralPath (Join-Path $bin 'freeway-providers.ps1') -Encoding UTF8
  Copy-Item -LiteralPath (Join-Path $bin 'freeway-providers.ps1') -Destination (Join-Path $bin 'freeway-provider-setup.ps1') -Force
}
if (-not (Test-Path -LiteralPath $binary -PathType Leaf)) {
  Write-Output '{"ok":false,"code":"provider_tool_not_installed"}'
  exit 1
}
# Native stderr can contain progress; the native exit code determines success.
$ErrorActionPreference = 'Continue'
$global:LASTEXITCODE = 1
& $binary @arguments
exit $global:LASTEXITCODE

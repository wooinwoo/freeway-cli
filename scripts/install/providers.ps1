param(
  [ValidateSet('install','update','check')][string]$Action = 'install',
  [string]$Root = $(if ($env:FREEWAY_PROVIDER_HOME) { $env:FREEWAY_PROVIDER_HOME } else { Join-Path $HOME '.freeway\providers' }),
  [switch]$ApiOnly,
  [ValidateSet('opencode','cline')][string]$Only,
  [switch]$Help
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
if ($Help) {
  Write-Output 'Usage: providers.ps1 [-Action install|update|check] [-ApiOnly|-Only opencode|cline] [-Root PATH]'
  Write-Output 'Updates apply the tested versions bundled with this Freeway checkout. Check never installs or authenticates.'
  exit 0
}
if ($env:OS -ne 'Windows_NT') { throw 'Use providers.sh on Linux, WSL, or macOS.' }
if ($ApiOnly -and $Only) { throw 'Choose ApiOnly or Only.' }
$Root = [IO.Path]::GetFullPath($Root)
$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
if ($Root.TrimEnd('\') -eq [IO.Path]::GetPathRoot($Root).TrimEnd('\') -or $Root.TrimEnd('\') -eq $HOME.TrimEnd('\') -or $Root.TrimEnd('\') -eq $repoRoot.TrimEnd('\')) { throw 'Choose a dedicated install directory.' }
$pins = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'versions.json') -Raw | ConvertFrom-Json
$version = [string]$pins.node
if ($version -notmatch '^\d+\.\d+\.\d+$') { throw 'Invalid Node version pin.' }
$architecture = [Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
if ($architecture -eq 'x64') { $arch = 'x64' } elseif ($architecture -eq 'arm64') { $arch = 'arm64' } else { throw 'Only x64 and arm64 Windows are supported.' }
$distribution = "node-v$version-win-$arch"
$target = Join-Path $Root "runtime\$distribution"
$node = Join-Path $target 'node.exe'
if (-not (Test-Path -LiteralPath $node -PathType Leaf)) {
  if ($Action -eq 'check') {
    Write-Output '{"ok":false,"command":"check","code":"runtime_not_installed_or_update_required"}'
    exit 1
  }
  New-Item -ItemType Directory -Path (Join-Path $Root 'runtime') -Force | Out-Null
  $lockPath = Join-Path $Root '.runtime-install.lock'
  $lock = [IO.File]::Open($lockPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
  $stage = Join-Path ([IO.Path]::GetTempPath()) ('freeway-node-' + [Guid]::NewGuid().ToString('N'))
  try {
    if (Test-Path -LiteralPath $target) { throw 'Existing runtime directory is incomplete; choose a clean install root.' }
    New-Item -ItemType Directory -Path $stage | Out-Null
    $archive = "$distribution.zip"
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    Write-Host "Installing private Node.js $version..."
    Invoke-WebRequest -UseBasicParsing -Uri "https://nodejs.org/dist/v$version/$archive" -OutFile (Join-Path $stage $archive)
    $sums = (Invoke-WebRequest -UseBasicParsing -Uri "https://nodejs.org/dist/v$version/SHASUMS256.txt").Content
    $match = [regex]::Match($sums, '(?m)^([a-f0-9]{64})\s+' + [regex]::Escape($archive) + '\r?$')
    if (-not $match.Success) { throw 'Missing official checksum.' }
    $actual = (Get-FileHash -LiteralPath (Join-Path $stage $archive) -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actual -ne $match.Groups[1].Value) { throw 'Node checksum mismatch.' }
    Expand-Archive -LiteralPath (Join-Path $stage $archive) -DestinationPath $stage
    & (Join-Path $stage "$distribution\node.exe") --version | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Node runtime verification failed.' }
    Move-Item -LiteralPath (Join-Path $stage $distribution) -Destination $target
  } finally {
    if (Test-Path -LiteralPath $stage) { Remove-Item -LiteralPath $stage -Recurse -Force }
    $lock.Dispose()
    Remove-Item -LiteralPath $lockPath -Force
  }
}
$arguments = @((Join-Path $PSScriptRoot 'providers.mjs'), $Action, '--root', $Root)
if ($ApiOnly) { $arguments += '--api-only' }
if ($Only) { $arguments += @('--only', $Only) }
& $node @arguments
exit $LASTEXITCODE

param([string]$ReleaseBase)
$ErrorActionPreference = 'Stop'
$originalLocation = Get-Location
$repo = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$manifest = Get-Content -LiteralPath (Join-Path $repo 'dist\native\release.json') -Raw | ConvertFrom-Json
$artifact = $manifest.artifacts | Where-Object { $_.os -eq 'windows' -and $_.arch -eq 'amd64' }
$archive = Join-Path $repo ('dist\native\' + ([Uri]$artifact.url).Segments[-1])
$bootstrap = Join-Path $repo 'install.ps1'
$proof = Join-Path ([IO.Path]::GetTempPath()) ('freeway-bootstrap-proof-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $proof | Out-Null
$env:TEMP = $proof
$env:TMP = $proof
$env:USERPROFILE = Join-Path $proof 'profile'
$env:FREEWAY_PROVIDER_HOME = Join-Path $proof 'providers'
$env:PATH = "$env:SystemRoot\System32"
$root = Join-Path $proof 'install with spaces'
try {
  Copy-Item -LiteralPath $archive -Destination (Join-Path $proof 'native.zip')
  Set-Location -LiteralPath $proof
  & $bootstrap -Archive '.\native.zip' -SHA256 $artifact.sha256 -Root '.\install with spaces'
  $launcher = Join-Path $root 'bin\freeway.exe'
  $version = & $launcher --version
  if ($LASTEXITCODE -ne 0 -or "$version".Trim() -ne $manifest.version) { throw 'Windows bootstrap version verification failed.' }
  $providers = (& $launcher providers list) | ConvertFrom-Json
  if ($LASTEXITCODE -ne 0 -or $providers.providers.Count -ne 11) { throw 'Windows provider utility verification failed.' }
  $before = [IO.File]::ReadAllText((Join-Path $root 'install.json'))
  $rejected = $false
  try { & $bootstrap -Archive $archive -SHA256 ('0' * 64) -Root $root } catch { $rejected = $true }
  if (!$rejected -or [IO.File]::ReadAllText((Join-Path $root 'install.json')) -ne $before) { throw 'Invalid checksum changed the Windows installation.' }
  if ($ReleaseBase) {
    $remoteRoot = Join-Path $proof 'download install'
    & $bootstrap -ReleaseUrl "$ReleaseBase/query" -Root $remoteRoot
    $remoteLauncher = Join-Path $remoteRoot 'bin\freeway.exe'
    $remoteVersion = & $remoteLauncher --version
    if ($LASTEXITCODE -ne 0 -or "$remoteVersion".Trim() -ne $manifest.version) { throw 'Signed-query download installation failed.' }
    $remoteBefore = [IO.File]::ReadAllText((Join-Path $remoteRoot 'install.json'))
    foreach ($fixture in @('missing','ambiguous','damaged')) {
      $rejected = $false
      try { & $bootstrap -ReleaseUrl "$ReleaseBase/$fixture" -Root $remoteRoot } catch { $rejected = $true }
      if (!$rejected -or [IO.File]::ReadAllText((Join-Path $remoteRoot 'install.json')) -ne $remoteBefore) { throw "Failed $fixture download changed the Windows installation." }
    }
    & $remoteLauncher uninstall
    if ($LASTEXITCODE -ne 0) { throw 'Windows download installation removal failed.' }
    for ($i=0; $i -lt 100 -and (Test-Path -LiteralPath (Join-Path $remoteRoot 'install.json')); $i++) { Start-Sleep -Milliseconds 200 }
    if (Test-Path -LiteralPath (Join-Path $remoteRoot 'versions')) { throw 'Windows download removal did not complete.' }
  }
  New-Item -ItemType Directory -Path $env:FREEWAY_PROVIDER_HOME | Out-Null
  $marker = Join-Path $env:FREEWAY_PROVIDER_HOME 'preserve.txt'
  Set-Content -LiteralPath $marker -Value 'preserve user data'
  & $launcher uninstall
  if ($LASTEXITCODE -ne 0) { throw 'Windows uninstall failed.' }
  for ($i=0; $i -lt 100 -and (Test-Path -LiteralPath (Join-Path $root 'install.json')); $i++) { Start-Sleep -Milliseconds 200 }
  if ((Test-Path -LiteralPath $launcher) -or (Test-Path -LiteralPath (Join-Path $root 'versions'))) { throw 'Windows removal did not complete.' }
  if (!(Test-Path -LiteralPath $marker)) { throw 'Windows removal deleted user data.' }
  Write-Output 'Windows native bootstrap verification passed.'
} finally {
  Set-Location -LiteralPath $originalLocation.Path
  Start-Sleep -Milliseconds 200
  Remove-Item -LiteralPath $proof -Recurse -Force
}

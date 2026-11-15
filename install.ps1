param([string]$InstallRoot = (Join-Path $env:LOCALAPPDATA 'Programs\Freeway'), [string]$PayloadPath, [switch]$SkipPath)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
if ($env:OS -ne 'Windows_NT') { throw 'This installer supports Windows x64.' }
if (-not [Environment]::Is64BitOperatingSystem) { throw '64-bit Windows is required.' }
$payloadSha = '0aec7d88d80e06398d73fa6a5f42f7df7ba299dedde6c09a47df0e6bc2b99ebb'
$assetUrl = 'https://github.com/wooinwoo/freeway-cli/releases/download/v0.3.0/freeway-windows-x64.zip'
$stageRoot = Join-Path ([IO.Path]::GetTempPath()) ('freeway-install-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $stageRoot -Force | Out-Null
if (-not $PayloadPath) {
  $PayloadPath = Join-Path $stageRoot 'freeway.zip'
  Write-Host 'Downloading Freeway CLI...'
  [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
  Invoke-WebRequest -UseBasicParsing -Uri $assetUrl -OutFile $PayloadPath
}
$actualSha = (Get-FileHash -LiteralPath $PayloadPath -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actualSha -ne $payloadSha) { throw 'Download checksum mismatch. Installation stopped.' }
$unpackRoot = Join-Path $stageRoot 'payload'
Expand-Archive -LiteralPath $PayloadPath -DestinationPath $unpackRoot
$targetRoot = [IO.Path]::GetFullPath($InstallRoot)
if ($targetRoot.TrimEnd('\') -eq [IO.Path]::GetPathRoot($targetRoot).TrimEnd('\')) { throw 'Choose an application directory, not a drive root.' }
New-Item -ItemType Directory -Path $targetRoot -Force | Out-Null
if ($targetRoot.StartsWith($unpackRoot.TrimEnd('\') + '\',[StringComparison]::OrdinalIgnoreCase)) { throw 'Install outside the temporary payload directory.' }
function Copy-FreewayPayload([string]$Source, [string]$Target) {
  Get-ChildItem -LiteralPath $Source -Force | ForEach-Object {
    $destination = Join-Path $Target $_.Name
    if ($_.PSIsContainer) {
      New-Item -ItemType Directory -Path $destination -Force | Out-Null
      Copy-FreewayPayload $_.FullName $destination
    } else {
      $sameFile = (Test-Path -LiteralPath $destination -PathType Leaf) -and ((Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash -eq (Get-FileHash -LiteralPath $destination -Algorithm SHA256).Hash)
      if (-not $sameFile) { Copy-Item -LiteralPath $_.FullName -Destination $destination -Force }
    }
  }
}
Copy-FreewayPayload $unpackRoot $targetRoot
$binRoot = Join-Path $targetRoot 'bin'
if (-not $SkipPath) {
  $currentPath = [Environment]::GetEnvironmentVariable('Path','User')
  $entries = @($currentPath -split ';' | Where-Object { $_ })
  if (-not ($entries | Where-Object { $_.TrimEnd('\') -ieq $binRoot.TrimEnd('\') })) {
    [Environment]::SetEnvironmentVariable('Path',(($entries + $binRoot) -join ';'),'User')
  }
  if (-not (($env:Path -split ';') | Where-Object { $_.TrimEnd('\') -ieq $binRoot.TrimEnd('\') })) { $env:Path += ';' + $binRoot }
  try {
    if (-not ('Freeway.EnvironmentBroadcast' -as [type])) {
      Add-Type -TypeDefinition 'using System; using System.Runtime.InteropServices; namespace Freeway { public static class EnvironmentBroadcast { [DllImport("user32.dll", CharSet=CharSet.Unicode, SetLastError=true)] public static extern IntPtr SendMessageTimeout(IntPtr h, uint m, UIntPtr w, string l, uint flags, uint timeout, out UIntPtr result); } }'
    }
    $messageResult = [UIntPtr]::Zero
    [void][Freeway.EnvironmentBroadcast]::SendMessageTimeout([IntPtr]0xffff,0x1a,[UIntPtr]::Zero,'Environment',2,3000,[ref]$messageResult)
  } catch { }
}
& (Join-Path $targetRoot 'runtime\node.exe') (Join-Path $targetRoot 'cli.mjs') --version
if ($LASTEXITCODE -ne 0) { throw 'Runtime verification failed.' }
Write-Host ''
Write-Host ('Installed: ' + $targetRoot)
Write-Host 'Run: freeway'
Write-Host 'Subagent: freeway agent --json "Your task"'
Write-Host 'MCP: freeway mcp'
Write-Host 'Open a new terminal if the command is not found.'

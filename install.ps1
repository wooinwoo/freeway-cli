# Bootstrap the native client; activation and recovery remain owned by Go.
param(
  [string]$Root = $(if ($env:FREEWAY_INSTALL_ROOT) { $env:FREEWAY_INSTALL_ROOT } else { Join-Path $HOME '.freeway\app' }),
  [switch]$Source,
  [string]$Archive,
  [string]$SHA256,
  [string]$ReleaseUrl = 'https://github.com/wooinwoo/freeway-cli/releases/latest/download',
  [switch]$NoModifyPath,
  [switch]$Help
)
$ErrorActionPreference = 'Stop'
if ($Help) {
  Write-Output 'Usage: install.ps1 [-Source | -Archive PATH -SHA256 HASH] [-Root PATH] [-ReleaseUrl HTTPS_BASE_URL] [-NoModifyPath]'
  Write-Output 'The bin directory is added to the user PATH unless -NoModifyPath is given.'
  Write-Output 'Default: official native release. Source builds require Go 1.26+. Node.js is not used.'
  exit 0
}
if ($env:OS -ne 'Windows_NT') { throw 'Use install.sh on Linux, WSL, or macOS.' }
if ($Source -and ($Archive -or $SHA256)) { throw 'Choose -Source or -Archive.' }
if ([bool]$Archive -ne [bool]$SHA256) { throw '-Archive and -SHA256 must be used together.' }
$Root = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Root)
$architecture = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
switch ($architecture.ToUpperInvariant()) { 'AMD64' { $architecture = 'amd64' }; 'ARM64' { $architecture = 'arm64' }; default { throw 'Unsupported CPU architecture.' } }
$stage = Join-Path ([IO.Path]::GetTempPath()) ('freeway-install-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $stage | Out-Null

function Assert-DownloadUrl([Uri]$Url) {
  if (!$Url.IsAbsoluteUri -or $Url.UserInfo -or $Url.Fragment) { throw 'Invalid release URL.' }
  if ($Url.Scheme -ne 'https' -and !($Url.Scheme -eq 'http' -and $Url.IsLoopback)) { throw 'Release downloads require HTTPS.' }
}

function Get-ReleaseFile([Uri]$Url, [string]$Destination, [long]$Limit) {
  [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
  $initialScheme = $Url.Scheme
  for ($redirect = 0; $redirect -le 10; $redirect++) {
    Assert-DownloadUrl $Url
    if ($initialScheme -eq 'https' -and $Url.Scheme -ne 'https') { throw 'Insecure release redirect.' }
    $request = [Net.HttpWebRequest]::Create($Url)
    $request.AllowAutoRedirect = $false
    $request.Timeout = 30000
    $request.ReadWriteTimeout = 30000
    $request.UserAgent = 'Freeway-Installer'
    $response = $null
    $inputStream = $null
    $outputStream = $null
    try {
      $response = $request.GetResponse()
      $status = [int]$response.StatusCode
      if ($status -in @(301,302,303,307,308)) {
        if (!$response.Headers['Location']) { throw 'Release redirect is missing a destination.' }
        $Url = [Uri]::new($Url, $response.Headers['Location'])
        continue
      }
      if ($status -ne 200) { throw "Release download failed (HTTP $status)." }
      if ($response.ContentLength -gt $Limit) { throw 'Release download exceeds size limit.' }
      $inputStream = $response.GetResponseStream()
      $outputStream = [IO.File]::Create($Destination)
      $buffer = New-Object byte[] 65536
      [long]$total = 0
      while (($read = $inputStream.Read($buffer,0,$buffer.Length)) -gt 0) {
        $total += $read
        if ($total -gt $Limit) { throw 'Release download exceeds size limit.' }
        $outputStream.Write($buffer,0,$read)
      }
      return
    } finally {
      if ($outputStream) { $outputStream.Dispose() }
      if ($inputStream) { $inputStream.Dispose() }
      if ($response) { $response.Dispose() }
    }
  }
  throw 'Too many release redirects.'
}

try {
  if ($Source) {
    if (!$PSScriptRoot -or !(Test-Path -LiteralPath (Join-Path $PSScriptRoot 'cli\go.mod'))) { throw '-Source requires a repository checkout.' }
    $repo = $PSScriptRoot
    $goCommand = Get-Command go -ErrorAction SilentlyContinue
    if (!$goCommand) { throw 'Source installation requires Go 1.26+ from https://go.dev/doc/install/.' }
    $previousPreference = $ErrorActionPreference
    try {
      $ErrorActionPreference = 'Continue'
      $global:LASTEXITCODE = 1
      & $goCommand.Source -C (Join-Path $repo 'cli') run ./cmd/package --root $repo --out (Join-Path $stage 'release') --targets "windows/$architecture" --go $goCommand.Source
      $buildExit = $global:LASTEXITCODE
    } finally { $ErrorActionPreference = $previousPreference }
    if ($buildExit -ne 0) { throw 'Native client build failed.' }
  }
  if (!$Archive) {
    $sumsPath = Join-Path $stage 'SHA256SUMS'
    if ($Source) {
      $sumsPath = Join-Path $stage 'release\SHA256SUMS'
    } else {
      $ReleaseUrl = $ReleaseUrl.TrimEnd('/')
      Assert-DownloadUrl ([Uri]$ReleaseUrl)
      if (([Uri]$ReleaseUrl).Query) { throw 'Release base URL must not contain a query.' }
      try { Get-ReleaseFile ([Uri]"$ReleaseUrl/SHA256SUMS") $sumsPath 1048576 }
      catch { throw 'Native release checksums are unavailable. From a checkout, run .\install.ps1 -Source. No installed version was changed.' }
    }
    $pattern = '^([0-9a-fA-F]{64})\s+\*?(freeway_[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z][0-9A-Za-z.-]*)?_windows_' + $architecture + '\.zip)$'
    $selected = @(foreach ($line in [IO.File]::ReadAllLines($sumsPath)) {
      if ($line -cmatch $pattern) { [pscustomobject]@{ Hash=$Matches[1]; Name=$Matches[2] } }
    })
    if ($selected.Count -ne 1) { throw 'Expected exactly one checksum for this OS and CPU.' }
    $SHA256 = $selected[0].Hash
    if ($Source) {
      $Archive = Join-Path $stage ('release\' + $selected[0].Name)
    } else {
      $Archive = Join-Path $stage $selected[0].Name
      Get-ReleaseFile ([Uri]("$ReleaseUrl/" + $selected[0].Name)) $Archive 268435456
    }
  }
  if ($SHA256 -notmatch '^[0-9a-fA-F]{64}$') { throw 'Expected SHA256 must contain 64 hexadecimal characters.' }
  $Archive = (Resolve-Path -LiteralPath $Archive).ProviderPath
  $archiveStream = [IO.File]::OpenRead($Archive)
  try {
    if ($archiveStream.Length -gt 268435456) { throw 'Archive exceeds size limit.' }
    $hasher = [Security.Cryptography.SHA256]::Create()
    try { $actual = [BitConverter]::ToString($hasher.ComputeHash($archiveStream)).Replace('-', '') }
    finally { $hasher.Dispose() }
  } finally { $archiveStream.Dispose() }
  if ($actual -ine $SHA256) { throw 'Archive SHA256 mismatch. No package executable was run.' }
  Add-Type -AssemblyName System.IO.Compression.FileSystem
  $zip = [IO.Compression.ZipFile]::OpenRead($Archive)
  try {
    $entries = @($zip.Entries | Where-Object { $_.FullName -ceq 'freeway.exe' })
    if ($entries.Count -ne 1 -or $entries[0].Length -le 0 -or $entries[0].Length -gt 67108864) { throw 'Invalid native bootstrap executable.' }
    if ((($entries[0].ExternalAttributes -shr 16) -band 61440) -eq 40960) { throw 'Bootstrap executable must not be a symbolic link.' }
    [IO.Compression.ZipFileExtensions]::ExtractToFile($entries[0], (Join-Path $stage 'freeway.exe'))
  } finally { $zip.Dispose() }
  $previousPreference = $ErrorActionPreference
  try {
    $ErrorActionPreference = 'Continue'
    $global:LASTEXITCODE = 1
    & (Join-Path $stage 'freeway.exe') install --archive $Archive --sha256 $SHA256 --root $Root
    $installExit = $global:LASTEXITCODE
  } finally { $ErrorActionPreference = $previousPreference }
  if ($installExit -ne 0) { throw 'Native installation failed; inspect the native installer error above.' }
  $bin = Join-Path ([IO.Path]::GetFullPath($Root)) 'bin'
  $escapedBin = $bin.Replace("'", "''")
  if (-not $NoModifyPath) {
    # Edit the raw value so %VARIABLE% entries in the user PATH stay unexpanded.
    $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
    try {
      $current = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
      $listed = @($current -split ';' | Where-Object { $_ -and $_.TrimEnd('\') -ieq $bin.TrimEnd('\') })
      if ($listed.Count -eq 0) {
        $kind = if ($key.GetValueNames() -contains 'Path') { $key.GetValueKind('Path') } else { [Microsoft.Win32.RegistryValueKind]::ExpandString }
        $value = $current.TrimEnd(';')
        $value = if ($value) { "$value;$bin" } else { $bin }
        $key.SetValue('Path', $value, $kind)
        # Setting a user variable makes Windows tell new terminals that the environment changed.
        [Environment]::SetEnvironmentVariable('FREEWAY_PATH_REFRESH', '1', 'User')
        [Environment]::SetEnvironmentVariable('FREEWAY_PATH_REFRESH', $null, 'User')
      }
    } finally { $key.Close() }
  }
  if ($NoModifyPath) {
    Write-Output "`nFreeway installed. Add its bin directory to PATH:"
  } else {
    Write-Output "`nFreeway installed and added to your user PATH. Open a new terminal, or run this now:"
  }
  Write-Output ('$env:Path = ' + "'$escapedBin;'" + ' + $env:Path')
  Write-Output 'Then run: freeway'
} finally {
  Remove-Item -LiteralPath $stage -Recurse -Force
}

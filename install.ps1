# FairPlay installer for Windows.
#
#   irm https://raw.githubusercontent.com/Kumarsujay03/fairplay/main/install.ps1 | iex
#
# Downloads the latest release, checks its SHA-256 against the release's
# SHA256SUMS, installs it to %LOCALAPPDATA%\Programs\FairPlay, adds that folder
# to your user PATH and creates a Start menu shortcut. Offers to install ffmpeg
# (needed) and cloudflared (internet sharing) with winget. FairPlay keeps itself
# up to date after that. It does NOT start at login.
#
# Options (set before running):
#   $env:FAIRPLAY_UNINSTALL=1   remove FairPlay (invites and settings are kept)
#   $env:FAIRPLAY_PURGE=1       with UNINSTALL, also delete invites, settings and the video cache
#   $env:FAIRPLAY_YES=1         install missing tools without asking
#   $env:FAIRPLAY_NO_LAUNCH=1   don't start FairPlay afterwards

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'   # Invoke-WebRequest is very slow with the progress bar
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$repo     = 'Kumarsujay03/fairplay'
$dir      = Join-Path $env:LOCALAPPDATA 'Programs\FairPlay'
$exe      = Join-Path $dir 'fairplay.exe'
$shortcut = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs\FairPlay.lnk'

function Remove-FromUserPath($folder) {
    $p = [Environment]::GetEnvironmentVariable('Path', 'User')
    if ($p) {
        $new = ($p -split ';' | Where-Object { $_ -and ($_.TrimEnd('\') -ne $folder.TrimEnd('\')) }) -join ';'
        [Environment]::SetEnvironmentVariable('Path', $new, 'User')
    }
}

if ($env:FAIRPLAY_UNINSTALL) {
    Get-Process fairplay -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
    Remove-Item -Recurse -Force $dir -ErrorAction SilentlyContinue
    Remove-Item -Force $shortcut -ErrorAction SilentlyContinue
    Remove-FromUserPath $dir
    Write-Host 'FairPlay removed.'
    if ($env:FAIRPLAY_PURGE) {
        Remove-Item -Recurse -Force (Join-Path $env:APPDATA 'fairplay') -ErrorAction SilentlyContinue
        Remove-Item -Recurse -Force (Join-Path $env:LOCALAPPDATA 'fairplay') -ErrorAction SilentlyContinue
        Write-Host 'Invites, settings and prepared videos deleted.'
    } else {
        Write-Host 'Your invites and settings are still in' (Join-Path $env:APPDATA 'fairplay')
    }
    return
}

function Ask($question) {
    if ($env:FAIRPLAY_YES) { return $true }
    $r = Read-Host "$question [Y/n]"
    return ($r -eq '' -or $r -match '^(y|yes)$')
}

$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }

# The latest release is found from the github.com/<repo>/releases/latest
# redirect, which (unlike the REST API) has no 60-requests-per-hour limit.
function Get-LatestTag {
    $url = "https://github.com/$repo/releases/latest"
    $loc = $null
    try {
        $req = [System.Net.HttpWebRequest]::Create($url)
        $req.Method = 'HEAD'
        $req.AllowAutoRedirect = $false
        $req.UserAgent = 'fairplay-installer'
        $resp = $req.GetResponse()
        $loc = $resp.Headers['Location']
        $resp.Close()
    } catch [System.Net.WebException] {
        if ($_.Exception.Response) { $loc = $_.Exception.Response.Headers['Location'] }
    }
    if ($loc -match '/releases/tag/(v\d+\.\d+\.\d+)$') { return $Matches[1] }
    $r = Invoke-WebRequest -UseBasicParsing -Method Head -Uri $url
    $final = $null
    if ($r.BaseResponse.ResponseUri) { $final = $r.BaseResponse.ResponseUri.AbsoluteUri }                       # PowerShell 5.1
    elseif ($r.BaseResponse.RequestMessage) { $final = $r.BaseResponse.RequestMessage.RequestUri.AbsoluteUri }  # PowerShell 7
    if ($final -match '/releases/tag/(v\d+\.\d+\.\d+)$') { return $Matches[1] }
    throw "Could not find the latest release (redirect: '$loc', final address: '$final')."
}

Write-Host 'Finding the latest FairPlay release...'
$tag = Get-LatestTag
Write-Host "Latest release: $tag"

Get-Process fairplay -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
$asset = "fairplay-$tag-windows-$arch.exe"
$base  = "https://github.com/$repo/releases/download/$tag"
New-Item -ItemType Directory -Force $dir | Out-Null
$tmp = Join-Path $dir 'fairplay.download'

Write-Host "Downloading $asset ..."
Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset" -OutFile $tmp
$sums = (Invoke-WebRequest -UseBasicParsing -Uri "$base/SHA256SUMS").Content
if ($sums -is [byte[]]) { $sums = [Text.Encoding]::UTF8.GetString($sums) }
$line = ($sums -split "`n") | Where-Object { $_ -match ([regex]::Escape($asset) + '\s*$') } | Select-Object -First 1
if (-not $line) { Remove-Item $tmp; throw "No checksum for $asset in the release." }
$want = ($line -split '\s+')[0].ToLower()
$got  = (Get-FileHash -Algorithm SHA256 $tmp).Hash.ToLower()
if ($want -ne $got) { Remove-Item $tmp; throw "Checksum mismatch for $asset. Not installing." }

Move-Item -Force $tmp $exe
Unblock-File $exe -ErrorAction SilentlyContinue

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not (($userPath -split ';') | Where-Object { $_.TrimEnd('\') -eq $dir })) {
    [Environment]::SetEnvironmentVariable('Path', (($userPath, $dir) | Where-Object { $_ }) -join ';', 'User')
}
$env:Path = "$env:Path;$dir"

$ws = New-Object -ComObject WScript.Shell
$lnk = $ws.CreateShortcut($shortcut)
$lnk.TargetPath = $exe
$lnk.WorkingDirectory = $dir
$lnk.Description = 'Watch videos together, perfectly in sync'
$lnk.Save()

Write-Host ""
Write-Host "FairPlay $tag installed to $exe"

# ---- tools FairPlay uses ----
$winget = Get-Command winget -ErrorAction SilentlyContinue
if (-not (Get-Command ffmpeg -ErrorAction SilentlyContinue)) {
    Write-Host ""
    Write-Host 'FairPlay needs ffmpeg to prepare videos, and it is not installed.'
    if ($winget -and (Ask 'Install it now with winget (Gyan.FFmpeg)?')) {
        winget install --id Gyan.FFmpeg -e --accept-source-agreements --accept-package-agreements
    } else {
        Write-Host 'Install it before starting FairPlay: winget install Gyan.FFmpeg'
    }
}
if (-not (Get-Command cloudflared -ErrorAction SilentlyContinue)) {
    Write-Host ""
    Write-Host 'cloudflared lets friends join over the internet (without it, only your Wi-Fi can join).'
    if ($winget -and (Ask 'Install it now with winget (Cloudflare.cloudflared)?')) {
        winget install --id Cloudflare.cloudflared -e --accept-source-agreements --accept-package-agreements
    }
}

Write-Host ""
Write-Host 'Open FairPlay from the Start menu, or run: fairplay'
Write-Host 'It opens the host console in your browser and updates itself when a new version is out.'
Write-Host 'When Windows Firewall asks, allow FairPlay on Private networks so Wi-Fi guests can join.'

if (-not $env:FAIRPLAY_NO_LAUNCH) {
    Start-Process $exe
}

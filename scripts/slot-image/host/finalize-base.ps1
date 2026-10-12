# finalize-base.ps1 — turn the powered-off install disk into the read-only
# base image and its manifest. Mirrors the validated RUYI-454/RUYI-679
# finalize (Move-Item + Optimize-VHD Full + IsReadOnly + SHA256), and folds
# the seed manifest (pinned versions + guest-script hashes) into an image
# manifest next to the VHDX.
#
# Usage (Hyper-V host, elevated):
#   .\finalize-base.ps1 -InstallDir I:\resource-pool\install `
#     -BaseDir I:\resource-pool\base -ImageVersion 2026.10.11
param(
  [Parameter(Mandatory = $true)][string]$InstallDir,
  [Parameter(Mandatory = $true)][string]$BaseDir,
  [Parameter(Mandatory = $true)][string]$ImageVersion
)
$ErrorActionPreference = 'Stop'

$src = Join-Path $InstallDir 'install-disk.vhdx'
if (-not (Test-Path $src)) { throw "install disk missing: $src (did install-vm.ps1 finish?)" }
$dst = Join-Path $BaseDir ("multica-slot-base-{0}.vhdx" -f $ImageVersion)
if (Test-Path $dst) { throw "base already exists: $dst (bump the image version; never overwrite a read-only base)" }
if (-not (Test-Path $BaseDir)) { New-Item -ItemType Directory -Path $BaseDir | Out-Null }

Move-Item $src $dst
Optimize-VHD -Path $dst -Mode Full | Out-Null
Set-ItemProperty $dst -Name IsReadOnly -Value $true

$vhd = Get-VHD -Path $dst
$file = Get-Item $dst
$vhdx_sha = (Get-FileHash -Path $dst -Algorithm SHA256).Hash.ToLower()

$seed = $null
# seed-manifest.json rides the seed dir the operator copied next to the
# install assets (install\seed\); if absent, the manifest records that
# honestly instead of inventing versions.
$seedCopy = Join-Path $InstallDir 'seed\seed-manifest.json'
if (Test-Path $seedCopy) { $seed = Get-Content $seedCopy -Raw | ConvertFrom-Json }

$manifest = [ordered]@{
  schema          = 'multica-slot-base-manifest v1'
  image           = 'multica-slot-base'
  version         = $ImageVersion
  created_at      = (Get-Date).ToUniversalTime().ToString('o')
  output          = [ordered]@{
    path              = $dst
    file_size_bytes   = $file.Length
    logical_size_gb   = [int]($vhd.SizeBytes / 1GB)
    minimum_size_gb   = [math]::Round($vhd.MinimumSize / 1GB, 2)
    is_read_only      = [bool]$file.IsReadOnly
    sha256            = $vhdx_sha
  }
  seed            = $seed
}
$manifestPath = Join-Path $BaseDir ("multica-slot-base-{0}.manifest.json" -f $ImageVersion)
$manifest | ConvertTo-Json -Depth 6 | Set-Content -Path $manifestPath -Encoding utf8

@{ manifest = $manifestPath; vhdx = $dst; sha256 = $vhdx_sha;
   logical_size_gb = $manifest.output.logical_size_gb } | ConvertTo-Json -Compress
'FINALIZED'

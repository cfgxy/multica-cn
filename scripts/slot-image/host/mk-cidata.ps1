# mk-cidata.ps1 — build the CIDATA seed VHD (FAT32, label CIDATA) holding the
# rendered user-data / meta-data / seed-manifest.json. Subiquity discovers the
# labelled volume at boot and runs the autoinstall unattended (validated path
# from the RUYI-454/RUYI-679 base preparation; mirrors I:\resource-pool\install\mk-cidata.ps1).
#
# Usage (Hyper-V host, elevated):
#   .\mk-cidata.ps1 -InstallDir I:\resource-pool\install -SeedDir .\seed
# Idempotent: recreates cidata.vhdx each run.
param(
  [Parameter(Mandatory = $true)][string]$InstallDir,
  [Parameter(Mandatory = $true)][string]$SeedDir
)
$ErrorActionPreference = 'Stop'

foreach ($f in 'user-data', 'meta-data', 'seed-manifest.json') {
  if (-not (Test-Path (Join-Path $SeedDir $f))) { throw "seed missing: $f" }
}

$vhd = Join-Path $InstallDir 'cidata.vhdx'
if (Test-Path $vhd) { Dismount-VHD -Path $vhd -ErrorAction SilentlyContinue; Remove-Item $vhd -Force }
New-VHD -Path $vhd -SizeBytes 256MB -Fixed | Out-Null
Mount-VHD -Path $vhd
try {
  $disk = (Get-VHD -Path $vhd).DiskNumber
  Initialize-Disk -Number $disk -PartitionStyle GPT
  $part = New-Partition -DiskNumber $disk -UseMaximumSize -AssignDriveLetter
  Format-Volume -Partition $part -FileSystem FAT32 -NewFileSystemLabel 'CIDATA' | Out-Null
  Start-Sleep -Seconds 2
  $dl = $part.DriveLetter
  if (-not $dl) { $dl = (Get-Partition -DiskNumber $disk | Where-Object DriveLetter).DriveLetter }
  if (-not $dl) { throw 'cidata drive letter missing' }
  foreach ($f in 'user-data', 'meta-data', 'seed-manifest.json') {
    Copy-Item (Join-Path $SeedDir $f) ("{0}:\{1}" -f $dl, $f) -Force
  }
  Get-ChildItem ("{0}:\" -f $dl) | Select-Object Name, Length | ConvertTo-Json -Compress
} finally {
  Dismount-VHD -Path $vhd
}
'CIDATA_OK'

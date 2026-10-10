# install-vm.ps1 — create the throwaway install VM, attach the target disk +
# CIDATA seed + Ubuntu Server ISO (first boot device), start it, and wait for
# autoinstall to power the VM off (user-data: shutdown: poweroff). Mirrors the
# validated RUYI-454/RUYI-679 sequence (I:\resource-pool\install\install-vm.ps1)
# plus a bounded wait so the bake is one command, not one vigil.
#
# Usage (Hyper-V host, elevated):
#   .\install-vm.ps1 -InstallDir I:\resource-pool\install `
#     -IsoPath I:\resource-pool\iso\ubuntu-24.04.5-live-server-amd64.iso `
#     [-IsoSha256 <sha>] [-VmName multica-slot-base-install] [-WaitMinutes 60]
param(
  [Parameter(Mandatory = $true)][string]$InstallDir,
  [Parameter(Mandatory = $true)][string]$IsoPath,
  [string]$IsoSha256 = '',
  [string]$VmName = 'multica-slot-base-install',
  [int]$DiskGb = 80,
  [int]$CpuCount = 4,
  [int]$MemoryGb = 4,
  [string]$SwitchName = 'Default Switch',
  [int]$WaitMinutes = 60
)
$ErrorActionPreference = 'Stop'

# ISO integrity gate: refuse to bake from an unverified image.
if ($IsoSha256 -ne '') {
  $actual = (Get-FileHash -Path $IsoPath -Algorithm SHA256).Hash.ToLower()
  if ($actual -ne $IsoSha256.ToLower()) { throw "ISO sha256 mismatch: expected $IsoSha256 got $actual" }
}

$disk = Join-Path $InstallDir 'install-disk.vhdx'
$vm = Get-VM -Name $VmName -ErrorAction SilentlyContinue
if ($vm) {
  if ($vm.State.ToString() -ne 'Off') { Stop-VM -Name $VmName -TurnOff -Force | Out-Null }
  Remove-VM -Name $VmName -Force | Out-Null
}
if (Test-Path $disk) { Remove-Item $disk -Force }

New-VHD -Path $disk -SizeBytes ("{0}GB" -f $DiskGb) -Dynamic | Out-Null
New-VM -Name $VmName -Generation 2 -MemoryStartupBytes ([int64]$MemoryGb * 1GB) -NoVHD | Out-Null
Set-VM -Name $VmName -CheckpointType Disabled -AutomaticStartAction Nothing -AutomaticStopAction TurnOff | Out-Null
Set-VMProcessor -VMName $VmName -Count $CpuCount | Out-Null
Set-VMMemory -VMName $VmName -DynamicMemoryEnabled $false | Out-Null
Set-VMFirmware -VMName $VmName -EnableSecureBoot Off | Out-Null
Connect-VMNetworkAdapter -VMName $VmName -SwitchName $SwitchName | Out-Null
Add-VMHardDiskDrive -VMName $VmName -Path $disk | Out-Null
Add-VMHardDiskDrive -VMName $VmName -Path (Join-Path $InstallDir 'cidata.vhdx') | Out-Null
$dvd = Add-VMDvdDrive -VMName $VmName -Path $IsoPath
Set-VMFirmware -VMName $VmName -FirstBootDevice $dvd | Out-Null

(Get-Date).ToString('s') | Out-File (Join-Path $InstallDir 'started-at.txt') -Encoding ascii
Start-VM -Name $VmName | Out-Null

# autoinstall ends with shutdown: poweroff — wait for Off, bounded.
$deadline = (Get-Date).AddMinutes($WaitMinutes)
while ((Get-VM -Name $VmName).State.ToString() -ne 'Off') {
  if ((Get-Date) -gt $deadline) { throw "install VM did not power off within $WaitMinutes minutes — inspect with vmconnect" }
  Start-Sleep -Seconds 20
}
(Get-VM -Name $VmName) | Select-Object Name, State, ProcessorCount, MemoryAssigned | ConvertTo-Json -Compress
'INSTALL_VM_DONE'

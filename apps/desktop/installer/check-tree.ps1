# Supplied as build-time literals by setup.nsi.
param([int] $ProfilesOnly)
$ErrorActionPreference = 'Stop'
$hklm = [Microsoft.Win32.RegistryKey]::OpenBaseKey('LocalMachine','Registry64')
$base = $hklm.OpenSubKey('Software\Microsoft\Windows\CurrentVersion').GetValue('ProgramFilesDir')
$trusted = 'S-1-5-18','S-1-5-32-544','S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464'
function Check-PathAcl($path) {
    $item = Get-Item -LiteralPath $path -Force
    if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Linked path' }
    $acl = Get-Acl -LiteralPath $path
    if ($acl.GetOwner([Security.Principal.SecurityIdentifier]).Value -notin $trusted) { throw 'Unexpected owner' }
    if ($null -eq ([Security.AccessControl.RawSecurityDescriptor]::new($acl.Sddl)).DiscretionaryAcl) { throw 'Unrestricted DACL' }
    foreach ($rule in $acl.GetAccessRules($true,$true,[Security.Principal.SecurityIdentifier])) {
        if ($rule.AccessControlType -eq 'Allow' -and !($rule.PropagationFlags -band 2) -and
            $rule.IdentityReference.Value -notin $trusted -and ([int64]$rule.FileSystemRights -band 0x500D0156)) {
            throw 'Writable by an untrusted principal'
        }
    }
}
function Check-Tree($path) {
    Check-PathAcl $path
    if ([IO.Directory]::Exists($path)) {
        foreach ($child in Get-ChildItem -LiteralPath $path -Force) { Check-Tree $child.FullName }
    }
}
if (!$ProfilesOnly) {
    Check-PathAcl $base
    $root = Join-Path $base 'Nexus'
    if (Test-Path -LiteralPath $root) { Check-Tree $root }
}

# After the original user has run --cleanup, require outstanding WinINet
# journals in other local profiles to be recovered by their respective owners.
$principal = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
if ($ProfilesOnly -and $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    $profiles = $hklm.OpenSubKey('SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList')
    foreach ($sid in $profiles.GetSubKeyNames()) {
        if ($sid -notlike 'S-1-5-21-*' -and $sid -notlike 'S-1-12-1-*') { continue }
        $profile = $profiles.OpenSubKey($sid).GetValue('ProfileImagePath',$null,'DoNotExpandEnvironmentNames')
        $profile = $profile -replace '^%SystemDrive%',([IO.Path]::GetPathRoot($base).TrimEnd('\'))
        if ($profile -notmatch '^[a-zA-Z]:\\' -or $profile.Contains('%')) { throw 'Unsupported profile location' }
        $part = [IO.Path]::GetPathRoot($profile)
        if ([IO.DriveInfo]::new($part).DriveType -ne 'Fixed') { throw 'Non-local profile' }
        $journal = Join-Path $profile 'AppData\Local\Nexus\desktop\wininet.journal'
        foreach ($name in $journal.Substring($part.Length).Split('\')) {
            $part = Join-Path $part $name
            if (!(Test-Path -LiteralPath $part)) { break }
            if ((Get-Item -Force -LiteralPath $part).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Linked profile' }
            if ($part -eq $journal) { throw 'WinINet recovery required' }
        }
    }
}

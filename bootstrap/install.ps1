#requires -Version 5.1
<#
.SYNOPSIS
Installs reviewed Midden release assets without a build toolchain.
.DESCRIPTION
Standalone: a saved script accepts parameters; irm URL | iex installs the UI
and runs it in the foreground. UI/core need only Windows PowerShell 5.1+ or
PowerShell 7. CLI mode requires an existing Python 3.9+ interpreter and delegates
to the verified bundle's offline installer.

The default directory is LOCALAPPDATA\Programs\Midden. Unless -NoPath is set,
UI/core installation adds it to the user's PATH, never the machine PATH.
PATH receipts retain the raw registry value, kind and presence without expanding
tokens. Committed PATH changes and restored states notify desktop processes with
WM_SETTINGCHANGE(Environment), using a five-second per-receiver timeout.
Notification failure warns without undoing committed files or raw registry state.
No execution-policy change, elevation, credentials or model configuration occurs.
Relative paths use the current PowerShell filesystem location. Installation roots
must not overlap default or explicitly selected core/UI data directories.
Application data and sources are never removed. Unsigned executables may trigger
Windows publisher warnings. Local checksums prove integrity, not authenticity.
.PARAMETER DistributionDir
Uses a reviewed local build instead of the network. CLI verify/uninstall require
this explicitly, with a bundle version matching the existing CLI receipt.
.PARAMETER DryRun
Checks metadata, archives and local UI/core ownership in memory. Prints JSON;
does not extract, write, probe executables, change PATH or launch the application.
CLI backend ownership/prerequisite checks are deferred until applying.
.PARAMETER NoPath
Leaves user and process PATH unchanged. CLI mode always leaves PATH unchanged.
.PARAMETER NoLaunch
Does not start the installed UI. Core/CLI modes never launch the application.
.PARAMETER NoOpen
Passes --no-open to the foreground UI; does not change application settings.
.EXAMPLE
.\install.ps1 -DistributionDir 'C:\midden-release' -InstallDir 'C:\tools\Midden' -NoPath -NoLaunch
.EXAMPLE
.\install.ps1 -Mode cli -ProjectDir 'C:\work\example' -Host claude -NoPath
#>
[CmdletBinding()]
param(
    [ValidateSet('ui', 'core', 'cli')][string]$Mode = 'ui',
    [string]$Version = 'latest',
    [string]$InstallDir = '',
    [string]$ProjectDir = (Get-Location).ProviderPath,
    [Alias('Host')][ValidateSet('copilot', 'claude', 'agents')][string]$CliHost = 'copilot',
    [string]$DistributionDir = '',
    [string]$Repository = 'xibodev/midden',
    [switch]$NoPath,
    [switch]$NoLaunch,
    [switch]$NoOpen,
    [switch]$Upgrade,
    [switch]$Verify,
    [switch]$Uninstall,
    [switch]$DryRun
)

& {
    param($Options)
    Set-StrictMode -Version 3
    $ErrorActionPreference = 'Stop'
    $metadataLimit = 1MB
    $archiveLimit = 256MB
    $memberLimit = 4096
    $receiptName = '.midden-bootstrap-receipt.json'
    $lockName = '.midden-bootstrap.lock'
    $pathSchema = 'midden.user-path/v1'
    $utf8 = New-Object System.Text.UTF8Encoding($false, $true)
    Add-Type -AssemblyName System.IO.Compression

    function New-Map {
        return ,(New-Object 'System.Collections.Generic.Dictionary[string,object]' ([StringComparer]::Ordinal))
    }

    function Get-Digest([byte[]]$Bytes) {
        $hasher = [Security.Cryptography.SHA256]::Create()
        try { return ([BitConverter]::ToString($hasher.ComputeHash($Bytes))).Replace('-', '').ToLowerInvariant() }
        finally { $hasher.Dispose() }
    }

    function Assert-Hash($Value) {
        if ($Value -isnot [string] -or $Value -cnotmatch '^[0-9a-fA-F]{64}$') {
            throw 'Invalid SHA-256 checksum'
        }
        return $Value.ToLowerInvariant()
    }

    function Assert-Version($Value) {
        if ($Value -isnot [string] -or $Value.Length -gt 128 -or
            $Value -cnotmatch '^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.+-]+)?$') {
            throw 'Invalid release version; use latest or a version without the v prefix'
        }
        return $Value
    }

    function Get-SafePath([string]$Value) {
        if ([string]::IsNullOrWhiteSpace($Value) -or $Value -match '[\x00-\x1f<>";|?*]' -or
            $Value.StartsWith('\\')) { throw 'Unsafe local path' }
        $provider = $null
        $drive = $null
        $resolved = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath(
            $Value, [ref]$provider, [ref]$drive)
        if ($provider.Name -ne 'FileSystem' -or $resolved.StartsWith('\\')) {
            throw 'Only local filesystem paths are supported'
        }
        $path = [IO.Path]::GetFullPath($resolved)
        if ($path.Substring(2).Contains(':')) { throw 'Alternate data streams are not paths' }
        $cursor = $path
        while ($cursor) {
            if (Test-Path -LiteralPath $cursor) {
                $item = Get-Item -LiteralPath $cursor -Force
                if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) {
                    throw "Refusing linked/reparse path: $cursor"
                }
                if ($cursor -ne $path -and -not $item.PSIsContainer) {
                    throw "A path ancestor is not a directory: $cursor"
                }
            }
            $next = [IO.Path]::GetDirectoryName($cursor)
            if ($next -eq $cursor) { break }
            $cursor = $next
        }
        return $path.TrimEnd('\')
    }

    function Test-Within([string]$Path, [string]$Root) {
        return $Path.Equals($Root, [StringComparison]::OrdinalIgnoreCase) -or
            $Path.StartsWith($Root.TrimEnd('\') + '\', [StringComparison]::OrdinalIgnoreCase)
    }

    function Assert-Relative([string]$Name) {
        if (-not $Name -or $Name.Length -gt 240 -or $Name -match '[\\:\x00-\x1f\x7f<>"|?*]' -or
            $Name.StartsWith('/') -or $Name.EndsWith('/')) { throw "Unsafe archive/receipt path: $Name" }
        foreach ($part in $Name.Split('/')) {
            if (-not $part -or $part -in @('.', '..') -or $part -match '[. ]$' -or
                $part -match '^(?i:con|prn|aux|nul|conin\$|conout\$|com[1-9\u00b9\u00b2\u00b3]|lpt[1-9\u00b9\u00b2\u00b3])(?:\.|$)') {
                throw "Unsafe archive/receipt path: $Name"
            }
        }
        return $Name
    }

    function Assert-Paths($Names) {
        $seen = New-Object 'System.Collections.Generic.Dictionary[string,string]' ([StringComparer]::OrdinalIgnoreCase)
        $directories = New-Object 'System.Collections.Generic.Dictionary[string,string]' ([StringComparer]::OrdinalIgnoreCase)
        foreach ($name in $Names) {
            $null = Assert-Relative $name
            if ($seen.ContainsKey($name) -or $directories.ContainsKey($name)) {
                throw "Duplicate or case-colliding archive/receipt path: $name"
            }
            $parent = $name
            while ($parent.Contains('/')) {
                $parent = $parent.Substring(0, $parent.LastIndexOf('/'))
                if ($seen.ContainsKey($parent) -or
                    ($directories.ContainsKey($parent) -and $directories[$parent] -cne $parent)) {
                    throw "Conflicting archive/receipt path: $name"
                }
                $directories[$parent] = $parent
            }
            $seen.Add($name, $name)
        }
    }

    function Read-Bounded($Stream, [long]$Limit) {
        $memory = New-Object IO.MemoryStream
        $buffer = New-Object byte[] 81920
        try {
            while (($read = $Stream.Read($buffer, 0, $buffer.Length)) -gt 0) {
                if ($memory.Length + $read -gt $Limit) { throw 'Input size limit exceeded' }
                $memory.Write($buffer, 0, $read)
            }
            return ,$memory.ToArray()
        } finally { $memory.Dispose() }
    }

    function Read-Local([string]$Path, [long]$Limit = $archiveLimit) {
        $path = Get-SafePath $Path
        if (-not [IO.File]::Exists($path)) { throw "Expected a regular file: $path" }
        $stream = [IO.File]::Open($path, 'Open', 'Read', 'Read')
        try {
            if ($stream.Length -gt $Limit) { throw "Input size limit exceeded: $path" }
            return ,(Read-Bounded $stream $Limit)
        } finally { $stream.Dispose() }
    }

    function Get-RemoteBytes([string]$Url, [long]$Limit) {
        $uri = [Uri]$Url
        for ($redirect = 0; $redirect -le 5; $redirect++) {
            if ($uri.Scheme -ne 'https' -or $uri.UserInfo -or
                ($uri.Host -notin @('github.com', 'api.github.com') -and
                 -not $uri.Host.EndsWith('.githubusercontent.com', [StringComparison]::OrdinalIgnoreCase))) {
                throw 'Refusing a non-GitHub or non-HTTPS release URL'
            }
            $request = [Net.WebRequest]::Create($uri)
            $request.Method = 'GET'
            $request.Timeout = 30000
            $request.Credentials = $null
            $request.UseDefaultCredentials = $false
            $request.UserAgent = 'Midden-bootstrap'
            $request.AllowAutoRedirect = $false
            $response = $request.GetResponse()
            try {
                $status = [int]$response.StatusCode
                if ($status -in @(301, 302, 303, 307, 308)) {
                    $uri = New-Object Uri($uri, $response.Headers['Location'])
                    continue
                }
                if ($status -ne 200) { throw "Release download failed with HTTP $status" }
                if ($response.ContentLength -gt $Limit) { throw 'Download size limit exceeded' }
                $stream = $response.GetResponseStream()
                try { return ,(Read-Bounded $stream $Limit) }
                finally { $stream.Dispose() }
            } finally { $response.Dispose() }
        }
        throw 'Release download exceeded the redirect limit'
    }

    function Convert-Json([byte[]]$Bytes) {
        return $utf8.GetString($Bytes) | ConvertFrom-Json
    }

    function Json-Bytes($Value) {
        return ,$utf8.GetBytes(($Value | ConvertTo-Json -Depth 20) + "`n")
    }

    function Get-PropertyMap($Value) {
        if ($null -eq $Value -or $Value -isnot [pscustomobject]) { throw 'Expected a JSON object' }
        $map = New-Map
        foreach ($property in $Value.PSObject.Properties) { $map.Add($property.Name, $property.Value) }
        return ,$map
    }

    function Assert-UserPathState($State) {
        $map = if ($State -is [Collections.IDictionary]) { $State } else { Get-PropertyMap $State }
        Assert-Keys @($map.Keys) @('exists', 'kind', 'value') 'User PATH state'
        if ($map['exists'] -isnot [bool]) { throw 'Invalid user PATH presence' }
        if ($map['exists']) {
            if ($map['kind'] -cnotin @('String', 'ExpandString') -or $map['value'] -isnot [string]) {
                throw 'Unsupported user PATH registry kind/value; only String and ExpandString are supported'
            }
        } elseif ($null -ne $map['kind'] -or $null -ne $map['value']) {
            throw 'An absent user PATH must have no kind or value'
        }
    }

    function Get-UserPathState {
        $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $false)
        try {
            if ($null -eq $key -or 'Path' -notin @($key.GetValueNames())) {
                return @{ exists = $false; kind = $null; value = $null }
            }
            $state = @{
                exists = $true; kind = $key.GetValueKind('Path').ToString()
                value = $key.GetValue('Path', $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
            }
            Assert-UserPathState $state
            return $state
        } finally { if ($null -ne $key) { $key.Dispose() } }
    }

    function Set-UserPathState($State) {
        Assert-UserPathState $State
        $key = $null
        try {
            if ($State.exists) {
                $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
                $kind = [Microsoft.Win32.RegistryValueKind][Enum]::Parse([Microsoft.Win32.RegistryValueKind], $State.kind)
                $key.SetValue('Path', $State.value, $kind)
            } else {
                $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
                if ($null -ne $key) { $key.DeleteValue('Path', $false) }
            }
        } finally { if ($null -ne $key) { $key.Dispose() } }
    }

    function Send-UserPathNotification([string]$Outcome) {
        $failure = $null
        try {
            if (-not ('MiddenEnvironmentNotification' -as [type])) {
                Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class MiddenEnvironmentNotification {
    [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    static extern IntPtr SendMessageTimeout(IntPtr window, uint message, UIntPtr wParam,
        string lParam, uint flags, uint timeout, out UIntPtr result);
    public static int Broadcast(string section, uint timeout) {
        UIntPtr result;
        if (SendMessageTimeout(new IntPtr(0xffff), 0x001a, UIntPtr.Zero,
            section, 0x0002, timeout, out result) != IntPtr.Zero) return 0;
        int error = Marshal.GetLastWin32Error();
        return error == 0 ? 1460 : error;
    }
}
'@
            }
            $errorCode = [MiddenEnvironmentNotification]::Broadcast('Environment', 5000)
            if ($errorCode -ne 0) { $failure = "Win32=$errorCode; timeout=5000ms per receiver" }
        } catch { $failure = $_.Exception.GetBaseException().Message }
        if ($failure) {
            # Notification is best-effort after commit; it must not trigger a stale registry rollback.
            Write-Warning -WarningAction Continue (
                "WM_SETTINGCHANGE(Environment) failed after user PATH $Outcome ($failure). " +
                'The resulting registry state was not rolled back; existing desktop processes may need restarting.'
            )
        } else {
            Write-Verbose "WM_SETTINGCHANGE(Environment) completed after user PATH $Outcome (timeout=5000ms per receiver)."
        }
    }

    function Test-UserPathState($First, $Second) {
        return $First.exists -eq $Second.exists -and $First.kind -ceq $Second.kind -and $First.value -ceq $Second.value
    }

    function New-UserPathChange($Before) {
        Assert-UserPathState $Before
        $prefix = if ($Before.exists -and $Before.value.Length) { $Before.value + ';' } else { '' }
        return @{
            schema = $pathSchema; before = $Before
            after = @{
                exists = $true
                kind = $(if ($Before.exists) { $Before.kind } else { 'String' })
                value = $prefix + $installRoot
            }
        }
    }

    function Test-PathContainsInstall($State) {
        if (-not $State.exists) { return $false }
        foreach ($entry in $State.value.Split(';')) {
            $value = if ($State.kind -eq 'ExpandString') { [Environment]::ExpandEnvironmentVariables($entry) } else { $entry }
            if ($value.TrimEnd('\') -ieq $installRoot) { return $true }
        }
        return $false
    }

    function Assert-Keys($Actual, $Expected, [string]$Label) {
        $left = @($Actual | Sort-Object -CaseSensitive)
        $right = @($Expected | Sort-Object -CaseSensitive)
        if ($left.Count -ne $right.Count -or
            [string]::Join("`n", [string[]]$left) -cne [string]::Join("`n", [string[]]$right)) {
            throw "$Label inventory mismatch"
        }
    }

    function Get-Asset([string]$Name, [long]$Limit) {
        if ($distribution) { return ,(Read-Local (Join-Path $distribution $Name) $Limit) }
        return ,(Get-RemoteBytes ($downloadBase + '/' + $Name) $Limit)
    }

    function Read-Release {
        $sums = New-Map
        $checksumBytes = Get-Asset 'SHA256SUMS' $metadataLimit
        $sumText = $utf8.GetString($checksumBytes)
        foreach ($line in $sumText.TrimEnd("`r", "`n").Split("`n")) {
            if ($line.TrimEnd("`r") -cnotmatch '^([0-9a-fA-F]{64})  ([^/\\]+)$') {
                throw 'Invalid SHA256SUMS entry'
            }
            $hash, $name = $Matches[1], $Matches[2]
            $null = Assert-Relative $name
            if ($sums.ContainsKey($name)) { throw 'Duplicate checksum filename' }
            $sums.Add($name, $hash.ToLowerInvariant())
        }
        Assert-Paths @($sums.Keys)
        $metadata = New-Map
        $metadata.Add('SHA256SUMS', $checksumBytes)
        foreach ($name in @('build-manifest.json', 'manifest.tsv')) {
            if (-not $sums.ContainsKey($name)) { throw "Missing checksum: $name" }
            $bytes = Get-Asset $name $metadataLimit
            if ((Get-Digest $bytes) -cne $sums[$name]) { throw "Metadata checksum mismatch: $name" }
            $metadata.Add($name, $bytes)
        }
        $build = Convert-Json $metadata['build-manifest.json']
        $releaseVersion = Assert-Version $build.version
        if ($requestedVersion -ne 'latest' -and $requestedVersion -cne $releaseVersion) {
            throw 'Build manifest version does not match the requested release'
        }
        if ($build.commit -isnot [string] -or $build.commit -cnotmatch '^(?:[0-9a-f]{40}|[0-9a-f]{64})$') {
            throw 'Invalid build source commit'
        }
        Assert-Keys @($build.products) @('core', 'bundle', 'ui') 'Release products'
        Assert-Keys @($build.installers) @('install.ps1', 'install.sh') 'Release installers'
        $targets = @($build.targets)
        if (-not $targets.Count) { throw 'Release has no native targets' }
        $expectedArchives = New-Map
        $coreHashes = Get-PropertyMap $build.core_binaries
        $uiHashes = Get-PropertyMap $build.ui_binaries
        Assert-Keys @($coreHashes.Keys) $targets 'Core binary'
        Assert-Keys @($uiHashes.Keys) $targets 'UI binary'
        foreach ($target in $targets) {
            if ($target -cnotin @('windows/amd64', 'linux/amd64', 'linux/arm64', 'darwin/amd64', 'darwin/arm64')) {
                throw 'Unsupported release target'
            }
            $null = Assert-Hash $coreHashes[$target]
            $null = Assert-Hash $uiHashes[$target]
            $suffix = if ($target -eq 'windows/amd64') { '.zip' } else { '.tar.gz' }
            foreach ($product in @('core', 'ui')) {
                $expectedArchives.Add("$product|$target", "midden-${product}_${releaseVersion}_$($target.Replace('/', '_'))$suffix")
            }
        }
        if (-not $expectedArchives.ContainsKey('core|windows/amd64')) { throw 'No native Windows AMD64 release asset' }
        $expectedArchives.Add('bundle|universal', "midden-bundle_${releaseVersion}.zip")
        Assert-Keys @($build.archives) @($expectedArchives.Values) 'Build archive'
        Assert-Keys @($sums.Keys) (@($expectedArchives.Values) + @('install.ps1', 'install.sh', 'build-manifest.json', 'manifest.tsv')) 'Checksum'
        $tsvHeaders = New-Map
        $tsvArchives = New-Map
        $fileMaps = New-Map
        foreach ($line in $utf8.GetString($metadata['manifest.tsv']).TrimEnd("`r", "`n").Split("`n")) {
            $fields = $line.TrimEnd("`r").Split("`t")
            switch -CaseSensitive ($fields[0]) {
                { $_ -in @('format', 'version', 'commit') } {
                    if ($fields.Count -ne 2 -or $tsvHeaders.ContainsKey($fields[0])) { throw 'Invalid TSV header' }
                    $tsvHeaders.Add($fields[0], $fields[1])
                }
                'archive' {
                    if ($fields.Count -ne 4) { throw 'Invalid TSV archive record' }
                    $key = $fields[1] + '|' + $fields[2]
                    if (-not $expectedArchives.ContainsKey($key) -or $tsvArchives.ContainsKey($key) -or
                        $fields[3] -cne $expectedArchives[$key]) { throw 'TSV archive/version mismatch' }
                    $tsvArchives.Add($key, $fields[3])
                }
                'file' {
                    if ($fields.Count -ne 5) { throw 'Invalid TSV file record' }
                    $key = $fields[1] + '|' + $fields[2]
                    if (-not $expectedArchives.ContainsKey($key)) { throw 'TSV file has no supported archive' }
                    if (-not $fileMaps.ContainsKey($key)) { $fileMaps.Add($key, (New-Map)) }
                    $name = Assert-Relative $fields[3]
                    if ($fileMaps[$key].ContainsKey($name)) { throw 'Duplicate TSV file path' }
                    $fileMaps[$key].Add($name, (Assert-Hash $fields[4]))
                    if ($fileMaps[$key].Count -gt $memberLimit) { throw 'Archive member limit exceeded' }
                }
                default { throw 'Unsupported TSV record' }
            }
        }
        Assert-Keys @($tsvHeaders.Keys) @('format', 'version', 'commit') 'TSV header'
        if ($tsvHeaders['format'] -cne 'midden-release-v1' -or $tsvHeaders['version'] -cne $releaseVersion -or
            $tsvHeaders['commit'] -cne $build.commit) { throw 'TSV version/source mismatch' }
        Assert-Keys @($tsvArchives.Keys) @($expectedArchives.Keys) 'TSV archive'
        Assert-Keys @($fileMaps.Keys) @($expectedArchives.Keys) 'TSV file'
        foreach ($key in $fileMaps.Keys) { Assert-Paths @($fileMaps[$key].Keys) }
        return @{ Build = $build; Sums = $sums; Archives = $tsvArchives; Files = $fileMaps; Metadata = $metadata }
    }

    function Read-Archive([byte[]]$Bytes, $Expected) {
        $memory = New-Object IO.MemoryStream(,$Bytes)
        $archive = New-Object IO.Compression.ZipArchive($memory, [IO.Compression.ZipArchiveMode]::Read)
        $files = New-Map
        try {
            if ($archive.Entries.Count -gt $memberLimit) { throw 'Archive member limit exceeded' }
            $size = 0L
            foreach ($entry in $archive.Entries) {
                $name = Assert-Relative $entry.FullName
                $kind = ([long]$entry.ExternalAttributes -shr 16) -band 61440
                if ($kind -notin @(0, 32768) -or ($entry.ExternalAttributes -band 1040) -or
                    $files.ContainsKey($name)) { throw 'Archive has a link, directory or duplicate member' }
                if (-not $Expected.ContainsKey($name)) { throw "Unlisted archive member: $name" }
                $size += $entry.Length
                if ($entry.Length -lt 0 -or $size -gt $archiveLimit) { throw 'Archive expanded size limit exceeded' }
                $stream = $entry.Open()
                try { $data = Read-Bounded $stream $entry.Length }
                finally { $stream.Dispose() }
                if ($data.Length -ne $entry.Length -or (Get-Digest $data) -cne $Expected[$name]) {
                    throw "Archive file checksum mismatch: $name"
                }
                $files.Add($name, $data)
            }
            Assert-Paths @($files.Keys)
            Assert-Keys @($files.Keys) @($Expected.Keys) 'Archive file'
            return ,$files
        } finally { $archive.Dispose(); $memory.Dispose() }
    }

    function Assert-ProductFiles([string]$Product, $Files) {
        $required = if ($Product -eq 'core') { @('midden.exe', 'LICENSE', 'CORE.md') } else {
            @('midden.exe', 'midden-ui.exe', 'LICENSE', 'NOTICE', 'README.md', 'start.ps1', 'start.sh', 'package-manifest.json',
              'bundles/README.md', 'bundles/investigation/SKILL.md', 'bundles/article/SKILL.md',
              'bundles/presentation/SKILL.md', 'bundles/long-form/SKILL.md',
              'bundles/midden-shared/sources.md', 'bundles/midden-shared/tools.md')
        }
        foreach ($name in $required) {
            if (-not $Files.ContainsKey($name)) { throw "Missing $Product file: $name" }
        }
        $allowed = $required + @('THIRD_PARTY_NOTICES.txt')
        foreach ($name in $Files.Keys) {
            if ($name -cnotin $allowed -and ($Product -eq 'core' -or -not $name.StartsWith('bundles/'))) {
                throw "Unsupported $Product file: $name"
            }
        }
    }

    function Read-Owned {
        if (-not [IO.File]::Exists($receiptPath)) {
            if (Test-Path -LiteralPath $receiptPath) { throw 'Receipt path is not a regular file' }
            return $null
        }
        $bytes = Read-Local $receiptPath $metadataLimit
        $receipt = Convert-Json $bytes
        if ($receipt.schema -cne 'midden.bootstrap/v1' -or $receipt.mode -cne $Options.Mode -or
            $receipt.install_dir -cne $installRoot) { throw 'Receipt mode/path/schema differs; use its original installation' }
        $null = Assert-Version $receipt.version
        $files = Get-PropertyMap $receipt.files
        if ($files.Count -gt $memberLimit) { throw 'Receipt file limit exceeded' }
        Assert-Paths @($files.Keys)
        Assert-ProductFiles $receipt.mode $files
        foreach ($name in @($files.Keys)) {
            $files[$name] = Assert-Hash $files[$name]
            Check-Expected (Join-Path $installRoot $name) $files[$name]
        }
        if ($null -ne $receipt.path_change) {
            $change = Get-PropertyMap $receipt.path_change
            Assert-Keys @($change.Keys) @('schema', 'before', 'after') 'PATH ownership'
            if ($change['schema'] -cne $pathSchema) { throw 'Unsupported user PATH receipt schema' }
            Assert-UserPathState $change['before']
            Assert-UserPathState $change['after']
            if (-not (Test-UserPathState $change['after'] (New-UserPathChange $change['before']).after)) {
                throw 'User PATH receipt does not describe adding this installation'
            }
        }
        return @{ Record = $receipt; Files = $files; Hash = (Get-Digest $bytes) }
    }

    function Check-Expected([string]$Path, $Expected) {
        $path = Get-SafePath $Path
        if ($null -eq $Expected) {
            if (Test-Path -LiteralPath $path) { throw "Refusing unowned collision: $path" }
        } elseif (-not [IO.File]::Exists($path) -or (Get-Digest (Read-Local $path)) -cne $Expected) {
            throw "Preserving modified or missing owned file: $path"
        }
    }

    function Resolve-TempParent {
        $path = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\')
        for ($pass = 0; $pass -lt 32; $pass++) {
            $cursor = [IO.Path]::GetPathRoot($path)
            $changed = $false
            $parts = $path.Substring($cursor.Length).Split('\')
            for ($index = 0; $index -lt $parts.Count; $index++) {
                $cursor = Join-Path $cursor $parts[$index]
                $item = Get-Item -LiteralPath $cursor -Force
                if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) {
                    $targets = @($item.Target)
                    if ($targets.Count -ne 1 -or -not $targets[0]) { throw 'Cannot resolve temporary reparse target' }
                    $target = $targets[0]
                    if (-not [IO.Path]::IsPathRooted($target)) {
                        $target = Join-Path ([IO.Path]::GetDirectoryName($cursor)) $target
                    }
                    for ($next = $index + 1; $next -lt $parts.Count; $next++) { $target = Join-Path $target $parts[$next] }
                    $path = [IO.Path]::GetFullPath($target).TrimEnd('\')
                    $changed = $true
                    break
                }
            }
            if (-not $changed) {
                $path = Get-SafePath $path
                foreach ($protected in $protectedPaths) {
                    if (Test-Within $path $protected) { throw 'Temporary storage must be outside installation, source and data directories' }
                }
                return $path
            }
        }
        throw 'Temporary path has too many linked ancestors'
    }

    function New-PrivateWork {
        $path = Join-Path (Resolve-TempParent) ('midden-bootstrap-' + [Guid]::NewGuid().ToString('N'))
        $null = New-Item -ItemType Directory -Path $path
        $security = New-Object Security.AccessControl.DirectorySecurity
        $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User
        $security.SetOwner($sid)
        $security.SetAccessRuleProtection($true, $false)
        $rule = New-Object Security.AccessControl.FileSystemAccessRule(
            $sid, 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow')
        $security.AddAccessRule($rule)
        try {
            if ($PSVersionTable.PSEdition -eq 'Desktop') {
                [IO.Directory]::SetAccessControl($path, $security)
            } else {
                [IO.FileSystemAclExtensions]::SetAccessControl([IO.DirectoryInfo]$path, $security)
            }
        }
        catch { [IO.Directory]::Delete($path); throw }
        return $path
    }

    function Remove-Work([string]$Path) {
        $null = Get-SafePath $Path
        foreach ($file in [IO.Directory]::GetFiles($Path)) {
            $null = Get-SafePath $file
            [IO.File]::Delete($file)
        }
        foreach ($directory in [IO.Directory]::GetDirectories($Path)) { Remove-Work $directory }
        [IO.Directory]::Delete($Path)
    }

    function Write-Snapshot([string]$Root, $Files) {
        foreach ($name in $Files.Keys) {
            $path = Get-SafePath (Join-Path $Root $name)
            $null = [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($path))
            $stream = [IO.File]::Open($path, 'CreateNew', 'Write', 'None')
            try { $stream.Write($Files[$name], 0, $Files[$name].Length); $stream.Flush($true) }
            finally { $stream.Dispose() }
        }
    }

    function Probe-Version([string]$Path, [string]$Argument, [string]$Expected) {
        $process = New-Object Diagnostics.Process
        $process.StartInfo = New-Object Diagnostics.ProcessStartInfo
        $process.StartInfo.FileName = $Path
        $process.StartInfo.Arguments = $Argument
        $process.StartInfo.UseShellExecute = $false
        $process.StartInfo.RedirectStandardOutput = $true
        $process.StartInfo.RedirectStandardError = $true
        try {
            try { $null = $process.Start() }
            catch {
                $native = $_.Exception.GetBaseException()
                if ($native -isnot [ComponentModel.Win32Exception]) { throw }
                $detail = if ($native.NativeErrorCode -eq 5) {
                    'Access is denied. Check executable permissions and application-control policy; no fallback was attempted.'
                } else { $native.Message }
                throw "Process.Start($([IO.Path]::GetFileName($Path)) $Argument) failed (Win32=$($native.NativeErrorCode)): $detail"
            }
            $stdout = $process.StandardOutput.ReadToEndAsync()
            $stderr = $process.StandardError.ReadToEndAsync()
            if (-not $process.WaitForExit(15000)) {
                $process.Kill()
                $process.WaitForExit()
                throw 'The bootstrap-owned version probe timed out'
            }
            if ($process.ExitCode -ne 0 -or $stderr.Result.Trim() -or $stdout.Result.Trim() -cne $Expected) {
                throw "Native version probe does not match release metadata: expected $Expected"
            }
        } finally { $process.Dispose() }
    }

    function Publish-File([string]$Path, [byte[]]$Bytes, $Expected) {
        Check-Expected $Path $Expected
        $parent = [IO.Path]::GetDirectoryName($Path)
        $null = Get-SafePath $parent
        $null = [IO.Directory]::CreateDirectory($parent)
        $temporary = Join-Path $parent ('.midden-new-' + [Guid]::NewGuid().ToString('N'))
        try {
            $stream = [IO.File]::Open($temporary, 'CreateNew', 'Write', 'None')
            try { $stream.Write($Bytes, 0, $Bytes.Length); $stream.Flush($true) }
            finally { $stream.Dispose() }
            Check-Expected $Path $Expected
            if ($null -eq $Expected) { [IO.File]::Move($temporary, $Path) }
            else { [IO.File]::Replace($temporary, $Path, [NullString]::Value) }
        } finally {
            if ([IO.File]::Exists($temporary)) { [IO.File]::Delete($temporary) }
        }
    }

    function Apply-Transaction($Changes, $Expected, $PathChange) {
        $null = Get-SafePath $installRoot
        $null = [IO.Directory]::CreateDirectory($installRoot)
        $lock = [IO.File]::Open($lockPath, 'CreateNew', 'Write', 'None')
        $applied = New-Object 'System.Collections.Generic.List[string]'
        $backups = New-Map
        $pathAttempted = $false
        $pathApplied = $false
        $pathRestored = $false
        try {
            $index = 0
            foreach ($name in $Changes.Keys) {
                $path = Join-Path $installRoot $name
                Check-Expected $path $Expected[$name]
                if ($null -ne $Expected[$name]) {
                    $data = Read-Local $path
                    if ((Get-Digest $data) -cne $Expected[$name]) { throw 'Owned file changed while preparing rollback' }
                    $backup = Join-Path $work "backup-$index"
                    [IO.File]::WriteAllBytes($backup, $data)
                    $backups.Add($name, $backup)
                    $index++
                }
            }
            $order = @($Changes.Keys | Where-Object { $_ -ne $receiptName } | Sort-Object -CaseSensitive) + @($receiptName)
            foreach ($name in $order) {
                $path = Join-Path $installRoot $name
                Check-Expected $path $Expected[$name]
                if ($null -eq $Changes[$name]) { [IO.File]::Delete($path) }
                else { Publish-File $path $Changes[$name] $Expected[$name] }
                $applied.Add($name)
                $currentHash = if ($null -eq $Changes[$name]) { $null } else { Get-Digest $Changes[$name] }
                Check-Expected $path $currentHash
            }
            if ($null -ne $PathChange) {
                if (-not (Test-UserPathState (Get-UserPathState) $PathChange.before)) {
                    throw 'User PATH changed concurrently; refusing to overwrite it'
                }
                $pathAttempted = $true
                Set-UserPathState $PathChange.after
                $pathApplied = $true
            }
        } catch {
            $failure = $_
            $recovery = New-Object 'System.Collections.Generic.List[string]'
            if ($pathAttempted) {
                try {
                    $currentPath = Get-UserPathState
                    if (Test-UserPathState $currentPath $PathChange.after) {
                        Set-UserPathState $PathChange.before
                        $pathRestored = $true
                    } elseif (-not (Test-UserPathState $currentPath $PathChange.before)) {
                        throw 'PATH changed during publication'
                    }
                } catch { $recovery.Add($_.Exception.Message) }
            }
            for ($index = $applied.Count - 1; $index -ge 0; $index--) {
                $name = $applied[$index]
                $path = Join-Path $installRoot $name
                try {
                    $currentHash = if ($null -eq $Changes[$name]) { $null } else { Get-Digest $Changes[$name] }
                    Check-Expected $path $currentHash
                    if ($backups.ContainsKey($name)) {
                        Publish-File $path (Read-Local $backups[$name]) $currentHash
                    } else { [IO.File]::Delete($path) }
                } catch { $recovery.Add("$name : $($_.Exception.Message)") }
            }
            if ($recovery.Count) {
                $script:retainWork = $true
                $map = @{ install_dir = $installRoot; expected = $Expected; backups = $backups; applied = @($applied) }
                [IO.File]::WriteAllBytes((Join-Path $work 'recovery.json'), (Json-Bytes $map))
                throw "Rollback requires recovery. Backups retained at $work. $failure $($recovery -join '; ')"
            }
            throw $failure
        } finally {
            try {
                $lock.Dispose()
                [IO.File]::Delete($lockPath)
            } finally {
                if ($pathRestored) { Send-UserPathNotification 'rollback restore' }
                elseif ($pathApplied) {
                    Send-UserPathNotification $(if ($Options.Uninstall) { 'restore' } else { 'commit' })
                }
            }
        }
        if ($pathApplied -and -not $Options.Uninstall) {
            $env:PATH = $env:PATH.TrimEnd(';') + ';' + $installRoot
        }
    }

    function Find-Python {
        $candidates = if ($env:PYTHON) { @($env:PYTHON) } else {
            @(foreach ($name in @('python', 'python3')) {
                Get-Command $name -CommandType Application -All -ErrorAction SilentlyContinue |
                    ForEach-Object { $_.Source }
            })
        }
        foreach ($candidate in $candidates) {
            & $candidate -c 'import sys; sys.exit(0 if sys.version_info >= (3,9) else 1)'
            if ($LASTEXITCODE -eq 0) { return $candidate }
            if ($env:PYTHON) { throw 'PYTHON must identify Python 3.9+; no fallback was attempted' }
        }
        throw 'CLI mode requires Python 3.9+; nothing was installed'
    }

    function Invoke-CliBackend {
        $python = Find-Python
        $bundleRoot = Join-Path $work 'bundle'
        Write-Snapshot $bundleRoot $selected['bundle|universal']
        $snapshotRoot = Join-Path $work 'distribution'
        $snapshotFiles = New-Map
        foreach ($name in $release.Metadata.Keys) {
            $snapshotFiles.Add($name, $release.Metadata[$name])
        }
        foreach ($key in @('core|windows/amd64', 'bundle|universal')) {
            $snapshotFiles.Add($release.Archives[$key], $archiveBytes[$key])
        }
        Write-Snapshot $snapshotRoot $snapshotFiles
        $arguments = @('-B', (Join-Path $bundleRoot 'installer\install.py'),
            '--distribution-dir', $snapshotRoot, '--project', $projectRoot,
            '--host', $Options.CliHost, '--bin-dir', $installRoot)
        if ($Options.Upgrade) { $arguments += '--upgrade' }
        if ($Options.Verify) { $arguments += '--verify' }
        if ($Options.Uninstall) { $arguments += '--uninstall' }
        & $python @arguments
        if ($LASTEXITCODE -ne 0) { throw "Verified CLI backend failed ($LASTEXITCODE); inspect its diagnostics" }
    }

    $work = $null
    $script:retainWork = $false
    try {
        if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) { throw 'Use the Unix bootstrap on this platform' }
        $architecture = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
        if ($architecture -ine 'AMD64') { throw "Unsupported native Windows architecture: $architecture" }
        if ($Options.Repository -cnotmatch '^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9][A-Za-z0-9._-]{0,99}$') {
            throw 'Repository must be an explicit owner/name'
        }
        if (([int]$Options.Upgrade + [int]$Options.Verify + [int]$Options.Uninstall) -gt 1) {
            throw 'Upgrade, Verify and Uninstall are mutually exclusive'
        }
        $requestedVersion = $Options.Version
        if ($requestedVersion -cne 'latest') { $null = Assert-Version $requestedVersion }
        $homePath = Get-SafePath $env:USERPROFILE
        $localAppData = Get-SafePath $env:LOCALAPPDATA
        $installRoot = Get-SafePath $(if ($Options.InstallDir) { $Options.InstallDir } else { Join-Path $localAppData 'Programs\Midden' })
        if (-not [IO.Path]::GetDirectoryName($installRoot) -or
            $installRoot -ieq $homePath -or $installRoot -ieq $localAppData) {
            throw 'Installation root cannot be a filesystem, home or application-data root'
        }
        if (Test-Path -LiteralPath $installRoot -PathType Leaf) { throw 'InstallDir must be a directory' }
        $projectRoot = Get-SafePath $Options.ProjectDir
        if (-not [IO.Directory]::Exists($projectRoot)) { throw 'ProjectDir must already exist' }
        $distribution = if ($Options.DistributionDir) { Get-SafePath $Options.DistributionDir } else { $null }
        if ($distribution -and -not [IO.Directory]::Exists($distribution)) { throw 'DistributionDir must be a local directory' }
        $protectedPaths = @($installRoot, (Join-Path $homePath '.midden'),
            (Join-Path $homePath 'AppData\Local\Midden'), (Join-Path $localAppData 'Midden'),
            (Join-Path $homePath '.copilot'), (Join-Path $homePath '.claude'), (Join-Path $homePath '.local\share\opencode'))
        foreach ($variable in @('MIDDEN_HOME', 'COMPA_HOME', 'MIDDEN_CLAUDE_ROOT', 'MIDDEN_COPILOT_ROOT', 'MIDDEN_OPENCODE_DB')) {
            $value = [Environment]::GetEnvironmentVariable($variable)
            if ($value) { $protectedPaths += Get-SafePath $value }
        }
        foreach ($protected in $protectedPaths | Select-Object -Skip 1) {
            $protected = Get-SafePath $protected
            if ((Test-Within $installRoot $protected) -or (Test-Within $protected $installRoot)) {
                throw 'InstallDir must not overlap application data or source stores'
            }
        }
        if ($distribution) {
            if ((Test-Within $installRoot $distribution) -or (Test-Within $distribution $installRoot)) {
                throw 'InstallDir must not overlap DistributionDir'
            }
            $protectedPaths += $distribution
        }
        if ($Options.SourcePath) { $protectedPaths += [IO.Path]::GetDirectoryName($Options.SourcePath) }
        if ($Options.Mode -eq 'cli') { $protectedPaths += $projectRoot }
        $receiptPath = Get-SafePath (Join-Path $installRoot $receiptName)
        $lockPath = Get-SafePath (Join-Path $installRoot $lockName)
        if (Test-Path -LiteralPath $lockPath) { throw 'Pending bootstrap lock; inspect the active/stale operation before retrying' }
        $owned = if ($Options.Mode -ne 'cli') { Read-Owned } else { $null }
        $operation = if ($Options.Verify) { 'verify' } elseif ($Options.Uninstall) { 'uninstall' } elseif ($Options.Upgrade) { 'upgrade' } else { 'install' }
        if ($Options.Mode -ne 'cli') {
            if (($Options.Verify -or $Options.Uninstall) -and -not $owned) { throw 'No supported bootstrap receipt' }
            if (-not ($Options.Verify -or $Options.Uninstall) -and ([bool]$owned -ne [bool]$Options.Upgrade)) {
                throw 'Use Upgrade only with an existing unchanged receipt'
            }
        } elseif (($Options.Verify -or $Options.Uninstall) -and -not $distribution) {
            throw 'CLI verify/uninstall requires a reviewed DistributionDir matching the receipt version'
        }
        $pathChange = $null
        $pathNotice = $null
        $recordedPath = if ($owned) { $owned.Record.path_change } else { $null }
        if (-not $Options.NoPath -and -not $Options.Verify -and $Options.Mode -ne 'cli') {
            $currentPath = Get-UserPathState
            if ($Options.Uninstall) {
                if ($recordedPath -and (Test-UserPathState $currentPath $recordedPath.after)) {
                    $pathChange = @{ before = $currentPath; after = $recordedPath.before }
                } elseif ($recordedPath) {
                    $pathNotice = 'User PATH raw value or kind has changed; preserving it rather than overwriting edits'
                }
            } elseif (-not $recordedPath -and -not (Test-PathContainsInstall $currentPath)) {
                $pathChange = New-UserPathChange $currentPath
                $recordedPath = $pathChange
            }
        }
        if ($pathNotice -and -not $Options.DryRun) { Write-Warning $pathNotice }
        $changes = New-Map
        $expected = New-Map
        $selected = New-Map
        $archiveBytes = New-Map
        $release = $null
        if (-not (($Options.Verify -or $Options.Uninstall) -and $Options.Mode -ne 'cli')) {
            $downloadBase = $null
            if (-not $distribution) {
                if ($requestedVersion -eq 'latest') {
                    $latest = Convert-Json (Get-RemoteBytes "https://api.github.com/repos/$($Options.Repository)/releases/latest" $metadataLimit)
                    if ($latest.tag_name -isnot [string] -or -not $latest.tag_name.StartsWith('v')) { throw 'Latest release has an invalid tag' }
                    $requestedVersion = Assert-Version $latest.tag_name.Substring(1)
                }
                $downloadBase = "https://github.com/$($Options.Repository)/releases/download/v$requestedVersion"
            }
            $release = Read-Release
            $keys = if ($Options.Mode -eq 'cli') { @('core|windows/amd64', 'bundle|universal') } else { @("$($Options.Mode)|windows/amd64") }
            foreach ($key in $keys) {
                $name = $release.Archives[$key]
                $bytes = Get-Asset $name $archiveLimit
                if ((Get-Digest $bytes) -cne $release.Sums[$name]) { throw "Archive checksum mismatch: $name" }
                $archiveBytes.Add($key, $bytes)
                $selected.Add($key, (Read-Archive $bytes $release.Files[$key]))
            }
            $core = if ($Options.Mode -eq 'ui') { $selected['ui|windows/amd64'] } else { $selected['core|windows/amd64'] }
            if ((Get-Digest $core['midden.exe']) -cne (Assert-Hash $release.Build.core_binaries.'windows/amd64')) {
                throw 'Core binary checksum differs from build metadata'
            }
            if ($Options.Mode -eq 'ui') {
                Assert-ProductFiles 'ui' $core
                if ((Get-Digest $core['midden-ui.exe']) -cne (Assert-Hash $release.Build.ui_binaries.'windows/amd64')) {
                    throw 'UI binary checksum differs from build metadata'
                }
                $package = Convert-Json $core['package-manifest.json']
                if ($package.kind -cne 'midden-ui-release' -or $package.version -cne $release.Build.version -or
                    $package.platform -cne 'windows/amd64' -or $package.source_commit -cne $release.Build.commit -or
                    $package.kernel -cne 'github.com/xibodev/compa v1.0.0') { throw 'UI package version/platform/source/kernel mismatch' }
                $packageFiles = Get-PropertyMap $package.files
                Assert-Keys @($packageFiles.Keys) @($core.Keys | Where-Object { $_ -ne 'package-manifest.json' }) 'UI package'
                foreach ($name in $packageFiles.Keys) {
                    if ((Assert-Hash $packageFiles[$name]) -cne $release.Files['ui|windows/amd64'][$name]) {
                        throw 'UI package file checksum differs from TSV'
                    }
                }
            } else { Assert-ProductFiles 'core' $core }
            if ($Options.Mode -ne 'cli') {
                foreach ($name in $core.Keys) { $changes.Add($name, $core[$name]) }
            } else {
                if (-not $selected['bundle|universal'].ContainsKey('installer/install.py')) { throw 'Bundle has no offline CLI backend' }
                if ($Options.Verify -or $Options.Uninstall) {
                    $cliReceipt = Convert-Json (Read-Local (Join-Path $installRoot 'midden-install-receipt.json') $metadataLimit)
                    if ($cliReceipt.version -cne ('midden ' + $release.Build.version)) {
                        throw 'Reviewed CLI backend release must match the installed receipt version'
                    }
                }
            }
        }
        if ($owned) {
            foreach ($name in $owned.Files.Keys) {
                if (-not $changes.ContainsKey($name)) { $changes.Add($name, $null) }
            }
        }
        foreach ($name in $changes.Keys) {
            $hash = if ($owned -and $owned.Files.ContainsKey($name)) { $owned.Files[$name] } else { $null }
            Check-Expected (Join-Path $installRoot $name) $hash
            $expected.Add($name, $hash)
        }
        $releaseVersion = if ($release) { $release.Build.version } elseif ($owned) { $owned.Record.version } else { $null }
        if ($Options.DryRun) {
            $destinations = @(
                foreach ($name in $changes.Keys) {
                    $action = if ($Options.Verify) { 'verify' } elseif ($null -eq $changes[$name]) { 'remove' } elseif ($null -eq $expected[$name]) { 'create' } else { 'replace' }
                    @{ path = (Join-Path $installRoot $name); action = $action }
                }
                if ($Options.Mode -ne 'cli') {
                    @{ path = $receiptPath; action = $(if ($owned) { $operation } else { 'create' }) }
                }
            )
            @{
                dry_run = $true; mode = $Options.Mode; operation = $operation; version = $releaseVersion
                repository = $Options.Repository; install_dir = $installRoot; project_dir = $projectRoot
                host = $Options.CliHost; destinations = $destinations
                path_action = $(if ($Options.NoPath -or $Options.Mode -eq 'cli') { 'unchanged' } else { 'user only; never machine PATH' })
                dependencies = $(if ($Options.Mode -eq 'cli') { 'Existing Python 3.9+; backend ownership checks deferred' } else { 'Windows PowerShell 5.1+ or PowerShell 7; no Go, Node or Python' })
                notes = @('No extraction, writes, version probes or launch; filesystem access remains unprobed.',
                    'Unsigned publisher. Checksums establish integrity, not cryptographic authenticity.') +
                    @($pathNotice | Where-Object { $_ })
            } | ConvertTo-Json -Depth 10
            return
        }
        if ($Options.Verify -and $Options.Mode -ne 'cli') {
            Write-Host "Verified Midden $($Options.Mode) $releaseVersion; all receipt-owned files match"
            return
        }
        $work = New-PrivateWork
        if ($Options.Mode -eq 'cli') { Invoke-CliBackend; return }
        if (-not $Options.Uninstall) {
            $stage = Join-Path $work 'payload'
            Write-Snapshot $stage $core
            Probe-Version (Join-Path $stage 'midden.exe') 'version' "midden $releaseVersion"
            if ($Options.Mode -eq 'ui') { Probe-Version (Join-Path $stage 'midden-ui.exe') '--version' "midden-ui $releaseVersion" }
        }
        if ($Options.Uninstall) { $changes.Add($receiptName, $null) }
        else {
            $inventory = [ordered]@{}
            foreach ($name in $core.Keys) { $inventory[$name] = Get-Digest $core[$name] }
            $record = @{
                schema = 'midden.bootstrap/v1'; mode = $Options.Mode; version = $releaseVersion
                commit = $release.Build.commit; repository = $Options.Repository; install_dir = $installRoot
                files = $inventory; path_change = $recordedPath
            }
            $changes.Add($receiptName, (Json-Bytes $record))
        }
        $expected.Add($receiptName, $(if ($owned) { $owned.Hash } else { $null }))
        Apply-Transaction $changes $expected $pathChange
        if ($Options.Uninstall) {
            Write-Host 'Removed unchanged receipt-owned files; unowned files and application data were preserved'
        } else {
            Write-Host "Installed Midden $($Options.Mode) $releaseVersion at $installRoot"
            Write-Host 'Unsigned publisher. Checksums establish integrity, not cryptographic authenticity.'
            if ($Options.Mode -eq 'ui' -and -not $Options.NoLaunch) {
                $arguments = @()
                if ($Options.NoOpen) { $arguments += '--no-open' }
                & (Join-Path $installRoot 'midden-ui.exe') @arguments
                if ($LASTEXITCODE -ne 0) { throw "Installed UI exited with status $LASTEXITCODE" }
            }
        }
    } catch {
        throw "midden bootstrap: $($_.Exception.Message)"
    } finally {
        if ($work -and -not $script:retainWork) { Remove-Work $work }
    }
} @{
    Mode = $Mode; Version = $Version; InstallDir = $InstallDir; ProjectDir = $ProjectDir
    CliHost = $CliHost; DistributionDir = $DistributionDir; Repository = $Repository
    NoPath = [bool]$NoPath; NoLaunch = [bool]$NoLaunch; NoOpen = [bool]$NoOpen
    Upgrade = [bool]$Upgrade; Verify = [bool]$Verify; Uninstall = [bool]$Uninstall; DryRun = [bool]$DryRun
    SourcePath = $MyInvocation.MyCommand.Path
}

<#
.SYNOPSIS
Installs, updates, verifies and removes Midden: Core, the Bundle and the App.
.DESCRIPTION
One installer and one receipt for all three products, with no build toolchain
and no Python. Standalone: a saved script accepts parameters; irm URL | iex
installs the App and runs it in the foreground.

Everything is per user. Programs go to LOCALAPPDATA\Programs\Midden; unless
-NoPath is set, that folder is added to the user's PATH, never the machine PATH.
The Bundle's skills are copied unchanged into each harness's skills folder, the
person's by default or one project's with -Project; they call midden by name.
The App also gets a Start menu entry and its own Pandoc, fetched from Pandoc's
own release, checked against the SHA-256 in Midden's manifest and kept in
app\tools, which is never on the person's PATH.

The receipt, install-receipt.tsv in the programs folder, lists every owned file
with its hash, every place the skills were copied to, the Start menu entry and
the user PATH change. -Upgrade moves everything installed to the new release: it
rewrites the person's skill copies that are unchanged since install and lists
older project copies. -Uninstall removes only owned files that are unchanged.
Core's state (~\.midden), the App's data and unowned files are never removed.

PATH receipts keep the raw registry value and kind without expanding tokens.
Committed PATH changes notify desktop processes with WM_SETTINGCHANGE
(Environment), with a five-second timeout per receiver; a failed notification
warns without undoing the change. No execution-policy change, elevation,
credentials or model configuration occurs. Unsigned executables may trigger
Windows publisher warnings. Checksums prove integrity, not authenticity.
.PARAMETER Mode
app (the default): Core, the skills and the App. core: Core only. bundle: Core,
and the skills in the harness folders named by -Harness.
.PARAMETER Harness
copilot, claude or agents; one or more. Required with -Mode bundle. With
-Uninstall, removes only those copies of the skills.
.PARAMETER Project
Copies the skills into this project's harness folder instead of the person's.
.PARAMETER DistributionDir
Uses a reviewed local release instead of the network. The app mode also needs
the pinned Pandoc archive in it.
.PARAMETER DryRun
Checks metadata, archives and ownership and prints the plan as JSON. Writes,
extracts, probes and launches nothing and leaves PATH unchanged.
.PARAMETER NoPath
Leaves the user and process PATH unchanged.
.PARAMETER NoLaunch
Does not start the App after installing it.
.PARAMETER NoOpen
Passes --no-open to the App started after installation.
.EXAMPLE
.\install.ps1 -Mode bundle -Harness claude,copilot
.EXAMPLE
.\install.ps1 -Mode bundle -Harness copilot -Project 'C:\work\example'
.EXAMPLE
.\install.ps1 -Upgrade
#>
#requires -Version 5.1
[CmdletBinding()]
param(
    [ValidateSet('', 'app', 'core', 'bundle')][string]$Mode = '',
    [string[]]$Harness = @(),
    [string]$Project = '',
    [string]$Version = 'latest',
    [string]$InstallDir = '',
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
    $toolLimit = 512MB
    $memberLimit = 4096
    $receiptName = 'install-receipt.tsv'
    $receiptFormat = 'midden-install-v1'
    $manifestFormat = 'midden-release-v2'
    $legacyReceiptName = '.midden-bootstrap-receipt.json'
    $legacyCliReceiptName = 'midden-install-receipt.json'
    $lockName = '.midden-install.lock'
    $startName = 'Midden.lnk'
    $pandocPrefix = 'https://github.com/jgm/pandoc/releases/download/'
    $products = @('core', 'bundle', 'app')
    $skillFolders = @('midden-investigation', 'midden-article', 'midden-presentation', 'midden-long-form', 'midden-shared')
    $userSkillDirs = @{ copilot = '.copilot\skills'; claude = '.claude\skills'; agents = '.agents\skills' }
    $projectSkillDirs = @{ copilot = '.github\skills'; claude = '.claude\skills'; agents = '.agents\skills' }
    $utf8 = New-Object System.Text.UTF8Encoding($false, $true)
    Add-Type -AssemblyName System.IO.Compression

    function New-Map {
        return ,(New-Object 'System.Collections.Generic.Dictionary[string,object]' ([StringComparer]::Ordinal))
    }

    function New-List {
        return ,(New-Object 'System.Collections.Generic.List[object]')
    }

    function Get-Digest([byte[]]$Bytes) {
        $hasher = [Security.Cryptography.SHA256]::Create()
        try { return ([BitConverter]::ToString($hasher.ComputeHash($Bytes))).Replace('-', '').ToLowerInvariant() }
        finally { $hasher.Dispose() }
    }

    function Get-FileDigest([string]$Path) {
        $stream = [IO.File]::Open($Path, 'Open', 'Read', 'Read')
        $hasher = [Security.Cryptography.SHA256]::Create()
        try { return ([BitConverter]::ToString($hasher.ComputeHash($stream))).Replace('-', '').ToLowerInvariant() }
        finally { $hasher.Dispose(); $stream.Dispose() }
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

    function Assert-Field([string]$Value, [string]$Label) {
        if ($Value -match "[\t\r\n]") { throw "$Label contains a tab or line break; it cannot be recorded" }
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

    function Copy-Bounded($Source, [string]$Path, [long]$Limit) {
        $output = [IO.File]::Open($Path, 'CreateNew', 'Write', 'None')
        $buffer = New-Object byte[] 81920
        $total = 0L
        try {
            while (($read = $Source.Read($buffer, 0, $buffer.Length)) -gt 0) {
                $total += $read
                if ($total -gt $Limit) { throw 'Input size limit exceeded' }
                $output.Write($buffer, 0, $read)
            }
            $output.Flush($true)
        } finally { $output.Dispose() }
        return $total
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

    function Open-Remote([string]$Url, [long]$Limit) {
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
            $request.UserAgent = 'Midden-installer'
            $request.AllowAutoRedirect = $false
            $response = $request.GetResponse()
            $status = [int]$response.StatusCode
            if ($status -in @(301, 302, 303, 307, 308)) {
                $uri = New-Object Uri($uri, $response.Headers['Location'])
                $response.Dispose()
                continue
            }
            if ($status -ne 200) { $response.Dispose(); throw "Download failed with HTTP $status" }
            if ($response.ContentLength -gt $Limit) { $response.Dispose(); throw 'Download size limit exceeded' }
            return $response
        }
        throw 'Download exceeded the redirect limit'
    }

    function Get-RemoteBytes([string]$Url, [long]$Limit) {
        $response = Open-Remote $Url $Limit
        try {
            $stream = $response.GetResponseStream()
            try { return ,(Read-Bounded $stream $Limit) }
            finally { $stream.Dispose() }
        } finally { $response.Dispose() }
    }

    function Save-Remote([string]$Url, [string]$Path, [long]$Limit) {
        $response = Open-Remote $Url $Limit
        try {
            $stream = $response.GetResponseStream()
            try { $null = Copy-Bounded $stream $Path $Limit }
            finally { $stream.Dispose() }
        } finally { $response.Dispose() }
    }

    function Convert-Json([byte[]]$Bytes) {
        return $utf8.GetString($Bytes) | ConvertFrom-Json
    }

    function Get-PropertyMap($Value) {
        if ($null -eq $Value -or $Value -isnot [pscustomobject]) { throw 'Expected a JSON object' }
        $map = New-Map
        foreach ($property in $Value.PSObject.Properties) { $map.Add($property.Name, $property.Value) }
        return ,$map
    }

    function Assert-Keys($Actual, $Expected, [string]$Label) {
        $left = @($Actual | Sort-Object -CaseSensitive)
        $right = @($Expected | Sort-Object -CaseSensitive)
        if ($left.Count -ne $right.Count -or
            [string]::Join("`n", [string[]]$left) -cne [string]::Join("`n", [string[]]$right)) {
            throw "$Label inventory mismatch"
        }
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
            before = $Before
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

    function Get-Asset([string]$Name, [long]$Limit) {
        if ($distribution) { return ,(Read-Local (Join-Path $distribution $Name) $Limit) }
        return ,(Get-RemoteBytes ($downloadBase + '/' + $Name) $Limit)
    }

    function Read-Release {
        $sums = New-Map
        $checksumBytes = Get-Asset 'SHA256SUMS' $metadataLimit
        foreach ($line in $utf8.GetString($checksumBytes).TrimEnd("`r", "`n").Split("`n")) {
            if ($line.TrimEnd("`r") -cnotmatch '^([0-9a-fA-F]{64})  ([^/\\]+)$') { throw 'Invalid SHA256SUMS entry' }
            $hash, $name = $Matches[1], $Matches[2]
            $null = Assert-Relative $name
            if ($sums.ContainsKey($name)) { throw 'Duplicate checksum filename' }
            $sums.Add($name, $hash.ToLowerInvariant())
        }
        Assert-Paths @($sums.Keys)
        $metadata = New-Map
        foreach ($name in @('build-manifest.json', 'manifest.tsv')) {
            if (-not $sums.ContainsKey($name)) { throw "Missing checksum: $name" }
            $bytes = Get-Asset $name $metadataLimit
            if ((Get-Digest $bytes) -cne $sums[$name]) { throw "Metadata checksum mismatch: $name" }
            $metadata.Add($name, $bytes)
        }
        $build = Get-PropertyMap (Convert-Json $metadata['build-manifest.json'])
        foreach ($key in @('version', 'commit', 'products', 'installers', 'targets', 'core_binaries', 'app_binaries', 'archives')) {
            if (-not $build.ContainsKey($key)) { throw "Build manifest lacks $key; this installer needs a newer release format" }
        }
        $version = Assert-Version $build['version']
        if ($requestedVersion -ne 'latest' -and $requestedVersion -cne $version) {
            throw 'Build manifest version does not match the requested release'
        }
        if ($build['commit'] -isnot [string] -or $build['commit'] -cnotmatch '^(?:[0-9a-f]{40}|[0-9a-f]{64})$') {
            throw 'Invalid build source commit'
        }
        Assert-Keys @($build['products']) $products 'Release products'
        Assert-Keys @($build['installers']) @('install.ps1', 'install.sh') 'Release installers'
        $targets = @($build['targets'])
        if (-not $targets.Count) { throw 'Release has no native targets' }
        $coreHashes = Get-PropertyMap $build['core_binaries']
        $appHashes = Get-PropertyMap $build['app_binaries']
        Assert-Keys @($coreHashes.Keys) $targets 'Core binary'
        Assert-Keys @($appHashes.Keys) $targets 'App binary'
        $expectedArchives = New-Map
        foreach ($target in $targets) {
            if ($target -cnotin @('windows/amd64', 'windows/arm64', 'linux/amd64', 'linux/arm64', 'darwin/amd64', 'darwin/arm64')) {
                throw 'Unsupported release target'
            }
            $null = Assert-Hash $coreHashes[$target]
            $null = Assert-Hash $appHashes[$target]
            $suffix = if ($target.StartsWith('windows/')) { '.zip' } else { '.tar.gz' }
            foreach ($product in @('core', 'app')) {
                $expectedArchives.Add("$product|$target", "midden-${product}_${version}_$($target.Replace('/', '_'))$suffix")
            }
        }
        $expectedArchives.Add('bundle|universal', "midden-bundle_${version}.zip")
        Assert-Keys @($build['archives']) @($expectedArchives.Values) 'Build archive'
        Assert-Keys @($sums.Keys) (@($expectedArchives.Values) + @('install.ps1', 'install.sh', 'build-manifest.json', 'manifest.tsv')) 'Checksum'
        $headers = New-Map
        $archives = New-Map
        $files = New-Map
        $fetch = New-Map
        $take = New-Map
        foreach ($line in $utf8.GetString($metadata['manifest.tsv']).TrimEnd("`r", "`n").Split("`n")) {
            $fields = $line.TrimEnd("`r").Split("`t")
            switch -CaseSensitive ($fields[0]) {
                { $_ -in @('format', 'version', 'commit') } {
                    if ($fields.Count -ne 2 -or $headers.ContainsKey($fields[0])) { throw 'Invalid TSV header' }
                    $headers.Add($fields[0], $fields[1])
                }
                'archive' {
                    if ($fields.Count -ne 4) { throw 'Invalid TSV archive record' }
                    $key = $fields[1] + '|' + $fields[2]
                    if (-not $expectedArchives.ContainsKey($key) -or $archives.ContainsKey($key) -or
                        $fields[3] -cne $expectedArchives[$key]) { throw 'TSV archive/version mismatch' }
                    $archives.Add($key, $fields[3])
                }
                'file' {
                    if ($fields.Count -ne 5) { throw 'Invalid TSV file record' }
                    $key = $fields[1] + '|' + $fields[2]
                    if (-not $expectedArchives.ContainsKey($key)) { throw 'TSV file has no supported archive' }
                    if (-not $files.ContainsKey($key)) { $files.Add($key, (New-Map)) }
                    $name = Assert-Relative $fields[3]
                    if ($files[$key].ContainsKey($name)) { throw 'Duplicate TSV file path' }
                    $files[$key].Add($name, (Assert-Hash $fields[4]))
                    if ($files[$key].Count -gt $memberLimit) { throw 'Archive member limit exceeded' }
                }
                'fetch' {
                    if ($fields.Count -ne 5 -or $fields[1] -cne 'pandoc' -or $fields[2] -cnotin $targets -or
                        $fetch.ContainsKey($fields[2]) -or -not $fields[3].StartsWith($pandocPrefix) -or
                        $fields[3].Substring($pandocPrefix.Length) -cnotmatch '^[0-9A-Za-z.]+/[A-Za-z0-9._-]+$') {
                        throw 'Invalid pinned Pandoc download'
                    }
                    $fetch.Add($fields[2], @{ Url = $fields[3]; Hash = (Assert-Hash $fields[4]); Name = $fields[3].Substring($fields[3].LastIndexOf('/') + 1) })
                    $take.Add($fields[2], (New-Map))
                }
                'take' {
                    if ($fields.Count -ne 5 -or $fields[1] -cne 'pandoc' -or -not $take.ContainsKey($fields[2]) -or
                        $take[$fields[2]].ContainsKey($fields[3]) -or -not $fields[4].StartsWith('app/tools/')) {
                        throw 'Invalid pinned Pandoc member'
                    }
                    $take[$fields[2]].Add((Assert-Relative $fields[3]), (Assert-Relative $fields[4]))
                }
                default { throw 'Unsupported TSV record' }
            }
        }
        Assert-Keys @($headers.Keys) @('format', 'version', 'commit') 'TSV header'
        if ($headers['format'] -cne $manifestFormat -or $headers['version'] -cne $version -or
            $headers['commit'] -cne $build['commit']) { throw 'TSV version/source mismatch' }
        Assert-Keys @($archives.Keys) @($expectedArchives.Keys) 'TSV archive'
        Assert-Keys @($files.Keys) @($expectedArchives.Keys) 'TSV file'
        foreach ($key in $files.Keys) { Assert-Paths @($files[$key].Keys) }
        Assert-Keys @($fetch.Keys) $targets 'Pinned Pandoc'
        foreach ($target in $take.Keys) {
            if (-not $take[$target].Count) { throw 'A pinned Pandoc download installs no files' }
            Assert-Paths @($take[$target].Values)
        }
        return @{
            Version = $version; Commit = $build['commit']; Targets = $targets; Sums = $sums; Archives = $archives
            Files = $files; Core = $coreHashes; App = $appHashes; Fetch = $fetch; Take = $take
        }
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
        $names = @($Files.Keys)
        switch ($Product) {
            'core' {
                foreach ($name in @('midden.exe', 'LICENSE')) { if ($name -cnotin $names) { throw "Missing core file: $name" } }
                foreach ($name in $names) {
                    if ($name -cnotin @('midden.exe', 'LICENSE', 'THIRD_PARTY_NOTICES.txt')) { throw "Unsupported core file: $name" }
                }
            }
            'bundle' {
                foreach ($folder in $skillFolders) {
                    $required = if ($folder -eq 'midden-shared') { 'sources.md' } else { 'SKILL.md' }
                    if ("skills/$folder/$required" -cnotin $names) { throw "Missing skill file: skills/$folder/$required" }
                }
                foreach ($name in $names) {
                    $parts = $name.Split('/')
                    if ($parts.Count -lt 3 -or $parts[0] -cne 'skills' -or $parts[1] -cnotin $skillFolders) {
                        throw "Unsupported bundle file: $name"
                    }
                }
            }
            'app' {
                foreach ($name in @('midden-ui.exe', 'app/LICENSE', 'app/NOTICE')) { if ($name -cnotin $names) { throw "Missing app file: $name" } }
                foreach ($name in $names) {
                    if ($name -cne 'midden-ui.exe' -and (-not $name.StartsWith('app/') -or $name.StartsWith('app/tools/'))) {
                        throw "Unsupported app file: $name"
                    }
                }
            }
        }
    }

    function New-Place([string]$Id, [string]$Kind, [string]$HarnessName, [string]$Scope, [string]$PlaceVersion, [string]$Dir) {
        return @{ Id = $Id; Kind = $Kind; Harness = $HarnessName; Scope = $Scope; Version = $PlaceVersion; Dir = $Dir; Files = (New-Map) }
    }

    function Read-PathRecord($Fields) {
        if ($Fields.Count -ne 5 -or $Fields[2] -cnotin @('0', '1')) { throw 'Invalid PATH record in the receipt' }
        if ($Fields[2] -eq '1') {
            $state = @{ exists = $true; kind = $Fields[3]; value = $Fields[4] }
        } else {
            if ($Fields[3] -cne '-' -or $Fields[4] -cne '') { throw 'Invalid absent PATH record in the receipt' }
            $state = @{ exists = $false; kind = $null; value = $null }
        }
        Assert-UserPathState $state
        return $state
    }

    function Read-LegacyReceipt([string]$Path) {
        $bytes = Read-Local $Path $metadataLimit
        $record = Get-PropertyMap (Convert-Json $bytes)
        foreach ($key in @('schema', 'mode', 'version', 'install_dir', 'files')) {
            if (-not $record.ContainsKey($key)) { throw 'The older installer''s receipt is incomplete' }
        }
        if ($record['schema'] -cne 'midden.bootstrap/v1' -or $record['mode'] -cnotin @('ui', 'core') -or
            $record['install_dir'] -ine $installRoot) {
            throw 'The older installer''s receipt does not describe this installation'
        }
        $owned = @{
            Legacy = $true; LegacyPath = $Path; Version = (Assert-Version $record['version'])
            Commit = $(if ($record.ContainsKey('commit')) { $record['commit'] } else { '' })
            Repository = $Options.Repository; Target = 'windows/amd64'; Products = (New-List); Places = (New-List)
            RootFiles = (New-Map); PathChange = $null; Pandoc = $null; Hash = (Get-Digest $bytes)
        }
        foreach ($product in $(if ($record['mode'] -eq 'ui') { $products } else { @('core') })) { $owned.Products.Add($product) }
        $files = Get-PropertyMap $record['files']
        if ($files.Count -gt $memberLimit) { throw 'Receipt file limit exceeded' }
        Assert-Paths @($files.Keys)
        foreach ($name in $files.Keys) { $owned.RootFiles.Add($name, (Assert-Hash $files[$name])) }
        if ($record.ContainsKey('path_change') -and $null -ne $record['path_change']) {
            $change = Get-PropertyMap $record['path_change']
            Assert-Keys @($change.Keys) @('schema', 'before', 'after') 'PATH ownership'
            if ($change['schema'] -cne 'midden.user-path/v1') { throw 'Unsupported user PATH receipt schema' }
            $before = Get-PropertyMap $change['before']
            $after = Get-PropertyMap $change['after']
            Assert-UserPathState $before
            Assert-UserPathState $after
            $before = @{ exists = $before['exists']; kind = $before['kind']; value = $before['value'] }
            $after = @{ exists = $after['exists']; kind = $after['kind']; value = $after['value'] }
            if (-not (Test-UserPathState $after (New-UserPathChange $before).after)) {
                throw 'User PATH receipt does not describe adding this installation'
            }
            $owned.PathChange = @{ before = $before; after = $after }
        }
        return $owned
    }

    function Read-Receipt {
        if (Test-Path -LiteralPath (Join-Path $installRoot $legacyCliReceiptName)) {
            throw ("This folder holds a Midden CLI installation made by the 0.3 installer ($legacyCliReceiptName). " +
                'Remove it with that release''s install.ps1 (-Mode cli -Uninstall -DistributionDir <that release>), then run this again.')
        }
        $legacy = Get-SafePath (Join-Path $installRoot $legacyReceiptName)
        $hasLegacy = Test-Path -LiteralPath $legacy
        $hasCurrent = Test-Path -LiteralPath $receiptPath
        if ($hasLegacy -and $hasCurrent) { throw 'Two installation receipts exist; inspect the installation before retrying' }
        if ($hasLegacy) {
            if (-not [IO.File]::Exists($legacy)) { throw 'Receipt path is not a regular file' }
            return Read-LegacyReceipt $legacy
        }
        if (-not $hasCurrent) { return $null }
        if (-not [IO.File]::Exists($receiptPath)) { throw 'Receipt path is not a regular file' }
        $bytes = Read-Local $receiptPath $metadataLimit
        $text = $utf8.GetString($bytes)
        if (-not $text.EndsWith("`n")) { throw 'Invalid installation receipt' }
        $meta = New-Map
        $owned = @{
            Legacy = $false; LegacyPath = $null; Products = (New-List); Places = (New-List); RootFiles = (New-Map)
            PathChange = $null; Pandoc = $null; Hash = (Get-Digest $bytes)
        }
        $placeIds = New-Map
        $placeDirs = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::OrdinalIgnoreCase)
        $pathStates = New-Map
        foreach ($line in $text.Substring(0, $text.Length - 1).Split("`n")) {
            $fields = $line.Split("`t")
            switch -CaseSensitive ($fields[0]) {
                { $_ -in @('format', 'version', 'commit', 'repository', 'target', 'root') } {
                    if ($fields.Count -ne 2 -or $meta.ContainsKey($fields[0])) { throw 'Invalid receipt header' }
                    $meta.Add($fields[0], $fields[1])
                }
                'product' {
                    if ($fields.Count -ne 2 -or $fields[1] -cnotin $products -or $owned.Products.Contains($fields[1])) {
                        throw 'Invalid receipt product'
                    }
                    $owned.Products.Add($fields[1])
                }
                'fetched' {
                    if ($fields.Count -ne 4 -or $fields[1] -cne 'pandoc' -or $null -ne $owned.Pandoc -or
                        -not $fields[2].StartsWith($pandocPrefix)) { throw 'Invalid receipt Pandoc record' }
                    $owned.Pandoc = @{ Url = $fields[2]; Hash = (Assert-Hash $fields[3]) }
                }
                'place' {
                    if ($fields.Count -ne 7 -or $fields[1] -cnotmatch '^[1-9][0-9]{0,5}$' -or $placeIds.ContainsKey($fields[1])) {
                        throw 'Invalid receipt place'
                    }
                    if ($fields[2] -ceq 'skills') {
                        if (-not $userSkillDirs.ContainsKey($fields[3]) -or $fields[4] -cnotin @('user', 'project')) { throw 'Invalid receipt skill place' }
                    } elseif ($fields[2] -ceq 'start') {
                        if ($fields[3] -cne '-' -or $fields[4] -cne '-') { throw 'Invalid receipt start place' }
                    } else { throw 'Invalid receipt place kind' }
                    $dir = $fields[6]
                    if ((Get-SafePath $dir) -cne $dir -or -not $placeDirs.Add($dir)) { throw 'Invalid or repeated receipt place folder' }
                    $place = New-Place $fields[1] $fields[2] $fields[3] $fields[4] (Assert-Version $fields[5]) $dir
                    $placeIds.Add($fields[1], $place)
                    $owned.Places.Add($place)
                }
                'file' {
                    if ($fields.Count -ne 4) { throw 'Invalid receipt file' }
                    $files = if ($fields[1] -ceq 'root') { $owned.RootFiles } elseif ($placeIds.ContainsKey($fields[1])) { $placeIds[$fields[1]].Files } else { $null }
                    if ($null -eq $files -or $files.ContainsKey($fields[2])) { throw 'Receipt file has no place, or repeats' }
                    $files.Add((Assert-Relative $fields[2]), (Assert-Hash $fields[3]))
                }
                'path' {
                    if ($fields.Count -lt 2 -or $fields[1] -cnotin @('before', 'after') -or $pathStates.ContainsKey($fields[1])) {
                        throw 'Invalid receipt PATH record'
                    }
                    $pathStates.Add($fields[1], (Read-PathRecord $fields))
                }
                default { throw 'Unsupported receipt record' }
            }
        }
        Assert-Keys @($meta.Keys) @('format', 'version', 'commit', 'repository', 'target', 'root') 'Receipt header'
        if ($meta['format'] -cne $receiptFormat) { throw 'Unsupported receipt format; use the installer of the installed release' }
        if ($meta['root'] -ine $installRoot) { throw 'Receipt describes another installation folder; refusing relocated ownership' }
        if ($meta['commit'] -cnotmatch '^(?:[0-9a-f]{40}|[0-9a-f]{64})$' -or
            $meta['target'] -cnotin @('windows/amd64', 'windows/arm64')) { throw 'Invalid receipt source or target' }
        $owned.Version = Assert-Version $meta['version']
        $owned.Commit = $meta['commit']
        $owned.Repository = $meta['repository']
        $owned.Target = $meta['target']
        if (-not $owned.Products.Contains('core')) { throw 'Receipt has no core product' }
        Assert-Paths @($owned.RootFiles.Keys)
        foreach ($place in $owned.Places) {
            if (-not $place.Files.Count) { throw 'Receipt place owns no files' }
            Assert-Paths @($place.Files.Keys)
        }
        if ($pathStates.Count -eq 2) {
            $owned.PathChange = @{ before = $pathStates['before']; after = $pathStates['after'] }
            if (-not (Test-UserPathState $owned.PathChange.after (New-UserPathChange $owned.PathChange.before).after)) {
                throw 'User PATH receipt does not describe adding this installation'
            }
        } elseif ($pathStates.Count) { throw 'Receipt PATH records are incomplete' }
        return $owned
    }

    function Format-PathRecord([string]$Which, $State) {
        if ($State.exists) {
            return "path`t$Which`t1`t$($State.kind)`t$(Assert-Field $State.value 'The user PATH')"
        }
        return "path`t$Which`t0`t-`t"
    }

    function Format-Receipt($Record) {
        $lines = New-List
        $lines.Add("format`t$receiptFormat")
        foreach ($key in @('version', 'commit', 'repository', 'target')) { $lines.Add("$key`t$($Record[$key])") }
        $lines.Add("root`t$(Assert-Field $installRoot 'The installation folder')")
        foreach ($product in $products) { if ($Record.Products.Contains($product)) { $lines.Add("product`t$product") } }
        if ($null -ne $Record.Pandoc) { $lines.Add("fetched`tpandoc`t$($Record.Pandoc.Url)`t$($Record.Pandoc.Hash)") }
        foreach ($place in $Record.Places) {
            $lines.Add("place`t$($place.Id)`t$($place.Kind)`t$($place.Harness)`t$($place.Scope)`t$($place.Version)`t$(Assert-Field $place.Dir 'A skills folder')")
        }
        foreach ($name in @($Record.RootFiles.Keys | Sort-Object -CaseSensitive)) { $lines.Add("file`troot`t$name`t$($Record.RootFiles[$name])") }
        foreach ($place in $Record.Places) {
            foreach ($name in @($place.Files.Keys | Sort-Object -CaseSensitive)) { $lines.Add("file`t$($place.Id)`t$name`t$($place.Files[$name])") }
        }
        if ($null -ne $Record.PathChange) {
            $lines.Add((Format-PathRecord 'before' $Record.PathChange.before))
            $lines.Add((Format-PathRecord 'after' $Record.PathChange.after))
        }
        return ,$utf8.GetBytes([string]::Join("`n", [string[]]$lines) + "`n")
    }

    function Get-SkillDir([string]$HarnessName, [string]$Scope) {
        $base, $relative = if ($Scope -eq 'project') { $projectRoot, $projectSkillDirs[$HarnessName] } else { $homePath, $userSkillDirs[$HarnessName] }
        $dir = Get-SafePath (Join-Path $base $relative)
        if ((Test-Within $dir $installRoot) -or (Test-Within $installRoot $dir)) {
            throw "A skills folder cannot overlap the installation folder: $dir"
        }
        return $dir
    }

    function Get-ProjectOf($Place) {
        $suffix = '\' + $projectSkillDirs[$Place.Harness]
        if ($Place.Dir.EndsWith($suffix, [StringComparison]::OrdinalIgnoreCase)) {
            return $Place.Dir.Substring(0, $Place.Dir.Length - $suffix.Length)
        }
        return $Place.Dir
    }

    function Get-StartDir {
        if (-not $env:APPDATA) { throw 'APPDATA is required for the Start menu entry' }
        return Get-SafePath (Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs')
    }

    function Get-ShortcutTarget([string]$Path) {
        $shell = New-Object -ComObject WScript.Shell
        try { return [string]$shell.CreateShortcut($Path).TargetPath }
        finally { [void][Runtime.InteropServices.Marshal]::ReleaseComObject($shell) }
    }

    function New-StartEntry([string]$Path) {
        $shell = New-Object -ComObject WScript.Shell
        try {
            $link = $shell.CreateShortcut($Path)
            $link.TargetPath = Join-Path $installRoot 'midden-ui.exe'
            $link.WorkingDirectory = $installRoot
            $link.Description = 'Midden: investigate your recorded AI sessions'
            $link.Save()
        } finally { [void][Runtime.InteropServices.Marshal]::ReleaseComObject($shell) }
    }

    function Test-StartEntry([string]$Path) {
        return [IO.File]::Exists($Path) -and (Get-ShortcutTarget $Path) -ieq (Join-Path $installRoot 'midden-ui.exe')
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
                    if (Test-Within $path $protected) { throw 'Temporary storage must be outside installation, project, source and data directories' }
                }
                return $path
            }
        }
        throw 'Temporary path has too many linked ancestors'
    }

    function New-PrivateWork {
        $path = Join-Path (Resolve-TempParent) ('midden-install-' + [Guid]::NewGuid().ToString('N'))
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
                throw 'The installer''s version probe timed out'
            }
            if ($process.ExitCode -ne 0 -or $stderr.Result.Trim() -or $stdout.Result.Trim() -cne $Expected) {
                throw "Native version probe does not match release metadata: expected $Expected"
            }
        } finally { $process.Dispose() }
    }

    function Test-Owned([string]$Path, [string]$Hash) {
        $path = Get-SafePath $Path
        return [IO.File]::Exists($path) -and (Get-FileDigest $path) -ceq $Hash
    }

    function Check-Expected([string]$Path, $Expected) {
        $path = Get-SafePath $Path
        if ($null -eq $Expected) {
            if (Test-Path -LiteralPath $path) { throw "Refusing to overwrite a file Midden does not own: $path" }
        } elseif (-not [IO.File]::Exists($path) -or (Get-FileDigest $path) -cne $Expected) {
            throw "Preserving a modified or missing owned file: $path"
        }
    }

    function New-Directory([string]$Path) {
        $missing = New-Object 'System.Collections.Generic.List[string]'
        $cursor = $Path
        while (-not [IO.Directory]::Exists($cursor)) {
            if (Test-Path -LiteralPath $cursor) { throw "A file is in the way of a folder: $cursor" }
            $missing.Insert(0, $cursor)
            $parent = [IO.Path]::GetDirectoryName($cursor)
            if (-not $parent -or $parent -eq $cursor) { break }
            $cursor = $parent
        }
        foreach ($directory in $missing) {
            $null = Get-SafePath $directory
            $null = [IO.Directory]::CreateDirectory($directory)
            $script:createdDirectories.Add($directory)
        }
    }

    function Publish-File([string]$Path, $Source, $Expected, [string]$NewHash) {
        Check-Expected $Path $Expected
        $parent = [IO.Path]::GetDirectoryName($Path)
        New-Directory $parent
        $temporary = Join-Path $parent ('.midden-new-' + [Guid]::NewGuid().ToString('N'))
        try {
            if ($Source -is [byte[]]) {
                $stream = [IO.File]::Open($temporary, 'CreateNew', 'Write', 'None')
                try { $stream.Write($Source, 0, $Source.Length); $stream.Flush($true) }
                finally { $stream.Dispose() }
            } else {
                [IO.File]::Copy([string]$Source, $temporary, $false)
            }
            if ((Get-FileDigest $temporary) -cne $NewHash) { throw "Staged file changed before publication: $Path" }
            Check-Expected $Path $Expected
            if ($null -eq $Expected) { [IO.File]::Move($temporary, $Path) }
            else { [IO.File]::Replace($temporary, $Path, [NullString]::Value) }
        } finally {
            if ([IO.File]::Exists($temporary)) { [IO.File]::Delete($temporary) }
        }
    }

    function Remove-EmptyParents([string]$Path, [string]$Stop) {
        $cursor = [IO.Path]::GetDirectoryName($Path)
        while ($cursor -and $cursor -ine $Stop -and (Test-Within $cursor $Stop)) {
            if (-not [IO.Directory]::Exists($cursor) -or
                [IO.Directory]::EnumerateFileSystemEntries($cursor).GetEnumerator().MoveNext()) { break }
            [IO.Directory]::Delete($cursor)
            $cursor = [IO.Path]::GetDirectoryName($cursor)
        }
    }

    # Changes: path -> @{ Old = hash or $null; New = hash or $null; Source = byte[] or staged file }.
    # Receipts are written last; any failure restores every file and the user PATH.
    function Apply-Transaction($Changes, $PathChange) {
        New-Directory $installRoot
        $lock = [IO.File]::Open($lockPath, 'CreateNew', 'Write', 'None')
        $applied = New-Object 'System.Collections.Generic.List[string]'
        $backups = New-Map
        $pathAttempted = $false
        $pathApplied = $false
        $pathRestored = $false
        try {
            $index = 0
            foreach ($path in @($Changes.Keys)) {
                $change = $Changes[$path]
                Check-Expected $path $change.Old
                if ($null -ne $change.Old) {
                    $backup = Join-Path $work "backup-$index"
                    $index++
                    [IO.File]::Copy($path, $backup, $false)
                    if ((Get-FileDigest $backup) -cne $change.Old) { throw 'Owned file changed while preparing rollback' }
                    $backups.Add($path, $backup)
                }
            }
            $last = @($legacyPath, $receiptPath) | Where-Object { $_ -and $Changes.Contains($_) }
            $order = @($Changes.Keys | Where-Object { $_ -notin $last } | Sort-Object) + @($last)
            foreach ($path in $order) {
                $change = $Changes[$path]
                Check-Expected $path $change.Old
                if ($null -eq $change.New) { [IO.File]::Delete($path) }
                else { Publish-File $path $change.Source $change.Old $change.New }
                $applied.Add($path)
                Check-Expected $path $change.New
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
            for ($position = $applied.Count - 1; $position -ge 0; $position--) {
                $path = $applied[$position]
                $change = $Changes[$path]
                try {
                    Check-Expected $path $change.New
                    if ($backups.ContainsKey($path)) { Publish-File $path $backups[$path] $change.New $change.Old }
                    else { [IO.File]::Delete($path) }
                } catch { $recovery.Add("$path : $($_.Exception.Message)") }
            }
            if ($recovery.Count) {
                $script:retainWork = $true
                $notes = @("installation: $installRoot") + @(foreach ($path in $backups.Keys) { "$path <- $($backups[$path])" })
                [IO.File]::WriteAllLines((Join-Path $work 'recovery.txt'), [string[]]$notes)
                throw "Rollback requires recovery. Backups retained at $work. $failure $($recovery -join '; ')"
            }
            for ($position = $script:createdDirectories.Count - 1; $position -ge 0; $position--) {
                $directory = $script:createdDirectories[$position]
                if ([IO.Directory]::Exists($directory) -and $directory -ine $installRoot -and
                    -not [IO.Directory]::EnumerateFileSystemEntries($directory).GetEnumerator().MoveNext()) {
                    [IO.Directory]::Delete($directory)
                }
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

    function Get-PandocArchive($Pin) {
        if ($distribution) {
            $path = Get-SafePath (Join-Path $distribution $Pin.Name)
            if (-not [IO.File]::Exists($path)) {
                throw "-DistributionDir needs $($Pin.Name) for the App's Pandoc; download it from $($Pin.Url)"
            }
            if ((New-Object IO.FileInfo $path).Length -gt $toolLimit) { throw 'Pandoc archive size limit exceeded' }
        } else {
            $path = Join-Path $work $Pin.Name
            Save-Remote $Pin.Url $path $toolLimit
        }
        if ((Get-FileDigest $path) -cne $Pin.Hash) { throw "Pandoc archive checksum mismatch: $($Pin.Name)" }
        return $path
    }

    function Expand-PandocMembers([string]$ArchivePath, $Take) {
        $result = New-Map
        $stream = [IO.File]::Open($ArchivePath, 'Open', 'Read', 'Read')
        $archive = New-Object IO.Compression.ZipArchive($stream, [IO.Compression.ZipArchiveMode]::Read)
        try {
            foreach ($member in $Take.Keys) {
                $entry = $archive.GetEntry($member)
                if ($null -eq $entry -or $entry.FullName -cne $member) { throw "Pandoc archive lacks $member" }
                $kind = ([long]$entry.ExternalAttributes -shr 16) -band 61440
                if ($kind -notin @(0, 32768) -or ($entry.ExternalAttributes -band 1040)) {
                    throw "Pandoc member is not an ordinary file: $member"
                }
                $staged = Join-Path $work ('pandoc-' + [Guid]::NewGuid().ToString('N'))
                $source = $entry.Open()
                try { $null = Copy-Bounded $source $staged $toolLimit }
                finally { $source.Dispose() }
                $result.Add($Take[$member], @{ Source = $staged; Hash = (Get-FileDigest $staged) })
            }
        } finally { $archive.Dispose(); $stream.Dispose() }
        return ,$result
    }

    function Get-OwnedPath([string]$Base, [string]$Name) {
        return [IO.Path]::GetFullPath((Join-Path $Base $Name.Replace('/', '\')))
    }

    function Add-Change([string]$Path, $Old, $New, $Source, [string]$Base, [string]$Kind = 'file') {
        $changes[$Path] = @{ Old = $Old; New = $New; Source = $Source; Base = $Base; Kind = $Kind }
    }

    function Add-PlaceRemoval($Place) {
        if ($Place.Kind -eq 'start') {
            $path = Join-Path $Place.Dir $startName
            if (Test-StartEntry $path) { Add-Change $path (Get-FileDigest $path) $null $null $Place.Dir 'start' }
            elseif (Test-Path -LiteralPath $path) { $notes.Add("Kept the Start menu entry ${path}: it no longer opens this Midden") }
            return
        }
        foreach ($name in $Place.Files.Keys) {
            $path = Get-OwnedPath $Place.Dir $name
            if (Test-Owned $path $Place.Files[$name]) { Add-Change $path $Place.Files[$name] $null $null $Place.Dir }
            elseif (Test-Path -LiteralPath $path) { $notes.Add("Kept ${path}: it was changed since installation") }
        }
    }

    function Add-PlaceWrite($Old, $New) {
        foreach ($name in $skillFiles.Keys) {
            $previous = if ($null -ne $Old -and $Old.Files.ContainsKey($name)) { $Old.Files[$name] } else { $null }
            $New.Files.Add($name, $skillFiles[$name].Hash)
            if ($null -ne $previous -and $previous -ceq $skillFiles[$name].Hash) { continue }
            Add-Change (Get-OwnedPath $New.Dir $name) $previous $skillFiles[$name].Hash $skillFiles[$name].Source $New.Dir
        }
        if ($null -ne $Old) {
            foreach ($name in $Old.Files.Keys) {
                if (-not $skillFiles.ContainsKey($name)) { Add-Change (Get-OwnedPath $New.Dir $name) $Old.Files[$name] $null $null $New.Dir }
            }
        }
    }

    function New-Record($Source) {
        return @{
            version = $Source.Version; commit = $Source.Commit; repository = $Source.Repository; target = $Source.Target
            Products = $Source.Products; Places = (New-List); RootFiles = $Source.RootFiles
            PathChange = $Source.PathChange; Pandoc = $Source.Pandoc
        }
    }

    $work = $null
    $script:retainWork = $false
    $script:createdDirectories = New-Object 'System.Collections.Generic.List[string]'
    try {
        if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) { throw 'Use install.sh on this platform' }
        $architecture = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
        $nativeTarget = if ($architecture -ieq 'AMD64') { 'windows/amd64' } elseif ($architecture -ieq 'ARM64') { 'windows/arm64' } else {
            throw "Unsupported native Windows architecture: $architecture"
        }
        if ($Options.Repository -cnotmatch '^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9][A-Za-z0-9._-]{0,99}$') {
            throw 'Repository must be an explicit owner/name'
        }
        if (([int]$Options.Upgrade + [int]$Options.Verify + [int]$Options.Uninstall) -gt 1) {
            throw 'Upgrade, Verify and Uninstall are mutually exclusive'
        }
        $operation = if ($Options.Verify) { 'verify' } elseif ($Options.Uninstall) { 'uninstall' } elseif ($Options.Upgrade) { 'upgrade' } else { 'install' }
        $harnesses = @($Options.Harness | ForEach-Object { "$_".Split(',') } | ForEach-Object { $_.Trim() } |
            Where-Object { $_ } | Select-Object -Unique)
        foreach ($name in $harnesses) {
            if ($name -cnotin @('copilot', 'claude', 'agents')) { throw "Unknown harness '$name'; use copilot, claude or agents" }
        }
        if ($operation -in @('install', 'upgrade')) {
            if ($Options.Mode -eq 'bundle' -and -not $harnesses.Count) { throw '-Mode bundle needs -Harness copilot, claude or agents' }
            if ($harnesses.Count -and $Options.Mode -ne 'bundle') { throw '-Harness goes with -Mode bundle' }
        } elseif ($operation -eq 'verify' -and ($harnesses.Count -or $Options.Project)) {
            throw '-Verify checks the whole installation; leave out -Harness and -Project'
        }
        if ($Options.Project -and -not $harnesses.Count) { throw '-Project goes with -Harness' }
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
        $projectRoot = $null
        if ($Options.Project) {
            $projectRoot = Get-SafePath $Options.Project
            if (-not [IO.Directory]::Exists($projectRoot)) { throw '-Project must be an existing folder' }
        }
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
        if ($projectRoot) {
            if ((Test-Within $installRoot $projectRoot) -or (Test-Within $projectRoot $installRoot)) {
                throw 'The installation folder and the project must not overlap'
            }
            $protectedPaths += $projectRoot
        }
        $receiptPath = Get-SafePath (Join-Path $installRoot $receiptName)
        $lockPath = Get-SafePath (Join-Path $installRoot $lockName)
        if (Test-Path -LiteralPath $lockPath) { throw 'Pending installation lock; inspect the active/stale operation before retrying' }
        $owned = Read-Receipt
        $legacyPath = if ($owned -and $owned.Legacy) { $owned.LegacyPath } else { $null }
        if ($operation -ne 'install' -and -not $owned) { throw "No Midden installation in $installRoot; nothing to $operation" }
        $scope = if ($projectRoot) { 'project' } else { 'user' }
        $requestedPlaces = New-List
        foreach ($name in $harnesses) { $requestedPlaces.Add((New-Place '' 'skills' $name $scope '' (Get-SkillDir $name $scope))) }
        $notes = New-List
        $changes = [ordered]@{}
        $pathChange = $null
        $release = $null
        $record = $null
        $final = New-List
        $written = New-List
        $startPlace = $null
        $fetchPandoc = $false
        $pin = $null
        $take = $null
        $launchApp = $Options.Mode -in @('', 'app') -and $operation -in @('install', 'upgrade') -and -not $Options.NoLaunch

        if ($operation -eq 'verify') {
            $problems = New-List
            foreach ($name in $owned.RootFiles.Keys) {
                $path = Get-OwnedPath $installRoot $name
                if (-not (Test-Owned $path $owned.RootFiles[$name])) { $problems.Add($path) }
            }
            foreach ($place in $owned.Places) {
                if ($place.Kind -eq 'start') {
                    $path = Join-Path $place.Dir $startName
                    if (-not (Test-StartEntry $path)) { $problems.Add($path) }
                    continue
                }
                foreach ($name in $place.Files.Keys) {
                    $path = Get-OwnedPath $place.Dir $name
                    if (-not (Test-Owned $path $place.Files[$name])) { $problems.Add($path) }
                }
            }
            if ($problems.Count) {
                throw ("These Midden files were changed or removed since installation:`n  " + [string]::Join("`n  ", [string[]]$problems))
            }
            Write-Host "Verified Midden $($owned.Version) ($([string]::Join(', ', [string[]]$owned.Products))) in ${installRoot}: every owned file matches the receipt"
            foreach ($place in $owned.Places) {
                if ($place.Kind -eq 'skills' -and $place.Version -cne $owned.Version) {
                    Write-Host "Older skills ($($place.Version)) for $($place.Harness) in $($place.Dir)"
                }
            }
            if ($owned.Legacy) { Write-Host 'Installed by the 0.3 installer; run with -Upgrade to replace it' }
            return
        }

        if ($operation -eq 'uninstall') {
            if ($harnesses.Count) {
                $removing = New-List
                foreach ($wanted in $requestedPlaces) {
                    $match = @($owned.Places | Where-Object { $_.Kind -eq 'skills' -and $_.Harness -ceq $wanted.Harness -and $_.Dir -ieq $wanted.Dir })
                    if (-not $match.Count) { throw "Midden has no skills for $($wanted.Harness) in $($wanted.Dir)" }
                    $removing.Add($match[0])
                }
                foreach ($place in $removing) { Add-PlaceRemoval $place }
                $record = New-Record $owned
                foreach ($place in $owned.Places) { if (-not $removing.Contains($place)) { $record.Places.Add($place) } }
            } else {
                foreach ($name in $owned.RootFiles.Keys) {
                    $path = Get-OwnedPath $installRoot $name
                    Check-Expected $path $owned.RootFiles[$name]
                    Add-Change $path $owned.RootFiles[$name] $null $null $installRoot
                }
                foreach ($place in $owned.Places) { Add-PlaceRemoval $place }
                Add-Change $(if ($legacyPath) { $legacyPath } else { $receiptPath }) $owned.Hash $null $null $installRoot 'receipt'
                if ($null -ne $owned.PathChange -and -not $Options.NoPath) {
                    $currentPath = Get-UserPathState
                    if (Test-UserPathState $currentPath $owned.PathChange.after) {
                        $pathChange = @{ before = $currentPath; after = $owned.PathChange.before }
                    } else {
                        $notes.Add('The user PATH was changed since installation; it was left as it is')
                    }
                }
            }
        } else {
            if ($owned -and $owned.Legacy -and $operation -eq 'install') {
                throw "Midden $($owned.Version) was installed by the 0.3 installer; run this with -Upgrade to replace it"
            }
            $downloadBase = $null
            if (-not $distribution) {
                if ($requestedVersion -eq 'latest') {
                    $latest = Get-PropertyMap (Convert-Json (Get-RemoteBytes "https://api.github.com/repos/$($Options.Repository)/releases/latest" $metadataLimit))
                    if (-not $latest.ContainsKey('tag_name') -or $latest['tag_name'] -isnot [string] -or -not $latest['tag_name'].StartsWith('v')) {
                        throw 'Latest release has an invalid tag'
                    }
                    $requestedVersion = Assert-Version $latest['tag_name'].Substring(1)
                }
                $downloadBase = "https://github.com/$($Options.Repository)/releases/download/v$requestedVersion"
            }
            $release = Read-Release
            if ($operation -eq 'install' -and $owned -and $owned.Version -cne $release.Version) {
                throw ("Midden $($owned.Version) is installed. Run with -Upgrade to move it to $($release.Version), " +
                    "or add -Version $($owned.Version) to add to it.")
            }
            $target = $nativeTarget
            if ($target -cnotin $release.Targets) {
                if ($target -eq 'windows/arm64' -and 'windows/amd64' -cin $release.Targets) { $target = 'windows/amd64' }
                else { throw "The release has no build for $nativeTarget" }
            }
            $requested = switch ($Options.Mode) { 'core' { @('core') } 'bundle' { @('core', 'bundle') } 'app' { $products } default { if ($operation -eq 'install') { $products } else { @() } } }
            foreach ($product in $products) {
                if (($owned -and $owned.Products.Contains($product)) -or $product -cin $requested) { $final.Add($product) }
            }
            $selected = New-Map
            foreach ($product in $final) {
                $key = if ($product -eq 'bundle') { 'bundle|universal' } else { "$product|$target" }
                $name = $release.Archives[$key]
                $bytes = Get-Asset $name $archiveLimit
                if ((Get-Digest $bytes) -cne $release.Sums[$name]) { throw "Archive checksum mismatch: $name" }
                $files = Read-Archive $bytes $release.Files[$key]
                Assert-ProductFiles $product $files
                $selected.Add($product, $files)
            }
            if ((Get-Digest $selected['core']['midden.exe']) -cne (Assert-Hash $release.Core[$target])) {
                throw 'Core binary checksum differs from build metadata'
            }
            if ($final.Contains('app') -and (Get-Digest $selected['app']['midden-ui.exe']) -cne (Assert-Hash $release.App[$target])) {
                throw 'App binary checksum differs from build metadata'
            }
            $desired = New-Map
            foreach ($product in $final) {
                foreach ($name in $selected[$product].Keys) {
                    $desired.Add($name, @{ Source = $selected[$product][$name]; Hash = (Get-Digest $selected[$product][$name]); Kind = 'file' })
                }
            }
            if ($final.Contains('app')) {
                $pin = $release.Fetch[$target]
                $take = $release.Take[$target]
                $reuse = $owned -and $null -ne $owned.Pandoc -and $owned.Pandoc.Hash -ceq $pin.Hash
                foreach ($destination in $take.Values) {
                    if (-not ($reuse -and $owned.RootFiles.ContainsKey($destination) -and
                        (Test-Owned (Get-OwnedPath $installRoot $destination) $owned.RootFiles[$destination]))) { $reuse = $false }
                }
                foreach ($destination in $take.Values) {
                    $desired.Add($destination, $(if ($reuse) {
                        @{ Source = $null; Hash = $owned.RootFiles[$destination]; Kind = 'keep' }
                    } else { @{ Source = $null; Hash = $null; Kind = 'pandoc' } }))
                }
                $fetchPandoc = -not $reuse
            }
            Assert-Paths @($desired.Keys)
            foreach ($name in $desired.Keys) {
                $entry = $desired[$name]
                if ($entry.Kind -eq 'keep') { continue }
                $path = Get-OwnedPath $installRoot $name
                $old = if ($owned -and $owned.RootFiles.ContainsKey($name)) { $owned.RootFiles[$name] } else { $null }
                if ($null -ne $old -and $old -ceq $entry.Hash) { Check-Expected $path $old; continue }
                Add-Change $path $old $entry.Hash $entry.Source $installRoot $entry.Kind
            }
            if ($owned) {
                foreach ($name in $owned.RootFiles.Keys) {
                    if (-not $desired.ContainsKey($name)) { Add-Change (Get-OwnedPath $installRoot $name) $owned.RootFiles[$name] $null $null $installRoot }
                }
            }
            $skillFiles = New-Map
            if ($final.Contains('bundle')) {
                foreach ($name in $selected['bundle'].Keys) {
                    $skillFiles.Add($name.Substring('skills/'.Length), $desired[$name])
                }
            }
            $places = New-List
            $nextId = 1
            if ($owned) { foreach ($place in $owned.Places) { $nextId = [Math]::Max($nextId, [int]$place.Id + 1) } }
            if ($owned) {
                foreach ($place in $owned.Places) {
                    if ($place.Kind -ne 'skills') { continue }
                    $wanted = @($requestedPlaces | Where-Object { $_.Dir -ieq $place.Dir })
                    if (-not ($wanted.Count -or ($operation -eq 'upgrade' -and $place.Scope -eq 'user'))) { $places.Add($place); continue }
                    $changed = @($place.Files.Keys | Where-Object { -not (Test-Owned (Get-OwnedPath $place.Dir $_) $place.Files[$_]) })
                    if ($changed.Count) {
                        if ($wanted.Count) {
                            throw "The skills in $($place.Dir) were changed since Midden installed them; restore or move them, then run this again"
                        }
                        $notes.Add("Left the skills in $($place.Dir) at $($place.Version): they were changed since installation")
                        $places.Add($place)
                        continue
                    }
                    $new = New-Place $place.Id 'skills' $place.Harness $place.Scope $release.Version $place.Dir
                    Add-PlaceWrite $place $new
                    $places.Add($new)
                    $written.Add($new)
                }
            }
            foreach ($wanted in $requestedPlaces) {
                if ($owned -and @($owned.Places | Where-Object { $_.Dir -ieq $wanted.Dir }).Count) { continue }
                $new = New-Place ([string]$nextId) 'skills' $wanted.Harness $wanted.Scope $release.Version $wanted.Dir
                $nextId++
                Add-PlaceWrite $null $new
                $places.Add($new)
                $written.Add($new)
            }
            if ($final.Contains('app')) {
                $startDir = Get-StartDir
                $startPath = Join-Path $startDir $startName
                $oldStart = @(if ($owned) { $owned.Places | Where-Object { $_.Kind -eq 'start' } })
                if ($oldStart.Count -and (Test-Path -LiteralPath $startPath) -and -not (Test-StartEntry $startPath)) {
                    $notes.Add("Kept the Start menu entry ${startPath}: it no longer opens this Midden")
                    $places.Add($oldStart[0])
                } else {
                    $old = if ($oldStart.Count -and (Test-Path -LiteralPath $startPath)) { Get-FileDigest $startPath } else { $null }
                    $id = if ($oldStart.Count) { $oldStart[0].Id } else { [string]$nextId }
                    $startPlace = New-Place $id 'start' '-' '-' $release.Version $startDir
                    $places.Add($startPlace)
                    Add-Change $startPath $old $null $null $startDir 'start'
                }
            }
            $recordedPath = if ($owned) { $owned.PathChange } else { $null }
            if (-not $Options.NoPath) {
                $currentPath = Get-UserPathState
                if (-not $recordedPath -and -not (Test-PathContainsInstall $currentPath)) {
                    $pathChange = New-UserPathChange $currentPath
                    $recordedPath = $pathChange
                    if ($pathChange.after.value -match "[\t\r\n]") {
                        throw 'The user PATH contains a tab or line break, so it cannot be recorded; rerun with -NoPath'
                    }
                }
            }
            $record = @{
                version = $release.Version; commit = $release.Commit; repository = $Options.Repository; target = $target
                Products = $final; Places = $places; RootFiles = (New-Map); PathChange = $recordedPath
                Pandoc = $(if ($pin) { @{ Url = $pin.Url; Hash = $pin.Hash } } else { $null })
            }
            foreach ($name in $desired.Keys) { $record.RootFiles[$name] = $desired[$name].Hash }
            $null = Assert-Field $installRoot 'The installation folder'
            foreach ($place in $places) { $null = Assert-Field $place.Dir 'A skills folder' }
        }
        foreach ($path in @($changes.Keys)) {
            if ($changes[$path].Kind -ne 'start' -or $null -eq $changes[$path].Old) { Check-Expected $path $changes[$path].Old }
        }

        if ($Options.DryRun) {
            if ($fetchPandoc -and $distribution) { $null = Get-PandocArchive $pin }
            $destinations = New-List
            foreach ($path in $changes.Keys) {
                $change = $changes[$path]
                $action = if ($change.Kind -eq 'pandoc') { 'fetch' } elseif ($null -eq $change.Old) { 'create' } elseif ($null -eq $change.New -and $change.Kind -notin @('start')) { 'remove' } else { 'replace' }
                if ($change.Kind -eq 'start' -and $operation -eq 'uninstall') { $action = 'remove' }
                $destinations.Add(@{ path = $path; action = $action })
            }
            if ($null -ne $record) { $destinations.Add(@{ path = $receiptPath; action = $(if ($owned -and -not $owned.Legacy) { 'replace' } else { 'create' }) }) }
            if ($legacyPath -and $operation -ne 'uninstall') { $destinations.Add(@{ path = $legacyPath; action = 'remove' }) }
            $planProducts = @(if ($null -ne $record) { $record.Products } elseif ($owned) { $owned.Products })
            $planSkills = @(if ($null -ne $record) { foreach ($place in $record.Places) { if ($place.Kind -eq 'skills') { $place.Dir } } })
            # @() over a List[object] throws in Windows PowerShell and PowerShell 7; ToArray() does not.
            $planNotes = @('Nothing was written, extracted, probed or launched.',
                'Unsigned publisher. Checksums establish integrity, not cryptographic authenticity.') + $notes.ToArray() +
                @(if ($fetchPandoc -and -not $distribution) { "Pandoc is downloaded from $($pin.Url) when installing" })
            @{
                dry_run = $true; operation = $operation; mode = $Options.Mode
                version = $(if ($release) { $release.Version } else { $owned.Version })
                repository = $Options.Repository; install_dir = $installRoot
                products = $planProducts
                skills = $planSkills
                destinations = $destinations.ToArray()
                path_action = $(if ($null -ne $pathChange) { 'user PATH only; never the machine PATH' } else { 'unchanged' })
                notes = $planNotes
            } | ConvertTo-Json -Depth 10
            return
        }

        $work = New-PrivateWork
        if ($null -ne $release) {
            $stage = Join-Path $work 'payload'
            $probes = New-Map
            $probes.Add('midden.exe', $selected['core']['midden.exe'])
            if ($final.Contains('app')) { $probes.Add('midden-ui.exe', $selected['app']['midden-ui.exe']) }
            Write-Snapshot $stage $probes
            Probe-Version (Join-Path $stage 'midden.exe') 'version' "midden $($release.Version)"
            if ($final.Contains('app')) { Probe-Version (Join-Path $stage 'midden-ui.exe') '--version' "midden-ui $($release.Version)" }
            if ($fetchPandoc) {
                $members = Expand-PandocMembers (Get-PandocArchive $pin) $take
                foreach ($destination in $members.Keys) {
                    $record.RootFiles[$destination] = $members[$destination].Hash
                    $path = Get-OwnedPath $installRoot $destination
                    $old = $changes[$path].Old
                    if ($null -ne $old -and $old -ceq $members[$destination].Hash) { $changes.Remove($path) }
                    else { Add-Change $path $old $members[$destination].Hash $members[$destination].Source $installRoot }
                }
            }
            if ($null -ne $startPlace) {
                $staged = Join-Path $work $startName
                New-StartEntry $staged
                $bytes = [IO.File]::ReadAllBytes($staged)
                $startPlace.Files[$startName] = Get-Digest $bytes
                $path = Join-Path $startPlace.Dir $startName
                Add-Change $path $changes[$path].Old $startPlace.Files[$startName] $bytes $startPlace.Dir 'start'
            }
            if ($legacyPath) { Add-Change $legacyPath $owned.Hash $null $null $installRoot 'receipt' }
        }
        if ($null -ne $record) {
            $bytes = Format-Receipt $record
            Add-Change $receiptPath $(if ($owned -and -not $owned.Legacy) { $owned.Hash } else { $null }) (Get-Digest $bytes) $bytes $installRoot 'receipt'
        }
        Apply-Transaction $changes $pathChange
        foreach ($path in @($changes.Keys)) {
            if ($null -eq $changes[$path].New) { Remove-EmptyParents $path $changes[$path].Base }
        }
        if ($operation -eq 'uninstall' -and $null -eq $record -and [IO.Directory]::Exists($installRoot) -and
            -not [IO.Directory]::EnumerateFileSystemEntries($installRoot).GetEnumerator().MoveNext()) {
            [IO.Directory]::Delete($installRoot)
        }
        if ($operation -eq 'uninstall') {
            if ($null -ne $record) {
                Write-Host "Removed Midden's skills for $([string]::Join(', ', [string[]]$harnesses)) from $([string]::Join(', ', [string[]]@($requestedPlaces | ForEach-Object { $_.Dir })))"
            } else {
                Write-Host 'Removed unchanged Midden files. Core''s state, the App''s data and files Midden does not own were kept.'
            }
            foreach ($note in $notes) { Write-Host $note }
            return
        }
        Write-Host "Installed Midden $($release.Version) ($([string]::Join(', ', [string[]]$final))) in $installRoot"
        foreach ($place in $written) { Write-Host "Skills for $($place.Harness): $($place.Dir)" }
        foreach ($place in $record.Places) {
            if ($place.Kind -eq 'skills' -and $place.Scope -eq 'project' -and $place.Version -cne $release.Version) {
                $folder = Get-ProjectOf $place
                Write-Host "Older skills ($($place.Version)) in ${folder}; update them with: install.ps1 -Mode bundle -Harness $($place.Harness) -Project '$folder'"
            }
        }
        foreach ($note in $notes) { Write-Host $note }
        Write-Host 'Unsigned publisher. Checksums establish integrity, not cryptographic authenticity.'
        if ($launchApp -and $final.Contains('app')) {
            $arguments = @()
            if ($Options.NoOpen) { $arguments += '--no-open' }
            & (Join-Path $installRoot 'midden-ui.exe') @arguments
            if ($LASTEXITCODE -ne 0) { throw "The installed App exited with status $LASTEXITCODE" }
        }
    } catch {
        throw "midden install: $($_.Exception.Message)"
    } finally {
        if ($work -and -not $script:retainWork) { Remove-Work $work }
    }
} @{
    Mode = $Mode; Harness = @($Harness); Project = $Project; Version = $Version; InstallDir = $InstallDir
    DistributionDir = $DistributionDir; Repository = $Repository
    NoPath = [bool]$NoPath; NoLaunch = [bool]$NoLaunch; NoOpen = [bool]$NoOpen
    Upgrade = [bool]$Upgrade; Verify = [bool]$Verify; Uninstall = [bool]$Uninstall; DryRun = [bool]$DryRun
    SourcePath = $MyInvocation.MyCommand.Path
}

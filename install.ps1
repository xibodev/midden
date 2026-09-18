#requires -Version 7.0
# Script-owned agentic CLI installation. No Midden setup/install command is used.
[CmdletBinding()]
param(
    [string]$Version = 'latest',
    [string]$BundleDir = '',
    [string]$Manifest = '',
    [string[]]$Hosts = @(),
    [string]$HostPath = '',
    [ValidateSet('user','project')][string]$Scope = 'user',
    [string]$ProjectDir = '',
    [string]$InstallDir = '',
    [string]$StateDir = '',
    [string]$HomeDir = $HOME,
    [string[]]$Dependencies = @(),
    [string]$PandocPath = '',
    [string]$D2Path = '',
    [switch]$NonInteractive,
    [switch]$DryRun,
    [switch]$Upgrade,
    [switch]$Uninstall,
    [switch]$Verify,
    [switch]$AddPath
)
$ErrorActionPreference = 'Stop'
if (-not $IsWindows) { throw 'Use install.sh on Linux/macOS.' }
if (-not $PSCommandPath) { throw 'Save this script to disk and run it with pwsh -File.' }
if (-not $Manifest) {
    $Manifest = Join-Path $PSScriptRoot 'installer/manifest.tsv'
    if (-not (Test-Path -LiteralPath $Manifest)) { $Manifest = Join-Path $PSScriptRoot 'manifest.tsv' }
}
if ($HomeDir -ne $HOME -and $AddPath) { throw '-HomeDir isolation cannot be combined with -AddPath (Windows user PATH is outside that directory).' }
function Ask([string]$Label, [string]$Default) {
    $answer = Read-Host "$Label [$Default]"
    if ([string]::IsNullOrWhiteSpace($answer)) { return $Default }
    return $answer.Trim()
}
function Safe-Path([string]$Path) {
    if (-not [IO.Path]::IsPathFullyQualified($Path) -or $Path -match "[`r`n`t]") { throw "Absolute, single-line path required: $Path" }
    $cursor = [IO.Path]::GetFullPath($Path)
    while ($cursor) {
        $item = Get-Item -LiteralPath $cursor -Force -ErrorAction SilentlyContinue
        if ($item -and ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw "Refusing linked install path: $cursor" }
        $parent = Split-Path -Parent $cursor
        if ($parent -eq $cursor) { break }
        $cursor = $parent
    }
}
function Hash([string]$Path) { return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant() }
function Run-Checked([string]$Program, [string[]]$Arguments) {
    & $Program @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$Program failed ($LASTEXITCODE)." }
}

Safe-Path $Manifest
$settings = @{}; $hostRows = @{}; $skills = @(); $deps = @{}; $overlays = @()
foreach ($line in [IO.File]::ReadAllLines($Manifest)) {
    if (-not $line -or $line.StartsWith('#')) { continue }
    $p = $line.Split("`t")
    switch ($p[0]) {
        'setting' { $settings[$p[1]] = $p[2] }
        'host' { $hostRows[$p[1]] = $p }
        'skill' { $skills += ,$p }
        'overlay' { $overlays += $p[1] }
        'dependency' { $deps[$p[1]] = $p }
        'related' { Write-Host "$($p[1]): $($p[2]) — $($p[3])" }
        default { throw "Unknown manifest record: $($p[0])" }
    }
}
if ($settings.schema -ne '1' -or $settings.repository -notmatch '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$') { throw 'Invalid installer manifest.' }
foreach ($relative in @($skills | ForEach-Object { $_[2] }) + $overlays + @($hostRows.Values | ForEach-Object { $_[3]; $_[4] })) {
    if ($relative -and ([IO.Path]::IsPathRooted($relative) -or $relative -match '(^|[/\\])\.\.([/\\]|$)')) { throw 'Unsafe manifest path.' }
}
foreach ($skill in $skills) { if ($skill[1] -notmatch '^[a-z0-9-]+$') { throw 'Invalid skill directory in manifest.' } }
if (-not $InstallDir) { $InstallDir = Join-Path $HomeDir $settings.install_relative }
if (-not $StateDir) { $StateDir = Join-Path $HomeDir $settings.state_relative }
Safe-Path $HomeDir; Safe-Path $InstallDir; Safe-Path $StateDir
$InstallDir = [IO.Path]::GetFullPath($InstallDir).TrimEnd('\','/')
$StateDir = [IO.Path]::GetFullPath($StateDir).TrimEnd('\','/')
$receiptPath = Join-Path $InstallDir 'install-receipt.json'
Safe-Path $receiptPath
$receipt = if (Test-Path -LiteralPath $receiptPath) { Get-Content -LiteralPath $receiptPath -Raw | ConvertFrom-Json -AsHashtable } else { $null }
function Skill-Root([string]$HostID, [string]$ChosenScope, [string]$Project) {
    if (-not $hostRows.ContainsKey($HostID)) { throw "Unsupported CLI: $HostID" }
    if ($ChosenScope -eq 'project') { Safe-Path $Project; return Join-Path $Project $hostRows[$HostID][4] }
    return Join-Path $HomeDir $hostRows[$HostID][3]
}
function Check-Receipt($Record) {
    if (-not $Record -or $Record.schema -ne 1) { throw 'No supported installation receipt.' }
    $allowed = @((Join-Path $InstallDir 'midden.exe'))
    foreach ($id in $Record.hosts) {
        $root = Skill-Root $id $Record.scope $Record.project
        foreach ($skill in $skills) { $allowed += Join-Path $root "$($skill[1])/SKILL.md" }
    }
    foreach ($file in $Record.files) {
        Safe-Path $file.path
        if ($file.path -notin $allowed -or $file.sha256 -notmatch '^[a-f0-9]{64}$') { throw 'Invalid receipt file entry.' }
        if (-not (Test-Path -LiteralPath $file.path -PathType Leaf) -or (Hash $file.path) -ne $file.sha256) { throw "Preserving missing/modified installation: $($file.path)" }
    }
}
if ($Verify -or $Uninstall) {
    Check-Receipt $receipt
    if ($Verify) { Write-Host 'Installed bytes and guidance verified. Agentic CLI acceptance is yours.'; return }
    $receipt.files | ForEach-Object { Write-Host "Remove owned file: $($_.path)" }
    Write-Host "Preserve recovery data: $($receipt.state)"
    if ($DryRun) { return }
    if (-not $NonInteractive -and (Ask 'Uninstall? yes/no' 'no') -ne 'yes') { return }
    foreach ($file in $receipt.files) { Remove-Item -LiteralPath $file.path }
    if ($receipt.path_added) {
        $old = [string][Environment]::GetEnvironmentVariable('Path','User')
        [Environment]::SetEnvironmentVariable('Path', (($old -split ';' | Where-Object { $_.TrimEnd('\') -ine $InstallDir }) -join ';'),'User')
    }
    Remove-Item -LiteralPath $receiptPath
    Write-Host 'Uninstalled. Recovery data and upgrade backups preserved.'
    return
}
Write-Host 'Midden — install for your agentic CLI'
foreach ($id in ($hostRows.Keys | Sort-Object)) {
    $detected = Get-Command $hostRows[$id][2] -ErrorAction SilentlyContinue
    Write-Host "  ${id}: $(if ($detected) { $detected.Source } else { 'not detected' })"
}
if (-not $NonInteractive) {
    if (-not $Hosts.Count) { $Hosts = (Ask 'Select CLI hosts (comma-separated)' 'copilot-cli').Split(',') }
    $Scope = Ask 'Scope: user or project' $Scope
    if ($Scope -notin @('user','project')) { throw 'Invalid scope.' }
    if ($Scope -eq 'project') { $ProjectDir = Ask 'Absolute project directory' $ProjectDir }
    $InstallDir = Ask 'Binary installation directory' $InstallDir
    $StateDir = Ask 'Recovery state directory' $StateDir
}
Safe-Path $InstallDir; Safe-Path $StateDir
if ($InstallDir -eq $StateDir) { throw 'Binary and state directories must differ.' }
# Reload after an interactive destination change.
$receiptPath = Join-Path $InstallDir 'install-receipt.json'; Safe-Path $receiptPath
$receipt = if (Test-Path -LiteralPath $receiptPath) { Get-Content -LiteralPath $receiptPath -Raw | ConvertFrom-Json -AsHashtable } else { $null }
if ($receipt) {
    Check-Receipt $receipt
    if ($receipt.scope -ne $Scope -or $receipt.project -ne $ProjectDir -or $receipt.state -ne $StateDir) { throw 'Use a separate installation directory for a different scope or state location.' }
    $Hosts += $receipt.hosts
}
$Hosts = @($Hosts | ForEach-Object { $_.Split(',') } | ForEach-Object { $_.Trim() } | Where-Object { $_ } | Sort-Object -Unique)
if (-not $Hosts.Count) { throw '-Hosts is required with -NonInteractive.' }
if ($HostPath -and $Hosts.Count -ne 1) { throw '-HostPath requires one selected host.' }
foreach ($id in $Hosts) {
    $null = Skill-Root $id $Scope $ProjectDir
    $program = if ($HostPath) { Safe-Path $HostPath; $HostPath } else { $hostRows[$id][2] }
    if (-not (Get-Command $program -ErrorAction SilentlyContinue)) { throw "Install/authenticate $id first, or supply -HostPath." }
    Run-Checked $program @('--version')
}
if ($Scope -eq 'project' -and -not (Test-Path -LiteralPath $ProjectDir -PathType Container)) { throw 'Project directory must exist.' }

$toolPaths = @{pandoc=$PandocPath; d2=$D2Path}
foreach ($id in ($deps.Keys | Sort-Object)) {
    if (-not $NonInteractive) {
        $p = Ask "Existing $id executable (auto to discover)" $(if ($toolPaths[$id]) { $toolPaths[$id] } else { 'auto' })
        $toolPaths[$id] = if ($p -eq 'auto') { '' } else { $p }
    }
    if ($toolPaths[$id]) { Safe-Path $toolPaths[$id] }
    $cmd = Get-Command $(if ($toolPaths[$id]) {$toolPaths[$id]} else {$id}) -ErrorAction SilentlyContinue
    if ($cmd) {
        $toolPaths[$id]=$cmd.Source
        Run-Checked $cmd.Source $(if ($id -eq 'd2') {@('version')} else {@('--version')})
    } elseif ($toolPaths[$id]) { throw "Missing executable: $($toolPaths[$id])" }
    $dep=$deps[$id]
    Write-Host "`n${id}: $($dep[3])"
    Write-Host "  Version policy: $($dep[2]); installed: $(if($cmd){$cmd.Source}else{'no'})"
    Write-Host "  Download: $(if($cmd){'0 — reuse'}else{$dep[4]}); additional disk: $(if($cmd){'0 — reuse'}else{$dep[5]})"
    Write-Host "  Optional install: winget install --exact --id $($dep[6]) --source winget (may request elevation; manager confirms version and total size)"
}
if (-not $NonInteractive) { $choice=Ask 'Optional packages to install/check (pandoc,d2 or none)' 'none'; $Dependencies=if($choice -eq 'none'){@()}else{$choice.Split(',')} }
$Dependencies = @($Dependencies | ForEach-Object { $_.Split(',') } | ForEach-Object { $_.Trim() } | Where-Object { $_ } | Sort-Object -Unique)
foreach ($id in $Dependencies) { if (-not $deps.ContainsKey($id)) { throw "Unknown dependency: $id" } }
if (-not $NonInteractive) { $AddPath = (Ask 'Add installation directory to user PATH? yes/no' $(if($AddPath){'yes'}else{'no'})) -eq 'yes' }
if ($HomeDir -ne $HOME -and $AddPath) { throw 'Isolated home cannot modify the real Windows user PATH.' }
Write-Host "`nBinary: $InstallDir; recovery state: $StateDir; PATH change: $AddPath"
foreach ($id in $Hosts) { Write-Host "Skills: $(Skill-Root $id $Scope $ProjectDir)" }
Write-Host 'No host model configuration or permission grants will be changed.'
if ($DryRun) { Write-Host 'Preview only. No download, package installation or files changed.'; return }
if (-not $NonInteractive -and (Ask 'Proceed? yes/no' 'no') -ne 'yes') { Write-Host 'Cancelled.'; return }

$stage = Join-Path ([IO.Path]::GetTempPath()) ('midden-installer-' + [guid]::NewGuid())
New-Item -ItemType Directory -Path $stage | Out-Null
$lock=$null; $undo=@(); $committed=$false; $pathChanged=$false; $pathBefore=''
try {
    if (-not $BundleDir) {
        if ($Version -eq 'latest') { $Version=(Invoke-RestMethod "https://api.github.com/repos/$($settings.repository)/releases/latest").tag_name }
        if ($Version -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$') { throw 'Invalid release tag; supply -Version for a public prerelease.' }
        $arch = switch ([Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()) { X64 {'amd64'} Arm64 {'arm64'} default {throw 'Unsupported architecture'} }
        $asset=$settings.asset.Replace('{version}',$Version.Substring(1)).Replace('{os}','windows').Replace('{arch}',$arch)+'.zip'
        $base="https://github.com/$($settings.repository)/releases/download/$Version"
        Invoke-WebRequest "$base/$asset" -OutFile (Join-Path $stage $asset)
        Invoke-WebRequest "$base/SHA256SUMS" -OutFile (Join-Path $stage 'SHA256SUMS')
        $sums=@([IO.File]::ReadAllLines((Join-Path $stage 'SHA256SUMS')) | Where-Object {$_ -match ('^[a-fA-F0-9]{64}\s+\*?'+[regex]::Escape($asset)+'$')})
        if ($sums.Count -ne 1 -or (Hash (Join-Path $stage $asset)) -ne ($sums[0] -split '\s+')[0]) { throw 'Checksum verification failed.' }
        $BundleDir=Join-Path $stage 'bundle'; New-Item -ItemType Directory $BundleDir | Out-Null
        $zip=[IO.Compression.ZipFile]::OpenRead((Join-Path $stage $asset))
        try {
            foreach ($relative in @('midden.exe') + @($skills | ForEach-Object {$_[2]}) + $overlays) {
                $entries=@($zip.Entries | Where-Object {$_.FullName -ceq $relative})
                if ($entries.Count -ne 1) { throw "Missing/duplicate archive entry: $relative" }
                $target=Join-Path $BundleDir $relative
                New-Item -ItemType Directory -Force (Split-Path -Parent $target) | Out-Null
                [IO.Compression.ZipFileExtensions]::ExtractToFile($entries[0],$target,$false)
            }
        } finally {$zip.Dispose()}
    }
    Safe-Path $BundleDir
    Safe-Path (Join-Path $BundleDir 'midden.exe')
    if ((Get-Item -LiteralPath (Join-Path $BundleDir 'midden.exe')).Length -eq 0) { throw 'Bundle executable is empty.' }
    $binary=Join-Path $InstallDir 'midden.exe'
    $files=@(@{path=$binary; source=(Join-Path $BundleDir 'midden.exe')})
    foreach ($id in $Hosts) {
        foreach ($skill in $skills) {
            $source=Join-Path $BundleDir $skill[2]; Safe-Path $source
            $content=[IO.File]::ReadAllText($source)
            if (-not $content.StartsWith('---') -or -not $content.Contains('name:')) { throw "Invalid skill content: $source" }
            if ($skill -eq $skills[0]) {foreach ($overlay in $overlays) {Safe-Path (Join-Path $BundleDir $overlay);$content+="`n`n"+[IO.File]::ReadAllText((Join-Path $BundleDir $overlay))}}
            $content+="`n`n## Installation binding`nBinary: ``$binary``. Invoke with PowerShell's & operator and a quoted path. Discover capabilities using module describe --json; execute using module invoke <capability> --input <absolute-request.json>.`nSupply roots.midden_home.path as ``$StateDir`` and mode rw. Preserve read-only source stores. Your agentic CLI owns models and permissions. Prefer authoring content in this host and submitting recipes.compose rather than launching another agent. Use the current descriptor to discover recipe, review, render and export operations.`n"
            foreach ($dep in $toolPaths.Keys) {if($toolPaths[$dep]){$content+="Optional $dep executable: ``$($toolPaths[$dep])``. Include its directory on PATH when invoking render operations.`n"}}
            $temp=Join-Path $stage "$id-$($skill[1]).md";[IO.File]::WriteAllText($temp,$content)
            $files+=@{path=(Join-Path (Skill-Root $id $Scope $ProjectDir) "$($skill[1])/SKILL.md");source=$temp}
        }
    }
    $known=@{};if($receipt){foreach($file in $receipt.files){$known[$file.path]=$file.sha256}}
    foreach($file in $files){Safe-Path $file.path;Safe-Path $file.source;if(Test-Path -LiteralPath $file.path){if(-not $known.ContainsKey($file.path)){throw "Preserving unowned file: $($file.path)"};if((Hash $file.path) -ne (Hash $file.source) -and -not $Upgrade){throw 'Installation differs; use -Upgrade to replace owned files with backups.'}}}
    # Dependencies are installed only after file-conflict preflight.
    foreach($id in $Dependencies){
        if(-not $toolPaths[$id]){Run-Checked 'winget' @('install','--exact','--id',$deps[$id][6],'--source','winget');$cmd=Get-Command $id -ErrorAction SilentlyContinue;if(-not $cmd){throw "$id is not visible on PATH yet; reopen the terminal and rerun with its explicit path."};$toolPaths[$id]=$cmd.Source}
        $checkFile=Join-Path $stage $(if($id -eq 'pandoc'){'check.pptx'}else{'check.svg'})
        if($id -eq 'pandoc'){'# Check' | & $toolPaths[$id] -f markdown -t pptx -o $checkFile}else{[IO.File]::WriteAllText((Join-Path $stage 'check.d2'),'source -> evidence');& $toolPaths[$id] (Join-Path $stage 'check.d2') $checkFile}
        if($LASTEXITCODE -ne 0){throw "$id functional check failed"}
        if (-not (Test-Path -LiteralPath $checkFile) -or (Get-Item -LiteralPath $checkFile).Length -eq 0) { throw "$id produced no test artifact." }
    }
    New-Item -ItemType Directory -Force $InstallDir | Out-Null
    $lockPath=Join-Path $InstallDir '.install.lock';$lock=[IO.File]::Open($lockPath,'CreateNew','Write','None')
    if($receipt){Check-Receipt $receipt}
    foreach($file in $files){Safe-Path $file.path;if((Test-Path -LiteralPath $file.path) -and -not $known.ContainsKey($file.path)){throw "Unowned path appeared during installation: $($file.path)"}}
    foreach($file in $files){
        if(Test-Path -LiteralPath $file.path){if((Hash $file.path) -eq (Hash $file.source)){continue};$backup="$($file.path).midden-backup-$([guid]::NewGuid())";Copy-Item -LiteralPath $file.path -Destination $backup;$undo+=@{path=$file.path;backup=$backup}}else{$undo+=@{path=$file.path;backup=''}}
        New-Item -ItemType Directory -Force (Split-Path -Parent $file.path) | Out-Null
        $temp="$($file.path).midden-new-$([guid]::NewGuid())"
        try {Copy-Item -LiteralPath $file.source -Destination $temp;Move-Item -LiteralPath $temp -Destination $file.path -Force}
        finally {if(Test-Path -LiteralPath $temp){Remove-Item -LiteralPath $temp}}
        if((Hash $file.source) -ne (Hash $file.path)){throw "Installed bytes do not match: $($file.path)"}
    }
    $record=@{schema=1;version=$Version;scope=$Scope;project=$ProjectDir;state=$StateDir;hosts=$Hosts;path_added=[bool]($receipt -and $receipt.path_added);files=@($files | ForEach-Object {@{path=$_.path;sha256=(Hash $_.path)}})}
    if($AddPath){$old=[string][Environment]::GetEnvironmentVariable('Path','User');if($InstallDir -notin ($old -split ';')){$pathBefore=$old;[Environment]::SetEnvironmentVariable('Path',($old.TrimEnd(';')+';'+$InstallDir).TrimStart(';'),'User');$pathChanged=$true;$record.path_added=$true}}
    Check-Receipt $record
    $receiptTemp="$receiptPath.new-$([guid]::NewGuid())"
    try {[IO.File]::WriteAllText($receiptTemp,($record | ConvertTo-Json -Depth 5)+"`n");Move-Item -LiteralPath $receiptTemp -Destination $receiptPath -Force}
    finally {if(Test-Path -LiteralPath $receiptTemp){Remove-Item -LiteralPath $receiptTemp}}
    $committed=$true
    Write-Host "Installed executable: $binary"
    Write-Host 'Restart your CLI if needed to discover midden-session-recovery. Host acceptance remains an operator check.'
    Write-Host 'First prompt: Use midden-session-recovery to assay recent project sessions and help me choose evidence for a blog post and presentation. Ask before extracting or writing.'
} finally {
    if(-not $committed -and $pathChanged){[Environment]::SetEnvironmentVariable('Path',$pathBefore,'User')}
    if(-not $committed){for($i=$undo.Count-1;$i -ge 0;$i--){$u=$undo[$i];if($u.backup){Copy-Item -LiteralPath $u.backup -Destination $u.path -Force}else{Remove-Item -LiteralPath $u.path -ErrorAction SilentlyContinue}}}
    if($lock){$lock.Dispose();Remove-Item -LiteralPath $lockPath}
    Remove-Item -LiteralPath $stage -Recurse -Force
}

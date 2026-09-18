#requires -Version 5.1
# Script-owned agentic CLI installation. No Midden setup/install command is used.
[CmdletBinding()]
param(
    [string]$Version = $env:MIDDEN_VERSION,
    [string]$BundleDir = '',
    [string]$Manifest = '',
    [string[]]$Hosts = @($env:MIDDEN_HOSTS -split ',' | Where-Object { $_ }),
    [string]$HostPath = '',
    [ValidateSet('user','project')][string]$Scope = 'user',
    [string]$ProjectDir = '',
    [string]$InstallDir = $env:MIDDEN_INSTALL_DIR,
    [string]$StateDir = $env:MIDDEN_STATE_DIR,
    [string]$HomeDir = $HOME,
    [string[]]$Dependencies = @($env:MIDDEN_DEPENDENCIES -split ',' | Where-Object { $_ }),
    [string]$PandocPath = '',
    [string]$D2Path = '',
    [switch]$NonInteractive = ($env:MIDDEN_YES -eq '1'),
    [switch]$DryRun,
    [switch]$Upgrade,
    [switch]$Uninstall,
    [switch]$Verify,
    [switch]$AddPath,
    [switch]$NoPath = ($env:MIDDEN_NO_PATH -eq '1'),
    [ValidateSet('auto','quick','custom')][string]$Setup = 'auto',
    [switch]$Plain = ($env:MIDDEN_PLAIN -eq '1')
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
if ($env:OS -ne 'Windows_NT') { throw 'Use install.sh on Linux/macOS.' }
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
Add-Type -AssemblyName System.IO.Compression.FileSystem
if (-not $PSCommandPath) { throw 'Save this script to disk and run it with pwsh -File.' }
if (-not $Manifest) {
    $Manifest = Join-Path $PSScriptRoot 'installer/manifest.tsv'
    if (-not (Test-Path -LiteralPath $Manifest)) { $Manifest = Join-Path $PSScriptRoot 'manifest.tsv' }
}
if ($HomeDir -ne $HOME -and $AddPath) { throw '-HomeDir isolation cannot be combined with -AddPath (Windows user PATH is outside that directory).' }
$rich = -not $Plain -and -not $NonInteractive -and -not [Console]::IsInputRedirected -and -not [Console]::IsOutputRedirected
$color = $rich -and -not $env:NO_COLOR
function Accent([string]$Text) {
    if ($color) { Write-Host $Text -ForegroundColor Cyan } else { Write-Host $Text }
}
function Section([string]$Text) { Write-Host ''; Accent "  +-- $Text" }
function Choose([string]$Label, [string[]]$Options, [int]$Default=0) {
    Write-Host ''; Accent "  ? $Label"
    if ($rich -and [Console]::WindowWidth -ge 40 -and [Console]::WindowHeight -ge ($Options.Count+8)) {
        $selected=$Default
        $oldControl=[Console]::TreatControlCAsInput
        try {
            [Console]::TreatControlCAsInput=$true
            while ($true) {
                $width=[Math]::Max(20,[Console]::WindowWidth-4)
                for($i=0;$i -lt $Options.Count;$i++) {
                    $prefix=if($i -eq $selected){'  > (*) '}else{'    ( ) '}
                    $line=$prefix+$Options[$i]
                    if($line.Length -gt $width){$line=$line.Substring(0,$width-3)+'...'}
                    if($i -eq $selected){Accent $line}else{Write-Host $line}
                }
                Write-Host '    Up/Down: move | Enter: select | Q: cancel'
                $key=[Console]::ReadKey($true)
                if($key.Key -eq 'Enter'){break}
                if($key.Key -eq 'Q' -or ($key.Key -eq 'C' -and $key.Modifiers -band [ConsoleModifiers]::Control)){throw 'Setup cancelled. No installation changes made.'}
                if($key.Key -eq 'UpArrow'){$selected=($selected+$Options.Count-1)%$Options.Count}
                if($key.Key -eq 'DownArrow'){$selected=($selected+1)%$Options.Count}
                if([Console]::CursorTop -ge ($Options.Count+1)){
                    [Console]::SetCursorPosition(0,[Console]::CursorTop-$Options.Count-1)
                    for($i=0;$i -le $Options.Count;$i++){[Console]::WriteLine((' '*([Console]::WindowWidth-1)))}
                    [Console]::SetCursorPosition(0,[Console]::CursorTop-$Options.Count-1)
                }
            }
        } finally { [Console]::TreatControlCAsInput=$oldControl }
        Step 'selected' $Options[$selected]
        return $selected
    }
    for($i=0;$i -lt $Options.Count;$i++){Write-Host "    $($i+1)) $($Options[$i])"}
    while($true){
        $reply=Ask '  Choice (q to cancel)' ([string]($Default+1))
        if($reply -eq 'q'){throw 'Setup cancelled. No installation changes made.'}
        $n=0
        if([int]::TryParse($reply,[ref]$n) -and $n -ge 1 -and $n -le $Options.Count){Step 'selected' $Options[$n-1];return ($n-1)}
        Write-Host '  Choose a listed number.'
    }
}
function Ask([string]$Label, [string]$Default) {
    if([Console]::IsInputRedirected){
        Write-Host "$Label [$Default]: " -NoNewline
        $answer=[Console]::ReadLine()
        if($null -eq $answer){throw 'Input ended; rerun interactively or use -NonInteractive.'}
    }else{$answer = Read-Host "$Label [$Default]"}
    if ([string]::IsNullOrWhiteSpace($answer)) { return $Default }
    return $answer.Trim()
}
function Confirm([string]$Label) {
    if($rich){return ((Choose $Label @('Yes - continue','No - cancel')) -eq 0)}
    while ($true) {
        switch -Regex (Ask $Label 'Y/n') {
            '^(y|yes|Y/n)$' { return $true }
            '^(n|no)$' { return $false }
            default { Write-Host '  Please enter yes or no.' }
        }
    }
}
function Step([string]$Label, [string]$Message) { Write-Host ('  | {0,-12} {1}' -f $Label,$Message) }
function Download([string]$Uri, [string]$OutFile) {
    for ($attempt=1; $attempt -le 3; $attempt++) {
        try { Invoke-WebRequest -UseBasicParsing -Uri $Uri -OutFile $OutFile -TimeoutSec 120; return }
        catch {
            if ($attempt -eq 3) { throw "Download failed after 3 attempts: $Uri. Check your connection/proxy and rerun; existing installation is preserved." }
            Step 'retry' "Download interrupted; retrying ($attempt/3)..."
            Start-Sleep -Seconds (2 * $attempt)
        }
    }
}
function Find-Program([string]$Name) { return @(Get-Command $Name -CommandType Application,ExternalScript -ErrorAction SilentlyContinue)[0] }
function Refresh-Path {
    $env:Path = (@($env:Path, [Environment]::GetEnvironmentVariable('Path','User'), [Environment]::GetEnvironmentVariable('Path','Machine')) | Where-Object { $_ }) -join ';'
}
function Safe-Path([string]$Path) {
    if ($Path -notmatch '^(?:[A-Za-z]:[\\/]|\\\\[^\\]+\\[^\\]+)' -or $Path -match "[`r`n`t]") { throw "Absolute, single-line path required: $Path" }
    $cursor = [IO.Path]::GetFullPath($Path)
    while ($cursor) {
        $item = Get-Item -LiteralPath $cursor -Force -ErrorAction SilentlyContinue
        if ($item -and ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw "Refusing linked install path: $cursor" }
        $parent = Split-Path -Parent $cursor
        if ($parent -eq $cursor) { break }
        $cursor = $parent
    }
}
function Hash([string]$Path) {
    $stream=[IO.File]::OpenRead($Path); $sha=[Security.Cryptography.SHA256]::Create()
    try { return ([BitConverter]::ToString($sha.ComputeHash($stream))).Replace('-','').ToLowerInvariant() }
    finally { $stream.Dispose(); $sha.Dispose() }
}
function Run-Checked([string]$Program, [string[]]$Arguments) {
    $output = & $Program @Arguments 2>&1
    if ($LASTEXITCODE -ne 0) { throw "$Program failed ($LASTEXITCODE): $($output -join [Environment]::NewLine)" }
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
        'related' { }
        default { throw "Unknown manifest record: $($p[0])" }
    }
}
if ($settings.schema -ne '1' -or $settings.repository -notmatch '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$') { throw 'Invalid installer manifest.' }
if (-not $Version) { $Version = $settings.version }
# Only x64 Windows artifacts are published. Fail before any wizard/download.
$machineArch = [Environment]::GetEnvironmentVariable('PROCESSOR_ARCHITECTURE','Machine')
if (-not $machineArch) { $machineArch = $env:PROCESSOR_ARCHITECTURE }
if ($machineArch -ne 'AMD64') { throw "Windows $machineArch is not supported by this release. Windows x64, Linux and macOS builds are available." }
$arch = 'amd64'
foreach ($relative in @($skills | ForEach-Object { $_[2] }) + $overlays + @($hostRows.Values | ForEach-Object { $_[3]; $_[4] })) {
    if ($relative -and ([IO.Path]::IsPathRooted($relative) -or $relative -match '(^|[/\\])\.\.([/\\]|$)')) { throw 'Unsafe manifest path.' }
}
foreach ($skill in $skills) { if ($skill[1] -notmatch '^[a-z0-9-]+$') { throw 'Invalid skill directory in manifest.' } }
if (-not $InstallDir) { $InstallDir = Join-Path $HomeDir $settings.install_relative }
if (-not $StateDir) { $StateDir = Join-Path $HomeDir $settings.state_relative }
if(-not $Verify -and -not $Uninstall){
    Write-Host ''
    Accent '       ____'
    Accent '     ________    midden'
    Accent '   ____________  Recover the work worth keeping.'
    Write-Host "`n  Installer $Version"
    Section '[1/3] Prepare your installation'
    Step 'system' 'Windows x64'
    if(-not $NonInteractive -and $Setup -eq 'auto'){
        $mode=Choose 'How would you like to start?' @('Quick start (recommended) - use sensible defaults','Custom setup - scope, folders and PATH')
        $Setup=if($mode -eq 0){'quick'}else{'custom'}
    }
    if(-not $NonInteractive -and $Setup -eq 'custom'){
        $InstallDir=Ask '  Binary installation folder (absolute path)' $InstallDir
        Safe-Path $InstallDir
        $existing=Join-Path $InstallDir 'install-receipt.json'
        if(Test-Path -LiteralPath $existing){Step 'existing' 'Keeping receipt scope/state for this installation. Choose a new binary folder to create a separate installation.'}
        else{
            $Scope=if((Choose 'Where should your CLI discover Midden?' @('Personal - available across projects','Project - only in one project') $(if($Scope -eq 'project'){1}else{0})) -eq 0){'user'}else{'project'}
            $PSBoundParameters['Scope']=$Scope
            if($Scope -eq 'project'){
                while($true){
                    $ProjectDir=Ask '  Project folder (absolute path)' $(if($ProjectDir){$ProjectDir}else{(Get-Location).Path})
                    try{Safe-Path $ProjectDir;if(Test-Path -LiteralPath $ProjectDir -PathType Container){break}}catch{}
                    Write-Host '  Choose an existing absolute project folder.'
                }
            }
            $StateDir=Ask '  Recovery data folder (absolute path)' $StateDir
            $PSBoundParameters['StateDir']=$StateDir
        }
        $NoPath=((Choose 'Make the midden command available on PATH?' @('Yes - add to user PATH','No - use the installed absolute path') $(if($NoPath -or $HomeDir -ne $HOME){1}else{0})) -eq 1)
    }
}
Safe-Path $HomeDir; Safe-Path $InstallDir; Safe-Path $StateDir
$InstallDir = [IO.Path]::GetFullPath($InstallDir).TrimEnd('\','/')
$StateDir = [IO.Path]::GetFullPath($StateDir).TrimEnd('\','/')
$receiptPath = Join-Path $InstallDir 'install-receipt.json'
Safe-Path $receiptPath
$receipt = if (Test-Path -LiteralPath $receiptPath) { Get-Content -LiteralPath $receiptPath -Raw | ConvertFrom-Json } else { $null }
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
    if ($Verify) { Step 'verified' "Midden $($receipt.version): executable and installed skills match the receipt."; return }
    $receipt.files | ForEach-Object { Write-Host "Remove owned file: $($_.path)" }
    Write-Host "Preserve recovery data: $($receipt.state)"
    if ($DryRun) { return }
    if (-not $NonInteractive -and -not (Confirm 'Remove these installed files?')) { return }
    foreach ($file in $receipt.files) { Remove-Item -LiteralPath $file.path }
    if ($receipt.path_added) {
        $old = [string][Environment]::GetEnvironmentVariable('Path','User')
        [Environment]::SetEnvironmentVariable('Path', (($old -split ';' | Where-Object { $_.TrimEnd('\') -ine $InstallDir }) -join ';'),'User')
    }
    Remove-Item -LiteralPath $receiptPath
    Write-Host 'Uninstalled. Recovery data and upgrade backups preserved.'
    return
}
if ($receipt) {
    Check-Receipt $receipt
    if (-not $PSBoundParameters.ContainsKey('Scope')) { $Scope = $receipt.scope }
    if (-not $PSBoundParameters.ContainsKey('ProjectDir')) { $ProjectDir = $receipt.project }
    if (-not $PSBoundParameters.ContainsKey('StateDir') -and -not $env:MIDDEN_STATE_DIR) { $StateDir = $receipt.state }
    if ($receipt.scope -ne $Scope -or $receipt.project -ne $ProjectDir -or $receipt.state -ne $StateDir) { throw 'Use a separate installation directory for a different scope or state location.' }
    $Hosts += $receipt.hosts
    Step 'existing' "$($receipt.version) found; owned files will be updated with backups."
    if (-not $NonInteractive) { $Upgrade = $true }
}
if (-not $Hosts.Count) {
    $available = @($hostRows.Keys | Sort-Object | Where-Object { Find-Program $hostRows[$_][2] })
    if (-not $available.Count) {
        foreach ($id in ($hostRows.Keys | Sort-Object)) { Step $hostRows[$id][5] $hostRows[$id][6] }
        throw 'No supported CLI found. Install one from the links above, sign in, then rerun this command.'
    }
    if ($available.Count -eq 1 -or $NonInteractive) { $Hosts = $available }
    else {
        $options=@('All detected CLIs')+@($available | ForEach-Object { $hostRows[$_][5] })
        $selected=Choose 'Which CLI should use Midden?' $options
        $Hosts=if($selected -eq 0){$available}else{@($available[$selected-1])}
    }
}
Safe-Path $StateDir
if ($InstallDir -eq $StateDir) { throw 'Binary and state directories must differ.' }
$Hosts = @($Hosts | ForEach-Object { $_.Split(',') } | ForEach-Object { $_.Trim() } | Where-Object { $_ } | Sort-Object -Unique)
if (-not $Hosts.Count) { throw '-Hosts is required with -NonInteractive.' }
if ($HostPath -and $Hosts.Count -ne 1) { throw '-HostPath requires one selected host.' }
foreach ($id in $Hosts) {
    $null = Skill-Root $id $Scope $ProjectDir
    $program = if ($HostPath) { Safe-Path $HostPath; $HostPath } else { $hostRows[$id][2] }
    if (-not (Get-Command $program -ErrorAction SilentlyContinue)) { throw "Install/authenticate $id first, or supply -HostPath." }
    Run-Checked $program @('--version')
    Step 'host' "$($hostRows[$id][5]) ready"
}
if ($Scope -eq 'project' -and -not (Test-Path -LiteralPath $ProjectDir -PathType Container)) { throw 'Project directory must exist.' }

$toolPaths = @{pandoc=$PandocPath; d2=$D2Path}
if (-not $NonInteractive -and -not $Dependencies.Count) {
    Write-Host "`n  Optional capabilities (core recovery needs neither):"
    $i=0
    foreach ($id in @('pandoc','d2')) {
        $i++; $found=Find-Program $id
        $detail=if($found){'found; no download'}else{'download/disk size unavailable; winget selects version'}
        Write-Host "  $i) $($deps[$id][3]) ($id; $detail)"
    }
    switch (Choose 'What would you like to create?' @('Core recovery only - add renderers later','PowerPoint and HTML - Pandoc','SVG diagrams - D2','Both rendering capabilities')) {
        0 { $Dependencies=@() }
        1 { $Dependencies=@('pandoc') }
        2 { $Dependencies=@('d2') }
        3 { $Dependencies=@('pandoc','d2') }
    }
}
$Dependencies = @($Dependencies | ForEach-Object { $_.Split(',') } | ForEach-Object { $_.Trim() } | Where-Object { $_ } | Sort-Object -Unique)
if ($Dependencies -contains 'none') { $Dependencies=@() }
foreach ($id in $Dependencies) { if (-not $deps.ContainsKey($id)) { throw "Unknown dependency: $id" } }
foreach ($id in $Dependencies) {
    $cmd=Find-Program $(if($toolPaths[$id]){$toolPaths[$id]}else{$id})
    if ($cmd) { $toolPaths[$id]=$cmd.Source; Step $id 'Reuse existing tool; functional check follows confirmation.' }
    elseif ($toolPaths[$id]) { throw "Missing $id executable: $($toolPaths[$id])" }
    elseif (-not (Find-Program 'winget')) { throw "To add $id, install it separately and use -$($id)Path, or rerun with core recovery only. winget is unavailable." }
    else { Step $id 'Install with winget; manager confirms version, size and any elevation.' }
}
if (-not $NonInteractive -and $HomeDir -eq $HOME -and -not $NoPath) { $AddPath=$true }
if ($NoPath) { $AddPath=$false }
if ($HomeDir -ne $HOME -and $AddPath) { throw 'Isolated home cannot modify the real Windows user PATH.' }
Section 'Install plan'
Step 'release' $Version
Step 'scope' $Scope
Step 'binary' $InstallDir
Step 'state' $StateDir
Step 'PATH' $(if($AddPath){'Add command to user PATH'}else{'Leave PATH unchanged'})
foreach ($id in $Hosts) { Write-Host "Skills: $(Skill-Root $id $Scope $ProjectDir)" }
Write-Host 'No host model configuration or permission grants will be changed.'
if ($DryRun) { Write-Host 'Preview only. No download, package installation or files changed.'; return }
if (-not $NonInteractive -and -not (Confirm 'Install with these settings?')) { Write-Host 'Cancelled. Nothing installed.'; return }
Section '[2/3] Install Midden'

$stage = Join-Path ([IO.Path]::GetTempPath()) ('midden-installer-' + [guid]::NewGuid())
New-Item -ItemType Directory -Path $stage | Out-Null
$lock=$null; $undo=@(); $committed=$false; $pathChanged=$false; $pathBefore=''
try {
    if (-not $BundleDir) {
        if ($Version -eq 'latest') { $Version=(Invoke-RestMethod "https://api.github.com/repos/$($settings.repository)/releases/latest").tag_name }
        if ($Version -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$') { throw 'Invalid release tag; supply -Version for a public prerelease.' }
        $asset=$settings.asset.Replace('{version}',$Version.Substring(1)).Replace('{os}','windows').Replace('{arch}',$arch)+'.zip'
        $base="https://github.com/$($settings.repository)/releases/download/$Version"
        Step 'download' "Midden $Version for Windows x64..."
        Download "$base/$asset" (Join-Path $stage $asset)
        Download "$base/SHA256SUMS" (Join-Path $stage 'SHA256SUMS')
        $sums=@([IO.File]::ReadAllLines((Join-Path $stage 'SHA256SUMS')) | Where-Object {$_ -match ('^[a-fA-F0-9]{64}\s+\*?'+[regex]::Escape($asset)+'$')})
        if ($sums.Count -ne 1 -or (Hash (Join-Path $stage $asset)) -ne ($sums[0] -split '\s+')[0]) { throw 'Checksum verification failed.' }
        Step 'verified' 'Archive checksum matches.'
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
            $content+="Optional rendering uses pandoc/d2 on PATH. If unavailable, keep editable source and report the missing renderer.`n"
            if ($PandocPath) { $content+="Explicit Pandoc: ``$PandocPath``; include its directory on PATH for rendering.`n" }
            if ($D2Path) { $content+="Explicit D2: ``$D2Path``; include its directory on PATH for rendering.`n" }
            $temp=Join-Path $stage "$id-$($skill[1]).md";[IO.File]::WriteAllText($temp,$content)
            $files+=@{path=(Join-Path (Skill-Root $id $Scope $ProjectDir) "$($skill[1])/SKILL.md");source=$temp}
        }
    }
    $known=@{};if($receipt){foreach($file in $receipt.files){$known[$file.path]=$file.sha256}}
    foreach($file in $files){Safe-Path $file.path;Safe-Path $file.source;if(Test-Path -LiteralPath $file.path){if(-not $known.ContainsKey($file.path)){throw "Preserving unowned file: $($file.path)"};if((Hash $file.path) -ne (Hash $file.source) -and -not $Upgrade){throw 'Installation differs; use -Upgrade to replace owned files with backups.'}}}
    # Dependencies are installed only after file-conflict preflight.
    foreach($id in $Dependencies){
        Step 'tool' "Setting up $id..."
        if(-not $toolPaths[$id]){
            & winget install --exact --id $deps[$id][6] --source winget
            if ($LASTEXITCODE -ne 0) { throw "winget could not install $id. Resolve its error above, or rerun with core recovery only." }
            Refresh-Path
            $cmd=Find-Program $id
            if (-not $cmd -and $id -eq 'pandoc') {
                foreach ($candidate in @("$env:LOCALAPPDATA\Pandoc\pandoc.exe","$env:ProgramFiles\Pandoc\pandoc.exe")) {
                    if (Test-Path -LiteralPath $candidate) { $cmd=Find-Program $candidate; break }
                }
            }
            if(-not $cmd){throw "$id was installed but its executable was not found. Use -$($id)Path with its installed location; Midden files have not been changed."}
            $toolPaths[$id]=$cmd.Source
        }
        $checkFile=Join-Path $stage $(if($id -eq 'pandoc'){'check.pptx'}else{'check.svg'})
        if($id -eq 'pandoc'){'# Check' | & $toolPaths[$id] -f markdown -t pptx -o $checkFile 2>&1 | Out-Null}else{[IO.File]::WriteAllText((Join-Path $stage 'check.d2'),'source -> evidence');Run-Checked $toolPaths[$id] @((Join-Path $stage 'check.d2'),$checkFile)}
        if($LASTEXITCODE -ne 0){throw "$id functional check failed"}
        if (-not (Test-Path -LiteralPath $checkFile) -or (Get-Item -LiteralPath $checkFile).Length -eq 0) { throw "$id produced no test artifact." }
    }
    Step 'install' 'Writing executable and CLI skills...'
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
    Section '[3/3] Finalize setup'
    $record=@{schema=1;version=$Version;scope=$Scope;project=$ProjectDir;state=$StateDir;hosts=$Hosts;path_added=[bool]($receipt -and $receipt.path_added);files=@($files | ForEach-Object {@{path=$_.path;sha256=(Hash $_.path)}})}
    if($AddPath){$old=[string][Environment]::GetEnvironmentVariable('Path','User');if($InstallDir -notin ($old -split ';')){$pathBefore=$old;[Environment]::SetEnvironmentVariable('Path',($old.TrimEnd(';')+';'+$InstallDir).TrimStart(';'),'User');$pathChanged=$true;$record.path_added=$true}}
    Check-Receipt $record
    $receiptTemp="$receiptPath.new-$([guid]::NewGuid())"
    try {[IO.File]::WriteAllText($receiptTemp,($record | ConvertTo-Json -Depth 5)+"`n");Move-Item -LiteralPath $receiptTemp -Destination $receiptPath -Force}
    finally {if(Test-Path -LiteralPath $receiptTemp){Remove-Item -LiteralPath $receiptTemp}}
    $committed=$true
    Section "Midden $Version installed successfully"
    Step 'ready' 'Midden installed. Executable and skill checksums verified.'
    $shadow=Find-Program 'midden'
    if($shadow -and $shadow.Source -ne $binary){Step 'notice' "Another midden is on PATH: $($shadow.Source). This install: $binary"}
    Write-Host "`n  Open a new terminal in your project and launch:"
    foreach($id in $Hosts){Write-Host "    $($hostRows[$id][2])   ($($hostRows[$id][5]))"}
    Write-Host "`n  Then ask: Use midden-editorial-production to investigate this project's sessions.`n  Compare worthwhile stories, audiences, evidence, and gaps before drafting."
    Write-Host "`n  Manage this install with the same installer: -Verify, -Upgrade, -Uninstall."
} finally {
    if(-not $committed -and $pathChanged){[Environment]::SetEnvironmentVariable('Path',$pathBefore,'User')}
    if(-not $committed){for($i=$undo.Count-1;$i -ge 0;$i--){$u=$undo[$i];if($u.backup){Copy-Item -LiteralPath $u.backup -Destination $u.path -Force}else{Remove-Item -LiteralPath $u.path -ErrorAction SilentlyContinue}}}
    if($lock){$lock.Dispose();Remove-Item -LiteralPath $lockPath}
    Remove-Item -LiteralPath $stage -Recurse -Force
}

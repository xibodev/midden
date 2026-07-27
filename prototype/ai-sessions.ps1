<#
.SYNOPSIS
    Unified session map for Copilot CLI, Claude Code and opencode.

.DESCRIPTION
    Reads the local session stores for all three AI CLIs, merges them into one
    recency-sorted table, and prints a ready-to-paste "cd + resume" command for
    each. Surfaces long-running sessions (created days ago, still being touched).

.EXAMPLE
    ai-sessions.ps1                    # everything touched in the last 5 days
    ai-sessions.ps1 -Days 10           # widen the window
    ai-sessions.ps1 -Tool claude       # one tool only
    ai-sessions.ps1 -LongRunning       # only sessions spanning 2+ days
    ai-sessions.ps1 -Resume 3          # cd + resume by index
    ai-sessions.ps1 -Json              # machine-readable
#>
[CmdletBinding()]
param(
    [ValidateSet('all', 'copilot', 'claude', 'opencode')]
    [string]$Tool = 'all',

    [int]$Days = 5,

    [int]$Limit = 200,

    [int]$Resume = 0,

    [switch]$LongRunning,

    [switch]$GroupByTool,

    [switch]$All,

    [switch]$Json
)

$ErrorActionPreference = 'Stop'
$inv = [cultureinfo]::InvariantCulture

# Automated spawns / health probes / scratch dirs - not real interactive work.
$noiseTitle = @(
    '^<GARRISON_GUIDE>'
    '^Reply briefly after using the shell tool'
    '^\s*(hi|hey|hello|test|ping)\s*$'
)
$noiseDir = @(
    '\\garrison-work\\.*\\assistant\\copilot\\'
    '\\\.copilot\\session-state\\'
)

function Test-IsNoise {
    param($Session)
    foreach ($p in $noiseTitle) { if ($Session.Title -match $p) { return $true } }
    foreach ($p in $noiseDir)   { if ($Session.Dir   -match $p) { return $true } }
    return $false
}

function ConvertTo-LocalTime {
    param([string]$Value)
    if ([string]::IsNullOrWhiteSpace($Value)) { return $null }
    $styles = [System.Globalization.DateTimeStyles]::AdjustToUniversal -bor
              [System.Globalization.DateTimeStyles]::AssumeUniversal
    [datetime]$parsed = [datetime]::MinValue
    if ([datetime]::TryParse($Value, [cultureinfo]::InvariantCulture, $styles, [ref]$parsed)) {
        return $parsed.ToLocalTime()
    }
    return $null
}

function Get-CopilotSessions {
    param([int]$WindowDays, [int]$Max)

    $db = Join-Path $env:USERPROFILE '.copilot\session-store.db'
    if (-not (Test-Path $db)) { return @() }
    if (-not (Get-Command python -ErrorAction SilentlyContinue)) {
        Write-Warning 'python not found on PATH - skipping Copilot sessions.'
        return @()
    }

    # Title falls back to the first user turn when the summary is blank, and
    # sessions with zero turns are dropped (empty shells, nothing to resume).
    $py = @"
import json, sqlite3, sys
con = sqlite3.connect('file:' + r'$db'.replace('\\', '/') + '?mode=ro', uri=True)
rows = con.execute('''
    SELECT s.id, s.cwd,
           COALESCE(NULLIF(TRIM(s.summary), ''),
                    (SELECT SUBSTR(t.user_message, 1, 160) FROM turns t
                     WHERE t.session_id = s.id ORDER BY t.turn_index LIMIT 1),
                    '(untitled)') AS title,
           s.created_at, s.updated_at, s.repository,
           (SELECT COUNT(*) FROM turns t2 WHERE t2.session_id = s.id) AS turns
    FROM sessions s
    WHERE SUBSTR(s.updated_at, 1, 10) >= date('now', '-$WindowDays days')
      AND s.cwd IS NOT NULL AND TRIM(s.cwd) != ''
    ORDER BY s.updated_at DESC
    LIMIT $Max
''').fetchall()
out = [{'id': r[0], 'cwd': r[1], 'title': r[2], 'created': r[3],
        'updated': r[4], 'repo': r[5], 'turns': r[6]} for r in rows if r[6] > 0]
json.dump(out, sys.stdout)
"@

    $raw = $py | & python - 2>$null
    if (-not $raw) { return @() }

    foreach ($r in (($raw -join '') | ConvertFrom-Json)) {
        [pscustomobject]@{
            Tool    = 'copilot'
            Id      = $r.id
            Dir     = $r.cwd
            Title   = $r.title
            Created = ConvertTo-LocalTime $r.created
            Updated = ConvertTo-LocalTime $r.updated
            Size    = '{0} turns' -f $r.turns
            Command = 'copilot --resume {0}' -f $r.id
        }
    }
}

function Get-ClaudeLiveMap {
    $map = @{}
    $dir = Join-Path $env:USERPROFILE '.claude\sessions'
    if (-not (Test-Path $dir)) { return $map }
    foreach ($f in Get-ChildItem $dir -Filter *.json -File -ErrorAction SilentlyContinue) {
        try { $o = Get-Content $f.FullName -Raw | ConvertFrom-Json } catch { continue }
        if (-not $o.sessionId -or -not $o.pid) { continue }
        if (-not (Get-Process -Id $o.pid -ErrorAction SilentlyContinue)) { continue }
        $map[$o.sessionId] = [pscustomobject]@{
            Pid = $o.pid; Status = $o.status; Name = $o.name
        }
    }
    return $map
}

function Get-ClaudeSessions {
    param([int]$WindowDays, [int]$Max, [int]$MinBytes = 51200)

    $root = Join-Path $env:USERPROFILE '.claude\projects'
    if (-not (Test-Path $root)) { return @() }

    $cutoff = [datetime]::Today.AddDays(-$WindowDays)
    $live   = Get-ClaudeLiveMap

    $files = Get-ChildItem $root -Recurse -Filter *.jsonl -ErrorAction SilentlyContinue |
        Where-Object {
            $_.Directory.Name -ne 'subagents' -and
            $_.BaseName -notlike 'agent-*' -and
            $_.BaseName -ne 'journal' -and
            $_.Length -gt $MinBytes -and
            $_.LastWriteTime -ge $cutoff
        } |
        Sort-Object LastWriteTime -Descending |
        Select-Object -First $Max

    foreach ($f in $files) {
        $cwd = $null; $title = $null; $summary = $null

        foreach ($line in (Get-Content $f.FullName -TotalCount 60 -ErrorAction SilentlyContinue)) {
            try { $o = $line | ConvertFrom-Json -ErrorAction Stop } catch { continue }
            if (-not $cwd -and $o.cwd) { $cwd = $o.cwd }
            if (-not $summary -and $o.type -eq 'summary' -and $o.summary) { $summary = $o.summary }
            if (-not $title -and $o.type -eq 'user') {
                if ($o.message.content -is [string]) {
                    $title = $o.message.content
                } else {
                    $t = ($o.message.content | Where-Object { $_.type -eq 'text' } | Select-Object -First 1).text
                    if ($t) { $title = $t }
                }
            }
            if ($cwd -and $summary) { break }
        }

        if ($summary) { $title = $summary }
        if (-not $cwd) { continue }
        if ($title -match '^<(local-command|command-name|user-memory)') { $title = '(slash command session)' }
        if ($title) { $title = ($title -replace '\s+', ' ').Trim() } else { $title = '(untitled)' }

        [pscustomobject]@{
            Tool    = 'claude'
            Id      = $f.BaseName
            Dir     = $cwd
            Title   = $title
            Created = $f.CreationTime
            Updated = $f.LastWriteTime
            Size    = [string]::Format($inv, '{0:0.0} MB', $f.Length / 1MB)
            Live    = $live[$f.BaseName]
            Command = 'claude --resume {0}' -f $f.BaseName
        }
    }
}

function Get-OpencodeSessions {
    param([int]$WindowDays, [int]$Max)

    # NOTE: `opencode session list` only returns sessions for the *current*
    # project, so it silently hides every other project's work. Read the DB
    # directly instead. parent_id IS NULL drops sub-agent/child sessions.
    $db = Join-Path $env:USERPROFILE '.local\share\opencode\opencode.db'
    if (-not (Test-Path $db)) { return @() }
    if (-not (Get-Command python -ErrorAction SilentlyContinue)) {
        Write-Warning 'python not found on PATH - skipping opencode sessions.'
        return @()
    }

    $cutoffMs = [DateTimeOffset]::new([datetime]::Today.AddDays(-$WindowDays)).ToUnixTimeMilliseconds()

    $py = @"
import json, sqlite3, sys
con = sqlite3.connect('file:' + r'$db'.replace('\\', '/') + '?mode=ro', uri=True)
rows = con.execute('''
    SELECT s.id, s.directory, COALESCE(NULLIF(TRIM(s.title), ''), '(untitled)'),
           s.time_created, s.time_updated, s.time_archived,
           (SELECT COUNT(*) FROM message m WHERE m.session_id = s.id)
    FROM session s
    WHERE s.parent_id IS NULL
      AND s.time_updated >= $cutoffMs
      AND s.directory IS NOT NULL AND TRIM(s.directory) != ''
    ORDER BY s.time_updated DESC
    LIMIT $Max
''').fetchall()
json.dump([{'id': r[0], 'dir': r[1], 'title': r[2], 'created': r[3],
            'updated': r[4], 'archived': r[5], 'msgs': r[6]} for r in rows], sys.stdout)
"@

    $raw = $py | & python - 2>$null
    if (-not $raw) { return @() }

    foreach ($r in (($raw -join '') | ConvertFrom-Json)) {
        $title = $r.title
        if ($r.archived) { $title = '[archived] ' + $title }
        [pscustomobject]@{
            Tool    = 'opencode'
            Id      = $r.id
            Dir     = ($r.dir -replace '/', '\')
            Title   = $title
            Created = ([DateTimeOffset]::FromUnixTimeMilliseconds($r.created)).LocalDateTime
            Updated = ([DateTimeOffset]::FromUnixTimeMilliseconds($r.updated)).LocalDateTime
            Size    = '{0} msgs' -f $r.msgs
            Command = 'opencode --session {0}' -f $r.id
        }
    }
}

# --- collect -----------------------------------------------------------------

$sessions = @()
if ($Tool -in 'all', 'copilot')  { $sessions += Get-CopilotSessions  -WindowDays $Days -Max $Limit }
if ($Tool -in 'all', 'claude')   { $sessions += Get-ClaudeSessions   -WindowDays $Days -Max $Limit -MinBytes $(if ($All) { 2kb } else { 50kb }) }
if ($Tool -in 'all', 'opencode') { $sessions += Get-OpencodeSessions -WindowDays $Days -Max $Limit }

$sessions = $sessions | Where-Object { $_.Updated }

$rawCount = $sessions.Count
if (-not $All) { $sessions = $sessions | Where-Object { -not (Test-IsNoise $_) } }
$filtered = $rawCount - $sessions.Count

# Span = how long the session has been alive. Long-running == idle-but-open.
$sessions = $sessions | ForEach-Object {
    $span = if ($_.Created) { $_.Updated - $_.Created } else { [timespan]::Zero }
    $_ | Add-Member -NotePropertyName SpanDays -NotePropertyValue ([math]::Round($span.TotalDays, 1)) -PassThru -Force
}

if ($LongRunning) { $sessions = $sessions | Where-Object { $_.SpanDays -ge 2 } }

$sessions = if ($GroupByTool) {
    $sessions | Sort-Object Tool, Updated -Descending
} else {
    $sessions | Sort-Object Updated -Descending
}

$i = 0
$sessions = $sessions | ForEach-Object {
    $i++
    $_ | Add-Member -NotePropertyName Index -NotePropertyValue $i -PassThru -Force
}

if (-not $sessions) {
    Write-Host "No sessions updated in the last $Days days." -ForegroundColor Yellow
    return
}

# --- resume ------------------------------------------------------------------

if ($Resume -gt 0) {
    $target = $sessions | Where-Object Index -EQ $Resume
    if (-not $target) {
        Write-Host "No session at index $Resume (have 1..$($sessions.Count))." -ForegroundColor Red
        return
    }
    if (-not (Test-Path $target.Dir)) {
        Write-Host "Directory no longer exists: $($target.Dir)" -ForegroundColor Red
        return
    }
    Write-Host "-> $($target.Dir)" -ForegroundColor DarkGray
    Write-Host "-> $($target.Command)" -ForegroundColor DarkGray
    Set-Location -LiteralPath $target.Dir
    Invoke-Expression $target.Command
    return
}

if ($Json) { $sessions | ConvertTo-Json -Depth 4; return }

# --- render ------------------------------------------------------------------

function Format-Age {
    param([timespan]$Span)
    if ($Span.TotalMinutes -lt 60) { return '{0}m ago' -f [int]$Span.TotalMinutes }
    if ($Span.TotalHours   -lt 24) { return '{0}h ago' -f [int]$Span.TotalHours }
    return '{0}d ago' -f [int]$Span.TotalDays
}

$colour   = @{ copilot = 'Cyan'; claude = 'Magenta'; opencode = 'Green' }
$lastTool = $null

Write-Host ''
Write-Host "  AI SESSION MAP  -  touched in the last $Days day(s)" -ForegroundColor White
Write-Host ('  ' + ('=' * 52)) -ForegroundColor DarkGray

$counts = $sessions | Group-Object Tool | ForEach-Object { '{0}: {1}' -f $_.Name, $_.Count }
$noiseNote = if ($filtered -gt 0) { '   ({0} automated/trivial hidden - use -All)' -f $filtered } else { '' }
Write-Host ('  {0}   |   {1} total{2}' -f ($counts -join '   '), $sessions.Count, $noiseNote) -ForegroundColor DarkGray
Write-Host ''

foreach ($s in $sessions) {
    if ($GroupByTool -and $s.Tool -ne $lastTool) {
        Write-Host ('  --- {0} ---' -f $s.Tool.ToUpper()) -ForegroundColor $colour[$s.Tool]
        $lastTool = $s.Tool
    }

    $ageS  = Format-Age ([datetime]::Now - $s.Updated)
    $title = $s.Title
    if ($title.Length -gt 66) { $title = $title.Substring(0, 63) + '...' }

    $flags = ''
    if ($s.PSObject.Properties['Live'] -and $s.Live) {
        $flags += '  [LIVE pid {0} - {1}]' -f $s.Live.Pid, $s.Live.Status
    }
    if ($s.SpanDays -ge 2)        { $flags += '  [long-running: {0}d span]' -f [int][math]::Round($s.SpanDays) }
    if (-not (Test-Path $s.Dir))  { $flags += '  [dir missing]' }

    Write-Host ('  [{0}] ' -f $s.Index) -NoNewline -ForegroundColor White
    Write-Host ('{0,-8}' -f $s.Tool) -NoNewline -ForegroundColor $colour[$s.Tool]
    Write-Host ("  {0,-8} {1,-10}  {2}" -f $ageS, $s.Size, $title) -NoNewline -ForegroundColor Gray
    Write-Host $flags -ForegroundColor DarkYellow
    Write-Host ('        {0}' -f $s.Dir) -ForegroundColor DarkGray
    Write-Host ("        Set-Location '{0}'; {1}" -f $s.Dir, $s.Command) -ForegroundColor DarkCyan
    Write-Host ''
}

Write-Host '  -Resume <n>   jump in    |  -Days <n>  widen window  |  -All  show hidden' -ForegroundColor DarkGray
Write-Host '  -LongRunning  idle-open  |  -Tool <t>  filter        |  -GroupByTool' -ForegroundColor DarkGray
Write-Host ''

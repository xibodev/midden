# Midden one-line entry point: irm https://xibodev.github.io/midden/install.ps1 | iex
# The versioned release scripts own installation; this bootstrap only fetches them.
& {
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'
    Set-StrictMode -Off
    $PSModuleAutoLoadingPreference = 'All'
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    $version = 'v0.2.2'
    if ($env:MIDDEN_VERSION) { $version=$env:MIDDEN_VERSION }
    if ($version -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$') { throw 'MIDDEN_VERSION must be a release tag, such as v0.2.1.' }
    $base = "https://github.com/xibodev/midden/releases/download/$version"
    $shell = (Get-Process -Id $PID).Path
    if ($env:OS -ne 'Windows_NT') { throw 'Use https://xibodev.github.io/midden/install.sh on Linux/macOS.' }
    $temporary = Join-Path ([IO.Path]::GetTempPath()) ('midden-bootstrap-' + [Guid]::NewGuid().ToString('N'))
    try {
        New-Item -ItemType Directory -Path $temporary | Out-Null
        foreach ($file in @('SHA256SUMS', 'install.ps1', 'manifest.tsv')) {
            for ($attempt=1; $attempt -le 3; $attempt++) {
                try { Invoke-WebRequest -UseBasicParsing -Uri "$base/$file" -OutFile (Join-Path $temporary $file) -TimeoutSec 120; break }
                catch {
                    if ($attempt -eq 3) { throw "Could not download $file. Check your connection/proxy, then rerun. No Midden installation files changed." }
                    Write-Host "  retry        Download interrupted ($attempt/3)..."
                    Start-Sleep -Seconds (2 * $attempt)
                }
            }
        }
        $lines = [IO.File]::ReadAllLines((Join-Path $temporary 'SHA256SUMS'))
        foreach ($file in @('install.ps1', 'manifest.tsv')) {
            $entries = @($lines | Where-Object { ($_ -split '\s+')[1] -eq $file })
            if ($entries.Count -ne 1) { throw "Missing or duplicate checksum: $file" }
            $expected = ($entries[0] -split '\s+')[0]
            $stream=[IO.File]::OpenRead((Join-Path $temporary $file)); $sha=[Security.Cryptography.SHA256]::Create()
            try { $actual=([BitConverter]::ToString($sha.ComputeHash($stream))).Replace('-','').ToLowerInvariant() }
            finally { $stream.Dispose(); $sha.Dispose() }
            if ($expected -notmatch '^[a-f0-9]{64}$' -or $actual -ne $expected) { throw "Checksum mismatch: $file" }
        }
        Write-Host "Starting verified Midden $version installer..."
        & $shell -NoProfile -File (Join-Path $temporary 'install.ps1') -Version $version @args
        if ($LASTEXITCODE -ne 0) { throw "Midden installer failed (exit $LASTEXITCODE)." }
        # The child cannot update its parent's environment. Pick up its user PATH.
        $userPath=[Environment]::GetEnvironmentVariable('Path','User')
        if ($userPath) { $env:Path=($env:Path+';'+$userPath) }
    } finally {
        if (Test-Path -LiteralPath $temporary) { Remove-Item -LiteralPath $temporary -Recurse -Force }
    }
} @args

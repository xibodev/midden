# Midden one-line entry point: irm https://xibodev.github.io/midden/install.ps1 | iex
# The versioned release scripts own installation; this bootstrap only fetches them.
& {
    $ErrorActionPreference = 'Stop'
    $version = 'v0.2.0'
    $base = "https://github.com/xibodev/midden/releases/download/$version"
    $pwsh = @(Get-Command pwsh -CommandType Application -ErrorAction SilentlyContinue)[0]
    if (-not $pwsh) { throw 'Install PowerShell 7 first: https://aka.ms/powershell-release?tag=stable' }
    if ($env:OS -ne 'Windows_NT') { throw 'Use https://xibodev.github.io/midden/install.sh on Linux/macOS.' }
    $temporary = Join-Path ([IO.Path]::GetTempPath()) ('midden-bootstrap-' + [Guid]::NewGuid().ToString('N'))
    try {
        New-Item -ItemType Directory -Path $temporary | Out-Null
        foreach ($file in @('SHA256SUMS', 'install.ps1', 'manifest.tsv')) {
            Invoke-WebRequest -UseBasicParsing -Uri "$base/$file" -OutFile (Join-Path $temporary $file)
        }
        $lines = [IO.File]::ReadAllLines((Join-Path $temporary 'SHA256SUMS'))
        foreach ($file in @('install.ps1', 'manifest.tsv')) {
            $entries = @($lines | Where-Object { ($_ -split '\s+')[1] -eq $file })
            if ($entries.Count -ne 1) { throw "Missing or duplicate checksum: $file" }
            $expected = ($entries[0] -split '\s+')[0]
            $actual = (Get-FileHash -LiteralPath (Join-Path $temporary $file) -Algorithm SHA256).Hash.ToLowerInvariant()
            if ($expected -notmatch '^[a-f0-9]{64}$' -or $actual -ne $expected) { throw "Checksum mismatch: $file" }
        }
        Write-Host "Starting verified Midden $version installer..."
        & $pwsh.Source -NoProfile -File (Join-Path $temporary 'install.ps1') -Version $version @args
        if ($LASTEXITCODE -ne 0) { throw "Midden installer failed (exit $LASTEXITCODE)." }
    } finally {
        if (Test-Path -LiteralPath $temporary) { Remove-Item -LiteralPath $temporary -Recurse -Force }
    }
} @args

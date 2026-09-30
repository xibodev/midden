#requires -Version 5.1
# Python is an explicit prerequisite; this launcher installs no dependencies.
$ErrorActionPreference = 'Stop'
$python = $env:PYTHON
$versionProbe = 'import sys; sys.exit(0 if sys.version_info >= (3, 9) else 1)'
if ($python) {
    & $python -c $versionProbe
    if ($LASTEXITCODE -ne 0) { throw 'PYTHON must identify Python 3.9+; nothing was installed.' }
} else {
    foreach ($name in @('python', 'python3')) {
        foreach ($command in @(Get-Command $name -CommandType Application -All -ErrorAction SilentlyContinue)) {
            $candidate = [string]$command.Source
            & $candidate -c $versionProbe
            if ($LASTEXITCODE -eq 0) {
                $python = $candidate
                break
            }
        }
        if ($python) { break }
    }
    if (-not $python) { throw 'Python 3.9+ is required. Set PYTHON to an existing interpreter; nothing was installed.' }
}
$backend = Join-Path $PSScriptRoot 'installer\install.py'
if (-not (Test-Path -LiteralPath $backend -PathType Leaf)) {
    throw 'Keep the installer directory beside install.ps1.'
}
& $python -B $backend @args
exit $LASTEXITCODE

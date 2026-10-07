param(
    [string]$Data = '',
    [int]$Port = 18890,
    [switch]$NoOpen
)
$ErrorActionPreference = 'Stop'
$binary = Join-Path $PSScriptRoot 'midden-ui.exe'
if (-not (Test-Path -LiteralPath $binary -PathType Leaf)) {
    throw "Packaged App executable is missing: $binary"
}
$arguments = @('--listen', "127.0.0.1:$Port")
if ($Data) { $arguments += @('--data', $Data) }
if ($NoOpen) { $arguments += '--no-open' }
& $binary @arguments
if ($LASTEXITCODE -ne 0) {
    throw "Midden exited with code $LASTEXITCODE"
}

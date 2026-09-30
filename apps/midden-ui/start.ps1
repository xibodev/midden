param(
    [string]$Workspace = '',
    [string]$State = '',
    [int]$Port = 18890,
    [switch]$NoOpen
)
$ErrorActionPreference = 'Stop'
$binary = Join-Path $PSScriptRoot 'midden-ui.exe'
if (-not (Test-Path -LiteralPath $binary -PathType Leaf)) {
    throw "Packaged UI executable is missing: $binary"
}
$arguments = @('--listen', "127.0.0.1:$Port")
if ($Workspace) { $arguments += @('--workspace', $Workspace) }
if ($State) { $arguments += @('--state', $State) }
if ($NoOpen) { $arguments += '--no-open' }
& $binary @arguments
if ($LASTEXITCODE -ne 0) {
    throw "Midden UI exited with code $LASTEXITCODE"
}

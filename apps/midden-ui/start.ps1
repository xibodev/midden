param(
    [string]$Workspace = (Join-Path $PSScriptRoot 'workspace'),
    [string]$State = (Join-Path $PSScriptRoot 'host-state'),
    [int]$Port = 18890
)
$ErrorActionPreference = 'Stop'
$binary = Join-Path $PSScriptRoot 'midden-ui.exe'
$core = Join-Path $PSScriptRoot 'midden.exe'
$bundle = Join-Path $PSScriptRoot 'bundles'
foreach ($path in @($binary, $core, $bundle)) {
    if (-not (Test-Path -LiteralPath $path)) { throw "Packaged component is missing: $path" }
}
if (-not (Test-Path -LiteralPath $Workspace)) {
    New-Item -ItemType Directory -Path $Workspace | Out-Null
}
& $binary --workspace $Workspace --state $State --core $core --bundle $bundle --listen "127.0.0.1:$Port"
exit $LASTEXITCODE

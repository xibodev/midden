$ErrorActionPreference = 'Stop'

# Drives the midden MCP server over real stdio JSON-RPC and measures the
# actual token cost of every tool against its declared budget.

$exe = Join-Path $PSScriptRoot '..\midden.exe' | Resolve-Path

$requests = @(
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{}}}'
  '{"jsonrpc":"2.0","method":"notifications/initialized"}'
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
  '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"midden_health","arguments":{}}}'
  '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"midden_list_sessions","arguments":{"days":5}}}'
  '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"midden_list_sessions","arguments":{}}}'
  '{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"midden_search","arguments":{"query":"brlex"}}}'
  '{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"midden_session_brief","arguments":{"id":"ac0c39cf","turns":3}}}'
  '{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"midden_resume_command","arguments":{"id":"ac0c39cf"}}}'
  '{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"nope","arguments":{}}}'
  '{"jsonrpc":"2.0","id":10,"method":"bogus/method"}'
  'this is not json'
)

$inFile  = Join-Path $env:TEMP 'mcp-in.jsonl'
$outFile = Join-Path $env:TEMP 'mcp-out.jsonl'
$errFile = Join-Path $env:TEMP 'mcp-err.txt'
Set-Content -Path $inFile -Value $requests -Encoding utf8

$p = Start-Process -FilePath $exe -ArgumentList 'mcp' -NoNewWindow -PassThru `
      -RedirectStandardInput $inFile -RedirectStandardOutput $outFile `
      -RedirectStandardError $errFile
$p.WaitForExit(180000) | Out-Null

$names = @{
  1='initialize'; 2='tools/list'; 3='health'; 4='list(days=5)'; 5='list(all)'
  6='search(brlex)'; 7='brief(681MiB)'; 8='resume_cmd'; 9='unknown tool'; 10='bad method'
}
$budgets = @{ 3=400; 4=4000; 5=4000; 6=1500; 7=1500; 8=200 }

"{0,-16} {1,8} {2,8} {3,8}  {4}" -f 'TOOL','~TOKENS','BUDGET','STATUS','NOTE'
"-" * 72

foreach ($line in Get-Content $outFile) {
  if (-not $line.Trim()) { continue }
  $o = $line | ConvertFrom-Json
  $id = [int]$o.id
  $name = $names[$id]; if (-not $name) { $name = "id=$id" }

  if ($o.error) {
    "{0,-16} {1,8} {2,8} {3,8}  {4}" -f $name,'-','-','ERR',$o.error.message
    continue
  }

  $text = ''
  if ($o.result.content) { $text = ($o.result.content | ForEach-Object { $_.text }) -join "`n" }
  elseif ($o.result.tools) { $text = ($o.result.tools | ConvertTo-Json -Depth 6 -Compress) }
  else { $text = ($o.result | ConvertTo-Json -Depth 6 -Compress) }

  $tok = [math]::Ceiling($text.Length / 4)
  $b = $budgets[$id]
  $status = 'ok'
  if ($b) { $status = if ($tok -le $b) { 'WITHIN' } else { 'OVER' } }
  $note = if ($o.result.isError) { 'isError=true' } else { '' }

  "{0,-16} {1,8} {2,8} {3,8}  {4}" -f $name,$tok,$(if($b){$b}else{'-'}),$status,$note
}

Remove-Item $inFile,$outFile,$errFile -ErrorAction SilentlyContinue

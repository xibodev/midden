# Clean agentic staging test

Test the agent-first bundle, not Studio and not the public stable installer.
Use a fresh binary install, workspace, Midden state directory and Copilot
configuration directory. This is state isolation, not an operating-system sandbox.
Midden still reads the operator's normal session stores read-only.

## 1. Obtain the current successful staging bundle

Prerequisites: Windows PowerShell 5.1 or PowerShell 7, Git, authenticated GitHub CLI
(`gh`), and Copilot CLI. Go, Pandoc, D2, and Studio are not required for this first
Markdown journey. Open a **new PowerShell window** so environment overrides end
when the window closes.

```powershell
$ErrorActionPreference = 'Stop'
$repo = 'xibodev/midden'
$branch = 'feat/agentic-efficient-journey'
$sha = gh api "repos/$repo/git/ref/heads/$branch" --jq '.object.sha'
if ($LASTEXITCODE -ne 0) { throw 'Cannot read staging feature revision.' }
$run = gh run list --repo $repo --branch $branch --commit $sha `
    --status success --workflow 'Stage agentic bundle' --limit 1 `
    --json databaseId --jq '.[0].databaseId'
if ($LASTEXITCODE -ne 0 -or -not $run -or $run -eq 'null') {
    throw 'Wait for a successful staging build of the current feature revision.'
}
$artifact = gh api "repos/$repo/actions/runs/$run/artifacts" `
    --jq '.artifacts[] | select(.name | startswith("midden-agentic-staging-Windows-")) | .name'
if ($LASTEXITCODE -ne 0 -or -not $artifact) { throw 'Windows staging artifact is unavailable.' }
$lab = Join-Path $HOME ('midden-tests\' + [guid]::NewGuid().ToString('N'))
$bundle = Join-Path $lab 'bundle'
$workspace = Join-Path $lab 'workspace'
New-Item -ItemType Directory -Path $bundle, $workspace | Out-Null
git init --quiet $workspace
if ($LASTEXITCODE -ne 0) { throw 'Cannot establish an isolated project boundary.' }
gh run download $run --repo $repo --name $artifact --dir $bundle
if ($LASTEXITCODE -ne 0) { throw 'Staging download failed.' }

# Verify the downloaded files before executing the installer.
foreach ($line in Get-Content -LiteralPath (Join-Path $bundle 'SHA256SUMS')) {
    if ($line -notmatch '^([0-9a-f]{64})  (.+)$') { throw 'Malformed checksum entry.' }
    $expected = $Matches[1]
    $relative = $Matches[2].Replace('/', '\')
    $path = [IO.Path]::GetFullPath((Join-Path $bundle $relative))
    if (-not $path.StartsWith($bundle + '\', [StringComparison]::OrdinalIgnoreCase)) {
        throw 'Checksum entry escapes the downloaded bundle.'
    }
    if ((Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash -ine $expected) {
        throw "Checksum mismatch: $relative"
    }
}
```

This selects a build of the feature head, not an older successful run.
Checksums check bundle consistency; obtain both bundle and checksums from the
trusted repository's successful workflow, not an arbitrary download.

## 2. Install only inside the lab

```powershell
$bin = Join-Path $lab 'bin'
$state = Join-Path $lab 'state'
$installerHome = Join-Path $lab 'installer-home'
& (Join-Path $bundle 'install.ps1') -BundleDir $bundle `
    -Hosts copilot-cli -Scope project -ProjectDir $workspace `
    -InstallDir $bin -StateDir $state -HomeDir $installerHome `
    -Dependencies none -NoPath -NonInteractive

& (Join-Path $bundle 'install.ps1') -Verify `
    -InstallDir $bin -HomeDir $installerHome -NoPath -NonInteractive

$env:MIDDEN_HOME = $state
$env:COPILOT_HOME = Join-Path $lab 'copilot-profile'
$env:COPILOT_ALLOW_ALL = 'false'
$midden = Join-Path $bin 'midden.exe'

& $midden agent schema handoffs.create
& $midden agent content.types --home $state
& $midden agent projects.list --home $state
$mcpConfig = Join-Path $lab 'midden-mcp.json'
& $midden agent mcp-config --home $state | Set-Content -LiteralPath $mcpConfig -Encoding utf8
copilot -C $workspace --no-custom-instructions skill list
```

Pass when:

- the receipt verifies and the installed project skill names the **lab binary
  and lab state**, not the stable installation;
- handoff targets are exactly `markdown`, `quarto`, `pandoc`, and `d2`;
- `content.types` reports zero evidence and `projects.list` reports zero projects;
- the four Midden skills are discovered under this workspace's `.github\skills`
  directory, without unrelated inherited project skills. Copilot's built-ins
  can remain available.

`content.types` initializes the empty Midden index without model use.
The installer does not change the user PATH, personal skills, existing Midden
state, source stores, or renderer installations. `COPILOT_HOME` separates the new
host conversation/configuration; it does not change the default source locations
Midden reads. Alternate source-store locations require explicit module roots.

## 3. Start a fresh operator-controlled conversation

```powershell
copilot -C $workspace --mode interactive --no-custom-instructions `
    --additional-mcp-config "@$mcpConfig" --allow-all-mcp-server-instructions `
    --disable-builtin-mcps --no-remote --no-remote-export
```

Do not resume this implementation conversation or use automatic all-tool
approval. A fresh Copilot profile may request sign-in; use its normal login
flow rather than copying credentials or configuration from the old profile.
This isolated profile must contain only the trusted Midden MCP connection before
enabling server instructions. That flag loads the bundle's process contract; it
does not supply expected findings or tool choices in the operator's prompt.
Use `/skills` to confirm the project skill source if needed. The new Git
repository stops inheritance from parent project directories; a fresh Copilot
profile avoids the old personal Midden install. Check for additional globally
configured skill locations and select the project-installed entry explicitly.

For the first comparison, use one identified source view:

> Look through session SESSION_ID. Is there anything worth developing into
> content? Show me the strongest possibilities.

Replace `SESSION_ID` with the chosen exact ID. Do not supply tool names, expected
findings, sampling limits or the workflow. Keep the assessment criteria below
outside the prompt: the installed bundle must guide the agent.

The investigation pins the source prefix it observes. Appends should not break
historical reads; refresh explicitly for newer events. Start comparisons from the
same captured view and record when that is not possible. Host reasoning consumes
the host's normal model budget even though
Midden's prepare/compose operations do not invoke another model.

## 4. Walk the checkpoints separately

| Checkpoint | Expected behavior |
|---|---|
| Discover | Exact source identity, composition, bounded candidate count, and coverage caveats; no claim to have read the whole transcript. |
| Investigate | The agent orients across the full time range, retrieves focused context, and distinguishes early claims from later corrections. A sample endpoint never becomes a claimed session ending. |
| Select | After the operator chooses, a draft recipe links only the selected opportunity's evidence. Selection is not approval. |
| Evidence selection | The exact source set is saved. Choice of a story or automatic continuation does not create evidence approval. |
| Draft | An explicit development request produces a local unreviewed draft without a mandatory human-approval receipt. Material claims have source references and limitations. |
| Output review | Citation/quotation findings are addressed; the agent examines support rather than equating identifiers with truth. The operator confirms the exact draft. |
| Deliver | Local export preserves reviewed bytes and provenance. Optional handoffs are source bundles, not automatically rendered publications. |
| Resume | A new conversation, with the same lab environment, finds the stored project/revision and continues without re-mining or resetting progress. |

Before a costly end-to-end run, use `host.status` and its optional non-mutating
confirmation probe. Record the precise outcome rather than inferring that a
decline means the channel is unsupported. Positive human review must be tested
with an operator present; headless interactive agent mode is not a UI test.

For the cost comparison, record model, reasoning effort, source view, warm/cold
cache condition, input/cache/output tokens and credits per stage. Also record
response bytes, retries and formatting calls. Normal replies should fit 8 KiB,
all compact envelopes should fit 16 KiB, and mutation acknowledgements should not
repeat whole plans. Do not infer a cost saving merely from fewer tool calls.

A useful follow-up after choosing a story:

> Develop the third idea as an internal article.

Do not start with every session, several formats, a book, or renderers.
After this single-output journey works, test a second explicit source, chapter
dependencies, and optional renderers independently. The workflow MCP connection
provides operator confirmation, not a second AI runtime.

## 5. Record the result and reset safely

Record the feature SHA, workflow run, installed binary path, host version,
project/recipe/output IDs, and first checkpoint that failed. Keep private
evidence, screenshots and drafts local.

For a clean rerun, make a **new lab directory**; do not clear the source stores
or reuse the old index. Close the dedicated PowerShell window to discard its
environment overrides. Keep the previous lab for comparison until you no longer
need its drafts and evidence.

This operator journey is not a substitute for full repository, cross-host, or
renderer tests. Those remain separate acceptance work.

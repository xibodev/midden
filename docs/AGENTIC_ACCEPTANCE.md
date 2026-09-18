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
$branch = 'feat/editorial-production-handoffs'
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
    --disable-builtin-mcps --no-remote --no-remote-export
```

Do not resume this implementation conversation or use automatic all-tool
approval. A fresh Copilot profile may request sign-in; use its normal login
flow rather than copying credentials or configuration from the old profile.
Use `/skills` to confirm the project skill source if needed. The new Git
repository stops inheritance from parent project directories; a fresh Copilot
profile avoids the old personal Midden install. Check for additional globally
configured skill locations and select the project-installed entry explicitly.

For the first prompt, use one **closed** session:

> Use the project-installed midden-editorial-production skill and its bound lab
> binary/state. Investigate only the Copilot session
> e8d45bbc-8a5a-49f7-97fb-cdde78961650. Begin with a free assay and at most 40
> candidate excerpts. Do not widen the scope or read raw transcripts. Compare
> useful stories, audiences, decisions that changed, evidence gaps, and privacy
> risks. Stop for my choice before drafting. Do not invoke another AI CLI,
> approve anything on my behalf, install tools, upload, publish, or modify source
> sessions.

Use another exact ID if that source is still active. Source stores can change
when their owning CLI is running; test a paused/closed source for stable digest
checks. Host reasoning still consumes the host's normal model budget even though
Midden's prepare/compose operations do not invoke another model.

## 4. Walk the checkpoints separately

| Checkpoint | Expected behavior |
|---|---|
| Discover | Exact source identity, composition, bounded candidate count, and coverage caveats; no claim to have read the whole transcript. |
| Investigate | Evidence is cited and stored in the lab. Competing stories distinguish original decisions from later corrections and unresolved failures. |
| Select | After the operator chooses, a draft recipe links only the selected opportunity's evidence. Selection is not approval. |
| Evidence review | The agent shows the exact evidence and gaps. Only the operator's explicit approval permits the approval transition. |
| Draft | The host authors a Markdown draft and submits it. Material claims have evidence references; disputed or unverified claims are labeled. |
| Output review | The operator inspects the actual draft. Review uses the current content digest; a draft cannot be exported. |
| Deliver | Local export preserves reviewed bytes and provenance. Optional handoffs are source bundles, not automatically rendered publications. |
| Resume | A new conversation, with the same lab environment, finds the stored project/revision and continues without re-mining or resetting progress. |

A useful follow-up after choosing a story:

> Develop this as an internal Markdown article. First show the outline, claims,
> supporting evidence, unresolved gaps and the exact recipe evidence selection.
> Wait for my evidence approval before composing. Leave the output as a draft
> until I review it.

Do not start with every session, several formats, a book, renderers, or MCP.
After this single-output journey works, test a second explicit source, chapter
dependencies, and optional transports/renderers independently.

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

#!/usr/bin/env bash
# Script-owned agentic CLI installer. Bash 3.2+ (including macOS system Bash).
set -euo pipefail
version=latest bundle= manifest= hosts= host_path= scope=user project= install_dir= state_dir=
home_dir=${HOME:?HOME must be set} dependencies= pandoc_path= d2_path=
noninteractive=0 dry_run=0 upgrade=0 uninstall=0 verify=0 add_path=0
usage() {
  printf '%s\n' 'Usage: bash install.sh [--version vX.Y.Z | --bundle-dir /path] [options]' \
    '  --hosts copilot-cli,claude-code,opencode --host-path /path' \
    '  --scope user|project --project /path --install-dir /path --state-dir /path' \
    '  --dependencies pandoc,d2 --pandoc-path /path --d2-path /path' \
    '  --yes --dry-run --upgrade --verify --uninstall --add-path' \
    '  --manifest /path/manifest.tsv --home-dir /isolated/test/home'
}
while (($#)); do
  case "$1" in
    --version) version=${2:?}; shift 2;; --bundle-dir) bundle=${2:?}; shift 2;;
    --manifest) manifest=${2:?}; shift 2;; --hosts) hosts=${2:?}; shift 2;;
    --host-path) host_path=${2:?}; shift 2;; --scope) scope=${2:?}; shift 2;;
    --project) project=${2:?}; shift 2;; --install-dir) install_dir=${2:?}; shift 2;;
    --state-dir) state_dir=${2:?}; shift 2;; --home-dir) home_dir=${2:?}; shift 2;;
    --dependencies) dependencies=${2:?}; shift 2;; --pandoc-path) pandoc_path=${2:?}; shift 2;; --d2-path) d2_path=${2:?}; shift 2;;
    --yes|--non-interactive) noninteractive=1; shift;; --dry-run) dry_run=1; shift;;
    --upgrade) upgrade=1; shift;; --uninstall) uninstall=1; shift;; --verify) verify=1; shift;; --add-path) add_path=1; shift;;
    --help|-h) usage; exit 0;; *) printf 'Unknown option: %s\n' "$1" >&2; exit 2;;
  esac
done
fail() { printf '%s\n' "$*" >&2; exit 1; }
case "$(uname -s)" in Linux) os=linux;; Darwin) os=darwin;; *) fail 'Use install.ps1 on Windows.';; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64;; arm64|aarch64) arch=arm64;; *) fail 'Unsupported architecture';; esac
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
if [[ -z "$manifest" ]]; then manifest="$script_dir/installer/manifest.tsv"; [[ -f "$manifest" ]] || manifest="$script_dir/manifest.tsv"; fi
safe_path() {
  local p=$1
  [[ "$p" = /* && "$p" != *$'\n'* && "$p" != *$'\r'* && "$p" != *$'\t'* && "$p" != */../* && "$p" != */.. ]] || fail "Absolute, single-line path required: $p"
  while [[ "$p" != / && -n "$p" ]]; do [[ ! -L "$p" ]] || fail "Refusing linked path: $p"; p=$(dirname -- "$p"); done
}
safe_path "$manifest"; [[ -f "$manifest" ]] || fail 'Installer manifest not found; keep manifest.tsv beside the release script.'
field() { awk -F '\t' -v type="$1" -v key="$2" -v col="$3" '$1==type && $2==key {print $col}' "$manifest"; }
[[ $(field setting schema 3) = 1 ]] || fail 'Unsupported manifest schema'
repo=$(field setting repository 3)
[[ "$repo" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || fail 'Invalid repository in manifest'
install_dir=${install_dir:-"$home_dir/$(field setting install_relative 3)"}
state_dir=${state_dir:-"$home_dir/$(field setting state_relative 3)"}
ask() { local reply; printf '%s [%s]: ' "$1" "$2" >&2; IFS= read -r reply || fail 'Input ended; use --yes with explicit choices.'; printf '%s' "${reply:-$2}"; }
hash() {
  if command -v sha256sum >/dev/null; then sha256sum "$1" | awk '{print $1}';
  elif command -v shasum >/dev/null; then shasum -a 256 "$1" | awk '{print $1}';
  else fail 'sha256sum or shasum is required'; fi
}
skill_root() {
  local id=$1 chosen_scope=$2 chosen_project=$3 relative
  if [[ "$chosen_scope" = project ]]; then safe_path "$chosen_project"; relative=$(field host "$id" 5); printf '%s/%s' "$chosen_project" "$relative"
  else relative=$(field host "$id" 4); printf '%s/%s' "$home_dir" "$relative"; fi
  [[ -n "$relative" && "$relative" != /* && "$relative" != *..* ]] || fail "Unknown/invalid host: $id"
}
if (( !noninteractive && !verify && !uninstall )); then
  printf '%s\n' 'Midden — install for your agentic CLI'
  while IFS=$'\t' read -r type id program rest; do
    [[ "$type" = host ]] || continue
    printf '  %s: %s\n' "$id" "$(command -v "$program" || printf 'not detected')"
  done < "$manifest"
  hosts=$(ask 'Select CLI hosts (comma-separated)' "${hosts:-copilot-cli}")
  scope=$(ask 'Scope: user or project' "$scope")
  [[ "$scope" != project ]] || project=$(ask 'Absolute project directory' "$project")
  install_dir=$(ask 'Binary installation directory' "$install_dir")
  state_dir=$(ask 'Recovery state directory' "$state_dir")
fi
[[ "$scope" = user || "$scope" = project ]] || fail 'Scope must be user or project'
safe_path "$home_dir"; safe_path "$install_dir"; safe_path "$state_dir"
install_dir=${install_dir%/}; state_dir=${state_dir%/}
[[ "$install_dir" != "$state_dir" ]] || fail 'Binary and state directories must differ'
receipt="$install_dir/install-receipt.tsv"; safe_path "$receipt"
receipt_value() { awk -F '\t' -v key="$1" '$1==key {print $2}' "$receipt"; }
check_receipt() {
  [[ -f "$receipt" && $(receipt_value schema) = 1 ]] || fail 'No supported installation receipt'
  local record a b old_scope old_project old_hosts allowed id sk type name relative
  old_scope=$(receipt_value scope); old_project=$(receipt_value project); old_hosts=$(receipt_value hosts)
  while IFS=$'\t' read -r record a b; do
    [[ "$record" = file ]] || continue
    safe_path "$b"; allowed=0
    [[ "$b" != "$install_dir/midden" ]] || allowed=1
    for id in ${old_hosts//,/ }; do
      sk=$(skill_root "$id" "$old_scope" "$old_project")
      while IFS=$'\t' read -r type name relative; do
        [[ "$type" = skill ]] || continue
        [[ "$b" != "$sk/$name/SKILL.md" ]] || allowed=1
      done < "$manifest"
    done
    (( allowed )) || fail 'Receipt contains an unowned path'
    [[ "$a" =~ ^[a-f0-9]{64}$ && -f "$b" && $(hash "$b") = "$a" ]] || fail "Preserving modified/missing installation: $b"
  done < "$receipt"
}
if (( verify || uninstall )); then
  check_receipt
  if (( verify )); then printf '%s\n' 'Installed bytes and guidance verified. Live CLI acceptance is yours.'; exit 0; fi
  if [[ $(receipt_value path_added) = 1 ]];then
    profile=$(receipt_value profile);safe_path "$profile"
    [[ "$profile" = "$home_dir/.profile" || "$profile" = "$home_dir/.zprofile" ]] || fail 'Invalid receipt profile path'
  fi
  printf 'Remove installer-owned files; preserve state: %s\n' "$(receipt_value state)"
  (( !dry_run )) || exit 0
  if (( !noninteractive )); then [[ $(ask 'Uninstall? yes/no' no) = yes ]] || exit 0; fi
  while IFS=$'\t' read -r record digest file; do [[ "$record" != file ]] || rm -- "$file"; done < "$receipt"
  if [[ $(receipt_value path_added) = 1 ]]; then
    profile=$(receipt_value profile); safe_path "$profile"
    [[ "$profile" = "$home_dir/.profile" || "$profile" = "$home_dir/.zprofile" ]] || fail 'Invalid receipt profile path'
    marker="# midden-cli:$install_dir"
    if [[ -f "$profile" ]]; then profile_temp=$(mktemp "$profile.midden-new-XXXXXX"); awk -v marker="$marker" 'index($0,marker)==0' "$profile" > "$profile_temp"; mv -- "$profile_temp" "$profile"; fi
  fi
  rm -- "$receipt"
  printf '%s\n' 'Uninstalled. Recovery data and upgrade backups preserved.'; exit 0
fi
[[ -n "$hosts" ]] || fail '--hosts is required with --yes'
if [[ -f "$receipt" ]]; then
  check_receipt
  [[ $(receipt_value scope) = "$scope" && $(receipt_value project) = "$project" && $(receipt_value state) = "$state_dir" ]] || fail 'Use a separate installation directory for different scope/state.'
  hosts="$hosts,$(receipt_value hosts)"
fi
hosts=$(printf '%s' "$hosts" | tr ',' '\n' | sort -u | awk 'NF {printf "%s%s", sep,$0;sep=","}')
[[ "$hosts" =~ ^[a-z0-9,-]+$ ]] || fail 'Invalid host selection'
[[ -z "$host_path" || "$hosts" != *,* ]] || fail '--host-path requires exactly one host'
for id in ${hosts//,/ }; do
  root=$(skill_root "$id" "$scope" "$project"); safe_path "$root"
  program=$(field host "$id" 3); [[ -z "$host_path" ]] || { safe_path "$host_path"; program=$host_path; }
  command -v "$program" >/dev/null || fail "Install/authenticate $id first, or supply --host-path"
  "$program" --version || fail "$id executable check failed"
done
[[ "$scope" != project || -d "$project" ]] || fail 'Project directory must exist'
dep_path() { case "$1" in pandoc) printf '%s' "${pandoc_path:-$(command -v pandoc || true)}";; d2) printf '%s' "${d2_path:-$(command -v d2 || true)}";; esac; }
for id in pandoc d2; do
  if (( !noninteractive )); then p=$(ask "Existing $id path (auto to discover)" "$(dep_path "$id" || true)"); [[ "$p" != auto ]] || p=; case "$id" in pandoc) pandoc_path=$p;; d2) d2_path=$p;; esac; fi
  p=$(dep_path "$id"); version_arg=--version; [[ "$id" != d2 ]] || version_arg=version
  # Read-only executable probes may follow package-manager symlinks (Homebrew).
  # Installation destinations still reject symlinks through safe_path.
  if [[ -n "$p" ]]; then [[ "$p" = /* && -x "$p" ]] || fail "Absolute executable path required: $p"; "$p" "$version_arg" || fail "$id executable check failed"; fi
  printf '\n%s — %s\n  Version policy: %s\n' "$id" "$(field dependency "$id" 4)" "$(field dependency "$id" 3)"
  if [[ -n "$p" ]]; then printf '  Existing: %s; download: 0; additional disk: 0\n' "$p"
  else printf '  Download: %s; installed footprint: %s (including dependencies: unknown)\n' "$(field dependency "$id" 5)" "$(field dependency "$id" 6)"; fi
  if command -v brew >/dev/null; then printf '  Optional install: brew install %s\n' "$(field dependency "$id" 8)"
  elif command -v apt-get >/dev/null && [[ $(field dependency "$id" 9) != unsupported ]]; then printf '  Optional install: sudo apt-get install %s\n' "$(field dependency "$id" 9)"
  else printf '%s\n' '  Install separately and supply an executable path.'; fi
done
if (( !noninteractive )); then dependencies=$(ask 'Optional tools to install/check (pandoc,d2 or none)' "${dependencies:-none}"); fi
[[ "$dependencies" != none ]] || dependencies=
for id in ${dependencies//,/ }; do [[ "$id" = pandoc || "$id" = d2 ]] || fail "Unknown dependency: $id"; done
if (( !noninteractive )); then [[ $(ask 'Add binary directory to your shell profile? yes/no' no) != yes ]] || add_path=1; fi
printf '\nBinary: %s\nRecovery state: %s\n' "$install_dir/midden" "$state_dir"
for id in ${hosts//,/ }; do printf 'Skills: %s\n' "$(skill_root "$id" "$scope" "$project")"; done
printf '%s\n' 'No host model configuration or permission grants are changed. Package managers may request elevation and confirm final sizes.'
(( !dry_run )) || { printf '%s\n' 'Preview only; no downloads or writes.'; exit 0; }
if (( !noninteractive )); then [[ $(ask 'Proceed? yes/no' no) = yes ]] || exit 0; fi
stage=$(mktemp -d); stage=$(cd -- "$stage" && pwd -P)
committed=0 locked=0 profile_changed=0 profile_existed=0 profile= temporary=
rollback() {
  if (( !committed && profile_changed )); then
    if (( profile_existed ));then cp -- "$stage/profile-before" "$profile";else rm -f -- "$profile";fi
  fi
  if (( !committed )) && [[ -f "$stage/undo" ]]; then
    while IFS=$'\t' read -r file backup; do
      if [[ "$backup" = new ]]; then rm -f -- "$file"; else cp -- "$backup" "$file"; fi
    done < "$stage/undo"
  fi
  (( !locked )) || rmdir -- "$install_dir/.install.lock" || true
  [[ -z "$temporary" ]] || rm -f -- "$temporary"
  rm -rf -- "$stage"
}
trap rollback EXIT
if [[ -z "$bundle" ]]; then
  command -v curl >/dev/null || fail 'curl is required'
  if [[ "$version" = latest ]]; then location=$(curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest");version=${location##*/}; fi
  [[ "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$ ]] || fail 'Invalid release; specify --version for a public prerelease'
  asset=$(field setting asset 3);asset=${asset//\{version\}/${version#v}};asset=${asset//\{os\}/$os};asset=${asset//\{arch\}/$arch};asset="$asset.tar.gz"
  base="https://github.com/$repo/releases/download/$version"
  curl -fSL "$base/$asset" -o "$stage/release.tar.gz"; curl -fsSL "$base/SHA256SUMS" -o "$stage/SHA256SUMS"
  expected=$(awk -v name="$asset" '$2==name {print $1}' "$stage/SHA256SUMS")
  [[ "$expected" =~ ^[a-f0-9]{64}$ && $(hash "$stage/release.tar.gz") = "$expected" ]] || fail 'Checksum verification failed'
  bundle="$stage/bundle";mkdir -p "$bundle"
  printf 'midden\n' > "$stage/entries"
  awk -F '\t' '$1=="skill"{print $3} $1=="overlay"{print $2}' "$manifest" >> "$stage/entries"
  tar -tzf "$stage/release.tar.gz" > "$stage/archive-list"
  while IFS= read -r relative; do
    [[ "$relative" != /* && "$relative" != *..* ]] || fail 'Unsafe archive path'
    [[ $(awk -v name="$relative" '$0==name{n++}END{print n+0}' "$stage/archive-list") = 1 ]] || fail "Missing/duplicate entry: $relative"
    mkdir -p "$bundle/$(dirname -- "$relative")"
    tar -xOzf "$stage/release.tar.gz" "$relative" > "$bundle/$relative"
  done < "$stage/entries"
fi
safe_path "$bundle";safe_path "$bundle/midden";[[ -s "$bundle/midden" ]] || fail 'Bundle lacks a non-empty midden executable'
printf '%s\t%s\n' "$bundle/midden" "$install_dir/midden" > "$stage/files"
for id in ${hosts//,/ }; do
  root=$(skill_root "$id" "$scope" "$project")
  first=1
  while IFS=$'\t' read -r type name relative rest; do
    [[ "$type" = skill ]] || continue
    [[ "$relative" != /* && "$relative" != *..* && "$name" =~ ^[a-z0-9-]+$ ]] || fail 'Unsafe skill manifest path'
    safe_path "$bundle/$relative";source="$stage/$id-$name.md";cp -- "$bundle/$relative" "$source"
    [[ $(head -n 1 "$source") = --- ]] && grep -q '^name:' "$source" || fail "Invalid skill content: $relative"
    if (( first )); then
      while IFS=$'\t' read -r type overlay rest; do [[ "$type" != overlay ]] || { safe_path "$bundle/$overlay";printf '\n\n' >> "$source";cat "$bundle/$overlay" >> "$source"; }; done < "$manifest"
      first=0
    fi
    printf '\n\n## Installation binding\nBinary: `%s/midden` (quote its path). Use module describe --json and module invoke <capability> --input <absolute-request.json>.\nSupply roots.midden_home.path `%s` with mode rw. Preserve read-only source stores. Your CLI owns models and permissions. Author content in this host and submit recipes.compose rather than launching another agent where possible. Use the current descriptor for recipe, review, render and export operations.\n' "$install_dir" "$state_dir" >> "$source"
    for dep in pandoc d2; do p=$(dep_path "$dep");[[ -z "$p" ]] || printf 'Optional %s: `%s`; include its directory on PATH for render invocation.\n' "$dep" "$p" >> "$source"; done
    printf '%s\t%s\n' "$source" "$root/$name/SKILL.md" >> "$stage/files"
  done < "$manifest"
done
while IFS=$'\t' read -r source target; do
  safe_path "$target"
  if [[ -e "$target" ]]; then
    [[ -f "$receipt" ]] || fail "Preserving unowned file: $target"
    known=$(awk -F '\t' -v file="$target" '$1=="file"&&$3==file{print $2}' "$receipt")
    [[ -n "$known" && $(hash "$target") = "$known" ]] || fail "Preserving unowned/modified file: $target"
    [[ $(hash "$source") = "$known" || "$upgrade" = 1 ]] || fail 'Installation differs; use --upgrade for owned files'
  fi
done < "$stage/files"
for id in ${dependencies//,/ }; do
  p=$(dep_path "$id")
  if [[ -z "$p" ]]; then
    if command -v brew >/dev/null;then brew install "$(field dependency "$id" 8)"
    elif command -v apt-get >/dev/null && [[ $(field dependency "$id" 9) != unsupported ]];then sudo apt-get install "$(field dependency "$id" 9)"
    else fail "No supported package manager for $id; install separately";fi
    p=$(command -v "$id") || fail "Reopen your terminal and supply the $id path"
  fi
  if [[ "$id" = pandoc ]];then printf '# Check\n' | "$p" -f markdown -t pptx -o "$stage/check.pptx";test -s "$stage/check.pptx"
  else printf 'source -> evidence\n' > "$stage/check.d2";"$p" "$stage/check.d2" "$stage/check.svg";test -s "$stage/check.svg";fi
done
mkdir -p "$install_dir"
mkdir "$install_dir/.install.lock" || fail 'Installer locked; inspect an interrupted install before removing .install.lock'
locked=1
[[ ! -f "$receipt" ]] || check_receipt
while IFS=$'\t' read -r source target; do
  safe_path "$target"
  if [[ -e "$target" ]];then
    [[ -f "$receipt" ]] || fail "Unowned path appeared during installation: $target"
    known=$(awk -F '\t' -v file="$target" '$1=="file"&&$3==file{print $2}' "$receipt")
    [[ -n "$known" && $(hash "$target") = "$known" ]] || fail "File changed during installation: $target"
  fi
  if [[ -f "$target" && $(hash "$source") = "$(hash "$target")" ]];then continue;fi
  backup=new
  if [[ -f "$target" ]];then backup="$target.midden-backup-$(date +%s)-$$";cp -- "$target" "$backup";fi
  printf '%s\t%s\n' "$target" "$backup" >> "$stage/undo"
  mkdir -p "$(dirname -- "$target")"
  temporary=$(mktemp "$target.midden-new-XXXXXX")
  cp -- "$source" "$temporary";if [[ "$target" = "$install_dir/midden" ]];then chmod 755 "$temporary";else chmod 644 "$temporary";fi
  mv -f -- "$temporary" "$target";temporary=
done < "$stage/files"
path_added=0 profile=
if [[ -f "$receipt" ]];then path_added=$(receipt_value path_added);profile=$(receipt_value profile);fi
if (( add_path ));then
  profile="$home_dir/.profile";[[ ${SHELL:-} != *zsh* ]] || profile="$home_dir/.zprofile";safe_path "$profile"
  marker="# midden-cli:$install_dir"
  if [[ ! -f "$profile" ]] || ! grep -Fq "$marker" "$profile";then
    if [[ -f "$profile" ]];then cp -- "$profile" "$stage/profile-before";profile_existed=1;fi
    profile_changed=1
    [[ ! -f "$profile" ]] || cp -- "$profile" "$profile.midden-backup-$(date +%s)-$$"
    # Double quote escaping preserves literal spaces, dollars, backticks and slashes.
    escaped=${install_dir//\\/\\\\};escaped=${escaped//\"/\\\"};escaped=${escaped//\$/\\\$};escaped=${escaped//\`/\\\`}
    printf '\nexport PATH="%s:$PATH" %s\n' "$escaped" "$marker" >> "$profile";path_added=1
  fi
fi
printf 'schema\t1\nversion\t%s\nscope\t%s\nproject\t%s\nstate\t%s\nhosts\t%s\npath_added\t%s\nprofile\t%s\n' "$version" "$scope" "$project" "$state_dir" "$hosts" "$path_added" "$profile" > "$stage/receipt"
while IFS=$'\t' read -r source target;do [[ $(hash "$source") = "$(hash "$target")" ]] || fail "Verification failed: $target";printf 'file\t%s\t%s\n' "$(hash "$target")" "$target" >> "$stage/receipt";done < "$stage/files"
temporary=$(mktemp "$receipt.new-XXXXXX");cp -- "$stage/receipt" "$temporary";mv -f -- "$temporary" "$receipt";temporary=;committed=1
printf '\nInstalled: %s/midden\n' "$install_dir"
printf '%s\n' 'Restart your CLI if needed to discover midden-session-recovery. No live agent acceptance was performed.' \
 'First prompt: Use midden-session-recovery to assay this project’s sessions and help me choose evidence for a blog post and presentation. Ask before extracting or writing.'

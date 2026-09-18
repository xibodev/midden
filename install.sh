#!/usr/bin/env bash
# Script-owned agentic CLI installer. Bash 3.2+ (including macOS system Bash).
set -euo pipefail
version=${MIDDEN_VERSION:-} bundle= manifest= hosts=${MIDDEN_HOSTS:-} host_path= scope=user project= install_dir=${MIDDEN_INSTALL_DIR:-} state_dir=${MIDDEN_STATE_DIR:-}
home_dir=${HOME:?HOME must be set} dependencies=${MIDDEN_DEPENDENCIES:-} pandoc_path= d2_path=
noninteractive=${MIDDEN_YES:-0} dry_run=0 upgrade=0 uninstall=0 verify=0 add_path=0 no_path=${MIDDEN_NO_PATH:-0}
scope_explicit=0 state_explicit=0
usage() {
  printf '%s\n' 'Usage: bash install.sh [--version vX.Y.Z | --bundle-dir /path] [options]' \
    '  --hosts copilot-cli,claude-code,opencode --host-path /path' \
    '  --scope user|project --project /path --install-dir /path --state-dir /path' \
    '  --dependencies pandoc,d2 --pandoc-path /path --d2-path /path' \
    '  --yes --dry-run --upgrade --verify --uninstall --add-path --no-path' \
    '  --manifest /path/manifest.tsv --home-dir /isolated/test/home'
}
while (($#)); do
  case "$1" in
    --version) version=${2:?}; shift 2;; --bundle-dir) bundle=${2:?}; shift 2;;
    --manifest) manifest=${2:?}; shift 2;; --hosts) hosts=${2:?}; shift 2;;
    --host-path) host_path=${2:?}; shift 2;; --scope) scope=${2:?}; scope_explicit=1; shift 2;;
    --project) project=${2:?}; shift 2;; --install-dir) install_dir=${2:?}; shift 2;;
    --state-dir) state_dir=${2:?}; state_explicit=1; shift 2;; --home-dir) home_dir=${2:?}; shift 2;;
    --dependencies) dependencies=${2:?}; shift 2;; --pandoc-path) pandoc_path=${2:?}; shift 2;; --d2-path) d2_path=${2:?}; shift 2;;
    --yes|--non-interactive) noninteractive=1; shift;; --dry-run) dry_run=1; shift;;
    --upgrade) upgrade=1; shift;; --uninstall) uninstall=1; shift;; --verify) verify=1; shift;; --add-path) add_path=1; shift;;
    --no-path) no_path=1; shift;;
    --help|-h) usage; exit 0;; *) printf 'Unknown option: %s\n' "$1" >&2; exit 2;;
  esac
done
fail() { printf '%s\n' "$*" >&2; exit 1; }
step() { printf '  %-12s %s\n' "$1" "$2"; }
download() { curl --retry 3 --connect-timeout 15 --max-time 180 -fsSL "$1" -o "$2" || fail "Download failed: $1. Check your connection/proxy and rerun; existing installation is preserved."; }
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
version=${version:-$(field setting version 3)}
repo=$(field setting repository 3)
[[ "$repo" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || fail 'Invalid repository in manifest'
install_dir=${install_dir:-"$home_dir/$(field setting install_relative 3)"}
state_dir=${state_dir:-"$home_dir/$(field setting state_relative 3)"}
ask() { local reply; printf '%s [%s]: ' "$1" "$2" >&2; IFS= read -r reply || fail 'Input ended; use --yes with explicit choices.'; printf '%s' "${reply:-$2}"; }
confirm() {
  local reply
  while :; do
    reply=$(ask "$1" Y/n)
    case "$reply" in y|Y|yes|Yes|Y/n) return 0;; n|N|no|No) return 1;; *) printf '  Please enter yes or no.\n' >&2;; esac
  done
}
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
  if (( verify )); then step verified "Midden $(receipt_value version): executable and skills match the receipt."; exit 0; fi
  if [[ $(receipt_value path_added) = 1 ]];then
    profile=$(receipt_value profile);safe_path "$profile"
    case "$profile" in "$home_dir/.profile"|"$home_dir/.zprofile"|"$home_dir/.zshrc"|"$home_dir/.bashrc"|"$home_dir/.bash_profile"|"$home_dir/.config/fish/conf.d/midden.fish") ;; *) fail 'Invalid receipt profile path';; esac
  fi
  printf 'Remove installer-owned files; preserve state: %s\n' "$(receipt_value state)"
  (( !dry_run )) || exit 0
  if (( !noninteractive )); then confirm 'Remove these installed files?' || exit 0; fi
  while IFS=$'\t' read -r record digest file; do [[ "$record" != file ]] || rm -- "$file"; done < "$receipt"
  if [[ $(receipt_value path_added) = 1 ]]; then
    profile=$(receipt_value profile); safe_path "$profile"
    case "$profile" in "$home_dir/.profile"|"$home_dir/.zprofile"|"$home_dir/.zshrc"|"$home_dir/.bashrc"|"$home_dir/.bash_profile"|"$home_dir/.config/fish/conf.d/midden.fish") ;; *) fail 'Invalid receipt profile path';; esac
    marker="# midden-cli:$install_dir"
    if [[ -f "$profile" ]]; then profile_temp=$(mktemp "$profile.midden-new-XXXXXX"); awk -v marker="$marker" 'index($0,marker)==0' "$profile" > "$profile_temp"; mv -- "$profile_temp" "$profile"; fi
  fi
  rm -- "$receipt"
  printf '%s\n' 'Uninstalled. Recovery data and upgrade backups preserved.'; exit 0
fi
printf '\n  Midden %s - recovery for your agentic CLI\n\n' "$version"
step system "$os/$arch"
if [[ -f "$receipt" ]]; then
  check_receipt
  (( scope_explicit )) || scope=$(receipt_value scope)
  [[ -n "$project" ]] || project=$(receipt_value project)
  if (( !state_explicit )) && [[ -z ${MIDDEN_STATE_DIR:-} ]]; then state_dir=$(receipt_value state); fi
  [[ $(receipt_value scope) = "$scope" && $(receipt_value project) = "$project" && $(receipt_value state) = "$state_dir" ]] || fail 'Use a separate installation directory for different scope/state.'
  hosts="$hosts,$(receipt_value hosts)"
  step existing "$(receipt_value version) found; owned files will be updated with backups."
  (( noninteractive )) || upgrade=1
fi
if [[ -z "$hosts" ]]; then
  available=()
  while IFS=$'\t' read -r type id program rest; do
    [[ "$type" = host ]] || continue
    if command -v "$program" >/dev/null; then available[${#available[@]}]=$id; fi
  done < "$manifest"
  if (( ${#available[@]} == 0 )); then
    while IFS=$'\t' read -r type id program personal local_path name url; do [[ "$type" != host ]] || step "$name" "$url"; done < "$manifest"
    fail 'No supported CLI found. Install one from the links above, sign in, then rerun.'
  fi
  if (( ${#available[@]} == 1 || noninteractive )); then hosts=$(IFS=,; printf '%s' "${available[*]}")
  else
    for ((i=0;i<${#available[@]};i++)); do printf '  %s) %s\n' "$((i+1))" "$(field host "${available[$i]}" 6)"; done
    while :; do
      choice=$(ask 'Choose CLI numbers (comma-separated), or all' all)
      if [[ "$choice" = all ]]; then hosts=$(IFS=,; printf '%s' "${available[*]}"); break; fi
      selected= valid=1
      for n in ${choice//,/ }; do
        if [[ ! "$n" =~ ^[1-9]$ ]] || (( n>${#available[@]} )); then valid=0; break; fi
        selected="${selected:+$selected,}${available[$((n-1))]}"
      done
      if (( valid )) && [[ -n "$selected" ]]; then hosts=$selected; break; fi
      printf '  Choose a listed number, such as 1 or 1,2.\n'
    done
  fi
fi
hosts=$(printf '%s' "$hosts" | tr ',' '\n' | sort -u | awk 'NF {printf "%s%s", sep,$0;sep=","}')
[[ "$hosts" =~ ^[a-z0-9,-]+$ ]] || fail 'Invalid host selection'
[[ -z "$host_path" || "$hosts" != *,* ]] || fail '--host-path requires exactly one host'
for id in ${hosts//,/ }; do
  root=$(skill_root "$id" "$scope" "$project"); safe_path "$root"
  program=$(field host "$id" 3); [[ -z "$host_path" ]] || { safe_path "$host_path"; program=$host_path; }
  command -v "$program" >/dev/null || fail "Install/authenticate $id first, or supply --host-path"
  "$program" --version >/dev/null 2>&1 || fail "$id did not answer --version. Repair the CLI or use --host-path."
  step host "$(field host "$id" 6) ready"
done
[[ "$scope" != project || -d "$project" ]] || fail 'Project directory must exist'
dep_path() { case "$1" in pandoc) printf '%s' "${pandoc_path:-$(command -v pandoc || true)}";; d2) printf '%s' "${d2_path:-$(command -v d2 || true)}";; esac; }
if (( !noninteractive )) && [[ -z "$dependencies" ]]; then
  printf '\n  Optional capabilities (core recovery needs neither):\n'
  i=0
  for id in pandoc d2; do
    i=$((i+1)); p=$(dep_path "$id")
    detail='download/disk size unavailable; package manager selects version'
    [[ -z "$p" ]] || detail='found; no download'
    printf '  %s) %s (%s; %s)\n' "$i" "$(field dependency "$id" 4)" "$id" "$detail"
  done
  while :; do
    choice=$(ask 'Add capabilities: 1, 2, both, or none' none)
    case "$choice" in 1) dependencies=pandoc; break;; 2) dependencies=d2; break;; both|1,2) dependencies=pandoc,d2; break;; none) dependencies=none; break;; *) printf '  Choose 1, 2, both, or none.\n';; esac
  done
fi
[[ "$dependencies" != none ]] || dependencies=
for id in ${dependencies//,/ }; do [[ "$id" = pandoc || "$id" = d2 ]] || fail "Unknown dependency: $id"; done
for id in ${dependencies//,/ }; do
  p=$(dep_path "$id")
  if [[ -n "$p" ]]; then [[ "$p" = /* && -x "$p" ]] || fail "Missing $id executable: $p"; step "$id" 'Reuse existing tool; functional check follows confirmation.'
  elif command -v brew >/dev/null; then step "$id" 'Install with Homebrew (version and total size reported by manager).'
  elif command -v apt-get >/dev/null && [[ $(field dependency "$id" 9) != unsupported ]]; then step "$id" 'Install with apt (manager confirms version, size and elevation).'
  else fail "To add $id, install it separately and supply --$id-path, or rerun with core recovery only."; fi
done
(( noninteractive || no_path )) || add_path=1
(( !no_path )) || add_path=0
printf '\nBinary: %s\nRecovery state: %s\n' "$install_dir/midden" "$state_dir"
for id in ${hosts//,/ }; do printf 'Skills: %s\n' "$(skill_root "$id" "$scope" "$project")"; done
printf '%s\n' 'No host model configuration or permission grants are changed. Package managers may request elevation and confirm final sizes.'
step PATH "Update shell profile: $add_path (use --no-path to opt out)"
(( !dry_run )) || { printf '%s\n' 'Preview only; no downloads or writes.'; exit 0; }
if (( !noninteractive )); then confirm 'Install with these settings?' || { printf 'Cancelled. Nothing installed.\n'; exit 0; }; fi
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
  step download "Midden $version for $os/$arch..."
  download "$base/$asset" "$stage/release.tar.gz"; download "$base/SHA256SUMS" "$stage/SHA256SUMS"
  expected=$(awk -v name="$asset" '$2==name {print $1}' "$stage/SHA256SUMS")
  [[ "$expected" =~ ^[a-f0-9]{64}$ && $(hash "$stage/release.tar.gz") = "$expected" ]] || fail 'Checksum verification failed'
  step verified 'Archive checksum matches.'
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
    printf 'Optional rendering uses pandoc/d2 on PATH. If unavailable, keep editable source and report the missing renderer.\n' >> "$source"
    [[ -z "$pandoc_path" ]] || printf 'Explicit Pandoc: `%s`; include its directory on PATH for rendering.\n' "$pandoc_path" >> "$source"
    [[ -z "$d2_path" ]] || printf 'Explicit D2: `%s`; include its directory on PATH for rendering.\n' "$d2_path" >> "$source"
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
  step tool "Setting up $id..."
  p=$(dep_path "$id")
  if [[ -z "$p" ]]; then
    if command -v brew >/dev/null;then brew install "$(field dependency "$id" 8)"
    elif command -v apt-get >/dev/null && [[ $(field dependency "$id" 9) != unsupported ]];then sudo apt-get install "$(field dependency "$id" 9)"
    else fail "No supported package manager for $id; install separately";fi
    builtin hash -r 2>/dev/null || true
    p=$(command -v "$id") || fail "$id was installed but not found. Supply --$id-path; Midden files have not been changed."
  fi
  if [[ "$id" = pandoc ]];then printf '# Check\n' | "$p" -f markdown -t pptx -o "$stage/check.pptx";test -s "$stage/check.pptx"
  else printf 'source -> evidence\n' > "$stage/check.d2";"$p" "$stage/check.d2" "$stage/check.svg";test -s "$stage/check.svg";fi
done
step install 'Writing executable and CLI skills...'
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
  if [[ -z "$profile" ]]; then
    case ${SHELL:-} in
      *zsh) profile="$home_dir/.zshrc";;
      *bash) profile="$home_dir/.bashrc"; [[ "$os" != darwin ]] || profile="$home_dir/.bash_profile";;
      *fish) profile="$home_dir/.config/fish/conf.d/midden.fish";;
      *) profile="$home_dir/.profile";;
    esac
  fi
  safe_path "$profile"
  mkdir -p "$(dirname -- "$profile")"
  marker="# midden-cli:$install_dir"
  if [[ ! -f "$profile" ]] || ! grep -Fq "$marker" "$profile";then
    if [[ -f "$profile" ]];then cp -- "$profile" "$stage/profile-before";profile_existed=1;fi
    profile_changed=1
    [[ ! -f "$profile" ]] || cp -- "$profile" "$profile.midden-backup-$(date +%s)-$$"
    # Double quote escaping preserves literal spaces, dollars, backticks and slashes.
    escaped=${install_dir//\\/\\\\};escaped=${escaped//\"/\\\"};escaped=${escaped//\$/\\\$};escaped=${escaped//\`/\\\`}
    if [[ "$profile" = *.fish ]]; then printf '\nset -gx PATH "%s" $PATH %s\n' "$escaped" "$marker" >> "$profile"
    else printf '\nexport PATH="%s:$PATH" %s\n' "$escaped" "$marker" >> "$profile"; fi
    path_added=1
  fi
fi
printf 'schema\t1\nversion\t%s\nscope\t%s\nproject\t%s\nstate\t%s\nhosts\t%s\npath_added\t%s\nprofile\t%s\n' "$version" "$scope" "$project" "$state_dir" "$hosts" "$path_added" "$profile" > "$stage/receipt"
while IFS=$'\t' read -r source target;do [[ $(hash "$source") = "$(hash "$target")" ]] || fail "Verification failed: $target";printf 'file\t%s\t%s\n' "$(hash "$target")" "$target" >> "$stage/receipt";done < "$stage/files"
temporary=$(mktemp "$receipt.new-XXXXXX");cp -- "$stage/receipt" "$temporary";mv -f -- "$temporary" "$receipt";temporary=;committed=1
step ready 'Midden installed. Executable and skill checksums verified.'
shadow=$(command -v midden || true)
if [[ -n "$shadow" && "$shadow" != "$install_dir/midden" ]]; then step notice "Another midden is on PATH: $shadow. This install: $install_dir/midden"; fi
printf '\n  Open a new terminal in your project and launch:\n'
for id in ${hosts//,/ }; do printf '    %s   (%s)\n' "$(field host "$id" 3)" "$(field host "$id" 6)"; done
printf '\n  Then ask: Use midden-session-recovery to assay this project’s sessions.\n  Help me choose evidence for a blog post. Ask before extracting or writing.\n'
printf '\n  Manage this install with the same installer: --verify, --upgrade, --uninstall.\n'

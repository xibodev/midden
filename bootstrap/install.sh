#!/bin/sh
# Standalone POSIX entry: definitions are consumed before anything reads stdin.

mb_fail() {
    printf 'midden bootstrap: %s\n' "$*" >&2
    exit 1
}

mb_path() (
    mb_p=$1
    [ -n "$mb_p" ] || mb_fail "An empty path is not allowed"
    printf '%s\n' "$mb_p" | awk 'NR != 1 || /[[:cntrl:]]/ {bad=1} END {exit bad}' ||
        mb_fail "Paths must not contain control characters"
    case "$mb_p" in
        *\\*) mb_fail "Backslashes are not supported in Unix paths" ;;
        /*) ;;
        *) mb_p=$mb_cwd/$mb_p ;;
    esac
    printf '%s\n' "$mb_p" | awk -F / '{
        n=0
        for(i=1;i<=NF;i++) {
            if($i=="" || $i==".") continue
            if($i=="..") {if(n>0) n--; continue}
            parts[++n]=$i
        }
        result=""
        for(i=1;i<=n;i++) result=result "/" parts[i]
        if(result=="") print "/"
        else print result
    }'
)

mb_no_links() (
    mb_n=$1
    while [ "$mb_n" != / ]; do
        [ ! -L "$mb_n" ] || mb_fail "Refusing linked path: $mb_n"
        [ "$mb_n" = "$1" ] || [ ! -e "$mb_n" ] || [ -d "$mb_n" ] ||
            mb_fail "A path ancestor is not a directory: $mb_n"
        mb_n=${mb_n%/*}
        [ -n "$mb_n" ] || mb_n=/
    done
)

mb_under() {
    [ "$2" != / ] || return 0
    case "$1/" in "$2/"*) return 0 ;; esac
    return 1
}

mb_state_disjoint() (
    mb_sd_logical=$(mb_path "$1")
    mb_sd_parent=$mb_sd_logical
    mb_sd_suffix=
    while [ ! -d "$mb_sd_parent" ]; do
        [ ! -L "$mb_sd_parent" ] || mb_fail "Cannot resolve protected state path: $mb_sd_parent"
        mb_sd_suffix=/${mb_sd_parent##*/}$mb_sd_suffix
        mb_sd_parent=${mb_sd_parent%/*}
        [ -n "$mb_sd_parent" ] || mb_sd_parent=/
    done
    mb_sd_physical=$(CDPATH= cd -P "$mb_sd_parent" && pwd -P) ||
        mb_fail "Cannot resolve protected state directory: $mb_sd_parent"
    mb_sd_physical=$(mb_path "$mb_sd_physical$mb_sd_suffix")
    mb_sd_install=$mb_root
    for mb_sd_state in "$mb_sd_logical" "$mb_sd_physical"; do
        if [ "$mb_system" = darwin ]; then
            # Darwin installations must also be safe on case-insensitive volumes.
            mb_sd_install=$(printf '%s\n' "$mb_sd_install" | awk '{print tolower($0)}')
            mb_sd_state=$(printf '%s\n' "$mb_sd_state" | awk '{print tolower($0)}')
        fi
        if mb_under "$mb_sd_install" "$mb_sd_state" || mb_under "$mb_sd_state" "$mb_sd_install"; then
            mb_fail "Installation/data-state overlap with $2: $mb_sd_logical"
        fi
    done
)

mb_protect_state() (
    [ "$mb_mode" != cli ] || exit 0
    case "$mb_system" in
        darwin) mb_ps_ui="$mb_home/Library/Application Support/Midden" ;;
        linux) mb_ps_ui=${XDG_DATA_HOME:-$mb_home/.local/share}/midden ;;
    esac
    mb_state_disjoint "$mb_ps_ui" "UI data"
    mb_state_disjoint "${MIDDEN_HOME:-$mb_home/.midden}" "core state"
    if [ -n "${COMPA_HOME:-}" ]; then
        mb_state_disjoint "$COMPA_HOME" COMPA_HOME
    fi
)

mb_hash() (
    if [ "$mb_hasher" = sha256sum ]; then
        mb_h=$(sha256sum "$1") || mb_fail "Cannot hash $1"
    else
        mb_h=$(shasum -a 256 "$1") || mb_fail "Cannot hash $1"
    fi
    printf '%s\n' "${mb_h%% *}"
)

mb_limit() (
    mb_l=$(wc -c < "$1") || mb_fail "Cannot measure $1"
    [ "$mb_l" -le "$2" ] || mb_fail "Size limit exceeded: $1 ($2 bytes maximum)"
)

mb_copy() (
    mb_no_links "$1"
    [ -f "$1" ] || mb_fail "Expected a regular input file: $1"
    head -c "$(($3 + 1))" < "$1" > "$2" || mb_fail "Cannot snapshot $1"
    mb_limit "$2" "$3"
)

mb_fetch() (
    if [ -n "$mb_distribution" ]; then
        mb_copy "$mb_distribution/$1" "$mb_work/distribution/$1" "$2"
    else
        # -q must be first: no ~/.curlrc, netrc, token lookup, or auth fallback.
        (
            set +e
            curl -q -fsSL --proto '=https' --proto-redir '=https' \
                --connect-timeout 15 --max-time 180 --max-filesize "$2" "$mb_url/$1"
            printf '%s\n' "$?" > "$mb_work/download.status"
        ) | head -c "$(($2 + 1))" > "$mb_work/distribution/$1"
        mb_limit "$mb_work/distribution/$1" "$2"
        [ -f "$mb_work/download.status" ] &&
            [ "$(cat "$mb_work/download.status")" = 0 ] ||
            mb_fail "Release download failed: $mb_url/$1 (no fallback was attempted)"
    fi
)

mb_checksums() {
    awk '
        {
            hash=substr($0,1,64); name=substr($0,67)
            if (length(hash)!=64 || hash ~ /[^0-9a-fA-F]/ ||
                substr($0,65,2)!="  " || name !~ /^[A-Za-z0-9][A-Za-z0-9._+-]*$/ ||
                seen[tolower(name)]++) bad=1
            names[name]=1
        }
        END {
            if (!names["manifest.tsv"] || !names["build-manifest.json"] ||
                !names["install.sh"] || !names["install.ps1"] || NR>4096) bad=1
            exit bad
        }
    ' "$mb_work/distribution/SHA256SUMS" || mb_fail "Invalid or duplicate SHA256SUMS checksum inventory"
}

mb_checksum() (
    mb_c=$(awk -v name="$1" 'substr($0,67)==name {print tolower(substr($0,1,64))}' \
        "$mb_work/distribution/SHA256SUMS")
    [ -n "$mb_c" ] || mb_fail "Missing checksum for $1"
    [ "$(mb_hash "$mb_work/distribution/$1")" = "$mb_c" ] || mb_fail "Checksum mismatch: $1"
)

mb_manifest() {
    awk -F '\t' '
        function reject() {bad=1; exit 1}
        function path(s, a,n,i) {
            if (s !~ /^[A-Za-z0-9][A-Za-z0-9._+\/-]*$/) return 0
            n=split(s,a,"/")
            for(i=1;i<=n;i++) if(a[i]=="" || a[i]=="." || a[i]=="..") return 0
            return 1
        }
        function product(p) {return (p=="core" || p=="ui" || p=="bundle")}
        function target(p,t) {
            if(p=="bundle") return (t=="universal")
            return (t ~ /^(linux|darwin)\/(amd64|arm64)$/ || t=="windows/amd64")
        }
        function layout(p,s) {
            if(p=="core") return (s=="midden" || s=="midden.exe" || s=="LICENSE" ||
                s=="THIRD_PARTY_NOTICES.txt")
            if(p=="ui") return (s ~ /^bundles\// || s=="midden" || s=="midden.exe" ||
                s=="midden-ui" || s=="midden-ui.exe" || s=="LICENSE" || s=="NOTICE" ||
                s=="start.ps1" || s=="start.sh" || s=="package-manifest.json" ||
                s=="THIRD_PARTY_NOTICES.txt")
            return (s ~ /^(bundles|installer)\// || s=="LICENSE" || s=="install.sh" || s=="install.ps1")
        }
        NR==1 {if($0!="format\tmidden-release-v1") reject()}
        $1=="format" {if(NF!=2 || $2!="midden-release-v1" || format++) reject(); next}
        $1=="version" {
            if(NF!=2 || version!="" || $2 !~ /^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.+-]+)?$/) reject()
            version=$2; next
        }
        $1=="commit" {
            if(NF!=2 || commit++ || (length($2)!=40 && length($2)!=64) || $2 ~ /[^0-9a-fA-F]/) reject()
            next
        }
        $1=="archive" {
            key=$2 SUBSEP $3
            if(NF!=4 || !product($2) || !target($2,$3) || archives[key]!="" ||
                $4 !~ /^[A-Za-z0-9][A-Za-z0-9._+-]*$/ || archiveNames[tolower($4)]++) reject()
            archives[key]=$4; products[key]=$2; targets[key]=$3; next
        }
        $1=="file" {
            key=$2 SUBSEP $3; full=key SUBSEP $4
            if(NF!=5 || !product($2) || !target($2,$3) || !path($4) || !layout($2,$4) ||
                length($5)!=64 || $5 ~ /[^0-9a-fA-F]/ || seen[key SUBSEP tolower($4)]++) reject()
            files[full]=1; fileKeys[full]=key; fileNames[full]=$4
            if(++counts[key]>4096) reject()
            prefix=$4
            while(sub(/\/[^\/]+$/,"",prefix)) {
                fold=key SUBSEP tolower(prefix)
                if(directories[fold]!="" && directories[fold]!=prefix) reject()
                directories[fold]=prefix
            }
            next
        }
        {reject()}
        END {
            if(bad) exit 1
            if(format!=1 || commit!=1 || version=="") exit 1
            for(key in archives) {
                t=targets[key]; p=products[key]; gsub(/\//,"_",t)
                suffix=".tar.gz"
                if(targets[key]=="windows/amd64") suffix=".zip"
                if(p=="bundle") expected="midden-bundle_" version ".zip"
                else expected="midden-" p "_" version "_" t suffix
                if(archives[key]!=expected || !counts[key]) exit 1
                binary="midden"
                if(targets[key]=="windows/amd64") binary="midden.exe"
                if(p=="core") {
                    required=2
                    if((key SUBSEP "THIRD_PARTY_NOTICES.txt") in files) required++
                    if(counts[key]!=required || !files[key SUBSEP binary] ||
                        !files[key SUBSEP "LICENSE"]) exit 1
                }
                if(p=="ui") {
                    ui="midden-ui"
                    if(targets[key]=="windows/amd64") ui="midden-ui.exe"
                    if(!files[key SUBSEP binary] || !files[key SUBSEP ui] ||
                        !files[key SUBSEP "LICENSE"] || !files[key SUBSEP "NOTICE"] ||
                        !files[key SUBSEP "start.sh"] ||
                        !files[key SUBSEP "start.ps1"] || !files[key SUBSEP "package-manifest.json"]) exit 1
                }
                if(p=="bundle" && (!files[key SUBSEP "installer/install.py"] ||
                    !files[key SUBSEP "installer/bundle-manifest.json"] ||
                    !files[key SUBSEP "install.sh"] || !files[key SUBSEP "install.ps1"] ||
                    !files[key SUBSEP "LICENSE"])) exit 1
            }
            for(full in files) {
                key=fileKeys[full]; name=fileNames[full]
                if(!(key in archives)) exit 1
                while(sub(/\/[^\/]+$/,"",name)) if(seen[key SUBSEP tolower(name)]) exit 1
            }
        }
    ' "$mb_work/distribution/manifest.tsv" || mb_fail "Invalid release manifest.tsv or archive/file allowlist"
}

mb_select() (
    mb_s=$(awk -F '\t' -v p="$1" -v t="$2" '$1=="archive" && $2==p && $3==t {print $4}' \
        "$mb_work/distribution/manifest.tsv")
    [ -n "$mb_s" ] || mb_fail "No exact native $1 archive declared for $2"
    awk -F '\t' -v p="$1" -v t="$2" \
        '$1=="file" && $2==p && $3==t {print $4 "\t" tolower($5)}' \
        "$mb_work/distribution/manifest.tsv" | sort > "$mb_work/$1.files"
    printf '%s\n' "$mb_s"
)

mb_extract_tar() (
    mb_xt_product=$1
    mb_xt_archive=$mb_work/distribution/$2
    mb_xt_tar=$mb_work/$1.tar
    mb_xt_dest=$3
    (
        set +e
        gzip -dc "$mb_xt_archive"
        printf '%s\n' "$?" > "$mb_work/gzip.status"
    ) | head -c "$((mb_archive_limit + 1))" > "$mb_xt_tar"
    mb_limit "$mb_xt_tar" "$mb_archive_limit"
    [ -f "$mb_work/gzip.status" ] && [ "$(cat "$mb_work/gzip.status")" = 0 ] ||
        mb_fail "Invalid compressed archive: $2"
    # Preserve raw names while listing; ignore zero padding so concatenated
    # archives cannot hide extra members. Extraction only ever writes stdout.
    tar -tP --ignore-zeros -f "$mb_xt_tar" > "$mb_work/tar.names" || mb_fail "Invalid archive inventory: $2"
    tar -tvP --ignore-zeros -f "$mb_xt_tar" > "$mb_work/tar.types" || mb_fail "Invalid archive types: $2"
    awk 'substr($0,1,1)!="-" {bad=1} END {exit (bad || NR==0 || NR>4096)}' \
        "$mb_work/tar.types" || mb_fail "Archive must contain only ordinary files, not links/devices/directories"
    [ "$(wc -l < "$mb_work/tar.types")" -eq "$(wc -l < "$mb_work/tar.names")" ] ||
        mb_fail "Ambiguous archive member names"
    sort "$mb_work/tar.names" > "$mb_work/tar.sorted"
    cut -f 1 "$mb_work/$mb_xt_product.files" | sort > "$mb_work/tar.expected"
    cmp -s "$mb_work/tar.sorted" "$mb_work/tar.expected" ||
        mb_fail "Archive members differ from the exact canonical file allowlist (unsafe, extra or duplicate member)"
    mkdir "$mb_xt_dest" || mb_fail "Cannot create private extraction directory"
    mb_xt_total=0
    while IFS="$mb_tab" read -r mb_xt_name mb_xt_hash; do
        mb_xt_file=$mb_xt_dest/$mb_xt_name
        mkdir -p "${mb_xt_file%/*}" || mb_fail "Cannot create private member directory"
        (
            set +e
            tar -xOP --ignore-zeros -f "$mb_xt_tar" "$mb_xt_name"
            printf '%s\n' "$?" > "$mb_work/tar.status"
        ) | head -c "$((mb_archive_limit - mb_xt_total + 1))" > "$mb_xt_file"
        mb_xt_size=$(wc -c < "$mb_xt_file")
        mb_xt_total=$((mb_xt_total + mb_xt_size))
        [ "$mb_xt_total" -le "$mb_archive_limit" ] || mb_fail "Expanded archive size limit exceeded"
        [ -f "$mb_work/tar.status" ] && [ "$(cat "$mb_work/tar.status")" = 0 ] ||
            mb_fail "Archive member extraction failed: $mb_xt_name"
        [ "$(mb_hash "$mb_xt_file")" = "$mb_xt_hash" ] ||
            mb_fail "File checksum mismatch: $mb_xt_name"
        chmod 644 "$mb_xt_file" || mb_fail "Cannot set private member permissions"
    done < "$mb_work/$mb_xt_product.files"
)

mb_python_select() {
    mb_python=${PYTHON:-}
    if [ -n "$mb_python" ]; then
        "$mb_python" -I -c 'import sys; sys.exit(0 if sys.version_info >= (3, 9) else 1)' </dev/null ||
            mb_fail "CLI mode requires an existing Python 3.9+; the explicit PYTHON was unsuitable"
        return
    fi
    for mb_candidate in python3 python; do
        if command -v "$mb_candidate" >/dev/null 2>&1 &&
            "$mb_candidate" -I -c 'import sys; sys.exit(0 if sys.version_info >= (3, 9) else 1)' </dev/null; then
            mb_python=$mb_candidate
            return
        fi
    done
    mb_fail "CLI mode requires Python 3.9+; set PYTHON to an existing interpreter. Nothing was installed."
}

mb_extract_zip() {
    "$mb_python" -I -B - "$mb_work/distribution/$1" "$mb_work/bundle.files" "$mb_work/bundle" <<'PY'
import hashlib
from pathlib import Path, PurePosixPath
import re
import stat
import sys
import zipfile

try:
    archive, inventory, destination = map(Path, sys.argv[1:])
    expected = dict(line.split("\t") for line in inventory.read_text(encoding="ascii").splitlines())
    limit = 256 * 1024 * 1024
    with zipfile.ZipFile(archive) as source:
        members = source.infolist()
        if not 0 < len(members) <= 4096:
            raise ValueError("ZIP member count limit exceeded")
        seen = set()
        total = 0
        for entry in members:
            name = entry.filename
            parts = PurePosixPath(name).parts
            mode = entry.external_attr >> 16
            if (not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._+/-]*", name)
                    or name != entry.orig_filename or str(PurePosixPath(name)) != name
                    or any(part in (".", "..") for part in parts)
                    or name.casefold() in seen or name not in expected
                    or entry.is_dir() or stat.S_IFMT(mode) not in (0, stat.S_IFREG)
                    or entry.flag_bits & 1):
                raise ValueError("ZIP must contain only canonical, unique ordinary files")
            seen.add(name.casefold())
            total += entry.file_size
            if total > limit:
                raise ValueError("Expanded ZIP size limit exceeded")
        if {entry.filename for entry in members} != set(expected):
            raise ValueError("ZIP differs from the exact manifest file allowlist")
        for entry in members:
            with source.open(entry) as stream:
                data = stream.read(limit + 1)
            if len(data) > limit or hashlib.sha256(data).hexdigest() != expected[entry.filename]:
                raise ValueError("ZIP file checksum or size limit mismatch: " + entry.filename)
            output = destination.joinpath(*PurePosixPath(entry.filename).parts)
            output.parent.mkdir(parents=True, exist_ok=True)
            with output.open("xb") as stream:
                stream.write(data)
except (OSError, ValueError, RuntimeError, zipfile.BadZipFile) as error:
    print("midden bootstrap: " + str(error), file=sys.stderr)
    sys.exit(1)
PY
}

mb_check_file() (
    mb_no_links "$1"
    [ -f "$1" ] || mb_fail "Missing or nonordinary receipt-owned file: $1"
    [ "$(mb_hash "$1")" = "$2" ] || mb_fail "Modified file / checksum mismatch: $1"
)

mb_check_link() (
    mb_no_links "${1%/*}"
    [ -L "$1" ] && [ "$(readlink "$1")" = "$2" ] || mb_fail "Modified or missing owned launch link: $1"
)

mb_owned() (
    while IFS="$mb_tab" read -r mb_o_name mb_o_hash; do
        mb_check_file "$mb_root/$mb_o_name" "$mb_o_hash"
    done < "$mb_work/old.files"
    while IFS="$mb_tab" read -r mb_o_name mb_o_target; do
        mb_check_link "$mb_bin/$mb_o_name" "$mb_o_target"
    done < "$mb_work/old.links"
    if [ -n "$mb_old_receipt" ]; then
        mb_check_file "$mb_receipt" "$mb_old_receipt"
    fi
)

mb_read_receipt() {
    : > "$mb_work/old.files"
    : > "$mb_work/old.links"
    mb_old_receipt=
    if [ -e "$mb_receipt" ] || [ -L "$mb_receipt" ]; then
        mb_copy "$mb_receipt" "$mb_work/receipt.old" "$mb_metadata_limit"
        awk -F '\t' '
            function bad() {failed=1; exit 1}
            $1=="file" {
                if(NF!=3 || $2 !~ /^[A-Za-z0-9.][A-Za-z0-9._+\/-]*$/ ||
                    $2 ~ /(^|\/)\.\.?($|\/)/ || $2 ~ /\/\// || $2 ~ /\/$/ ||
                    length($3)!=64 || $3 ~ /[^0-9a-f]/ || files[tolower($2)]++) bad()
                names[$2]=1; count++; next
            }
            $1=="link" {
                if(NF!=3 || ($2!="midden" && $2!="midden-ui") || links[$2]++) bad()
                targets[$2]=$3; next
            }
            {
                if(NF!=2 || meta[$1]!="" || $2=="" ||
                    $1 !~ /^(format|mode|version|target|root|home|project|host)$/) bad()
                meta[$1]=$2
            }
            END {
                if(failed) exit 1
                if(meta["format"]!="midden-bootstrap-v1" || meta["mode"] !~ /^(ui|core|cli)$/ ||
                    meta["version"] !~ /^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.+-]+)?$/ ||
                    meta["target"] !~ /^(linux|darwin)\/(amd64|arm64)$/ ||
                    meta["root"]=="" || meta["home"]=="" || meta["project"]=="" ||
                    meta["host"] !~ /^(copilot|claude|agents)$/ || count<1 || count>4096) exit 1
                for(name in names) {
                    if(meta["mode"]=="cli" && name!=".midden-bootstrap-cli.py") exit 1
                    if(meta["mode"]=="core" && name !~ /^(midden|LICENSE|THIRD_PARTY_NOTICES[.]txt)$/) exit 1
                    if(meta["mode"]=="ui" && name !~ /^bundles\// &&
                        name !~ /^(midden|midden-ui|LICENSE|NOTICE|start[.]ps1|start[.]sh|package-manifest[.]json|THIRD_PARTY_NOTICES[.]txt)$/) exit 1
                    parent=name
                    while(sub(/\/[^\/]+$/,"",parent)) if(files[tolower(parent)]) exit 1
                }
                for(name in targets)
                    if(targets[name]!=meta["root"] "/" name ||
                        (name=="midden-ui" && meta["mode"]!="ui")) exit 1
            }
        ' "$mb_work/receipt.old" || mb_fail "Invalid bootstrap installation receipt"
        mb_old_receipt=$(mb_hash "$mb_work/receipt.old")
        mb_record_mode=$(awk -F '\t' '$1=="mode"{print $2}' "$mb_work/receipt.old")
        mb_record_root=$(awk -F '\t' '$1=="root"{print $2}' "$mb_work/receipt.old")
        mb_record_home=$(awk -F '\t' '$1=="home"{print $2}' "$mb_work/receipt.old")
        [ "$mb_record_root" = "$mb_root" ] && [ "$mb_record_home" = "$mb_home" ] ||
            mb_fail "Receipt installation/home binding differs; refusing relocated ownership"
        if [ "$mb_mode_explicit" = no ] && [ "$mb_operation" != install ]; then
            mb_mode=$mb_record_mode
        fi
        [ "$mb_mode" = "$mb_record_mode" ] || mb_fail "Mode differs from the existing receipt; uninstall that mode first"
        mb_version=$(awk -F '\t' '$1=="version"{print $2}' "$mb_work/receipt.old")
        mb_record_project=$(awk -F '\t' '$1=="project"{print $2}' "$mb_work/receipt.old")
        mb_record_host=$(awk -F '\t' '$1=="host"{print $2}' "$mb_work/receipt.old")
        if [ "$mb_mode" = cli ]; then
            if [ "$mb_project_explicit" = no ]; then mb_project=$mb_record_project; fi
            if [ "$mb_host_explicit" = no ]; then mb_host=$mb_record_host; fi
            [ "$mb_project" = "$mb_record_project" ] && [ "$mb_host" = "$mb_record_host" ] ||
                mb_fail "CLI project/host binding differs from the receipt"
            mb_no_links "$mb_project"
        fi
        awk -F '\t' '$1=="file"{print $2 "\t" $3}' "$mb_work/receipt.old" > "$mb_work/old.files"
        awk -F '\t' '$1=="link"{print $2 "\t" $3}' "$mb_work/receipt.old" > "$mb_work/old.links"
        mb_owned
        [ "$mb_operation" != install ] || mb_fail "An installation receipt already exists; use --upgrade"
    else
        [ "$mb_operation" = install ] || mb_fail "No bootstrap installation receipt; cannot $mb_operation"
    fi
}

mb_preflight() (
    while IFS="$mb_tab" read -r mb_pf_name mb_pf_hash; do
        mb_pf_old=$(awk -F '\t' -v n="$mb_pf_name" '$1==n{print $2}' "$mb_work/old.files")
        mb_no_links "$mb_root/$mb_pf_name"
        if [ -z "$mb_pf_old" ] && { [ -e "$mb_root/$mb_pf_name" ] || [ -L "$mb_root/$mb_pf_name" ]; }; then
            mb_fail "Refusing unowned installation collision: $mb_root/$mb_pf_name"
        fi
    done < "$mb_work/new.files"
    while IFS="$mb_tab" read -r mb_pf_name mb_pf_target; do
        mb_pf_old=$(awk -F '\t' -v n="$mb_pf_name" '$1==n{print $2}' "$mb_work/old.links")
        mb_no_links "$mb_bin"
        [ ! -e "$mb_bin" ] || [ -d "$mb_bin" ] || mb_fail "PATH link directory is occupied: $mb_bin"
        if [ -z "$mb_pf_old" ] && { [ -e "$mb_bin/$mb_pf_name" ] || [ -L "$mb_bin/$mb_pf_name" ]; }; then
            mb_fail "Refusing unowned PATH entry: $mb_bin/$mb_pf_name (use --no-path)"
        fi
    done < "$mb_work/new.links"
)

mb_mkdir() (
    mb_md=$1
    mb_no_links "$mb_md"
    : > "$mb_work/directories.pending"
    while [ ! -d "$mb_md" ]; do
        [ ! -e "$mb_md" ] || mb_fail "Directory path is occupied: $mb_md"
        printf '%s\n' "$mb_md" >> "$mb_work/directories.pending"
        mb_md=${mb_md%/*}
        [ -n "$mb_md" ] || mb_md=/
    done
    sort "$mb_work/directories.pending" > "$mb_work/directories.sorted"
    while IFS= read -r mb_md; do
        mkdir "$mb_md" || mb_fail "Cannot create installation directory: $mb_md"
        printf '%s\n' "$mb_md" >> "$mb_work/directories.created"
    done < "$mb_work/directories.sorted"
)

mb_apply_file() {
    mb_af_dest=$1 mb_af_source=$2 mb_af_old=$3 mb_af_new=$4 mb_af_mode=$5
    mb_no_links "$mb_af_dest"
    if [ "$mb_af_old" = - ]; then
        [ ! -e "$mb_af_dest" ] && [ ! -L "$mb_af_dest" ] || mb_fail "New file collision: $mb_af_dest"
    else
        mb_check_file "$mb_af_dest" "$mb_af_old"
    fi
    mb_sequence=$((mb_sequence + 1))
    mb_af_backup=$mb_work/backups/$mb_sequence
    if [ "$mb_af_old" != - ]; then
        cp -p "$mb_af_dest" "$mb_af_backup" || mb_fail "Cannot back up $mb_af_dest"
        mb_check_file "$mb_af_backup" "$mb_af_old"
    fi
    printf 'file\t%s\t%s\t%s\t%s\n' "$mb_af_dest" "$mb_af_old" "$mb_af_new" "$mb_af_backup" \
        > "$mb_work/journal/$mb_sequence"
    printf '%s\n' "$mb_sequence" >> "$mb_work/order"
    if [ "$mb_af_new" = - ]; then
        rm "$mb_af_dest" || mb_fail "Cannot remove owned file: $mb_af_dest"
    else
        mb_mkdir "${mb_af_dest%/*}"
        mb_af_temp=$(mktemp "${mb_af_dest%/*}/.midden-bootstrap.XXXXXXXX") || mb_fail "Cannot stage atomic publication"
        printf '%s\n' "$mb_af_temp" >> "$mb_work/temporaries"
        cp "$mb_af_source" "$mb_af_temp" && chmod "$mb_af_mode" "$mb_af_temp" ||
            mb_fail "Cannot prepare publication: $mb_af_dest"
        mb_check_file "$mb_af_temp" "$mb_af_new"
        if [ "$mb_af_old" = - ]; then
            ln "$mb_af_temp" "$mb_af_dest" || mb_fail "Atomic no-clobber publication failed: $mb_af_dest"
            rm "$mb_af_temp" || mb_fail "Cannot remove publication temporary"
        else
            mb_check_file "$mb_af_dest" "$mb_af_old"
            mv -f "$mb_af_temp" "$mb_af_dest" || mb_fail "Atomic publication failure: $mb_af_dest"
        fi
        mb_check_file "$mb_af_dest" "$mb_af_new"
    fi
}

mb_apply_link() {
    mb_al_name=$1 mb_al_old=$2 mb_al_new=$3
    mb_al_path=$mb_bin/$mb_al_name
    mb_no_links "$mb_bin"
    if [ "$mb_al_old" = - ]; then
        [ ! -e "$mb_al_path" ] && [ ! -L "$mb_al_path" ] || mb_fail "PATH entry appeared: $mb_al_path"
    else
        mb_check_link "$mb_al_path" "$mb_al_old"
    fi
    mb_sequence=$((mb_sequence + 1))
    printf 'link\t%s\t%s\t%s\t-\n' "$mb_al_path" "$mb_al_old" "$mb_al_new" > "$mb_work/journal/$mb_sequence"
    printf '%s\n' "$mb_sequence" >> "$mb_work/order"
    if [ "$mb_al_new" = - ]; then
        rm "$mb_al_path" || mb_fail "Cannot remove owned launch link: $mb_al_path"
    else
        mb_mkdir "$mb_bin"
        ln -s "$mb_al_new" "$mb_al_path" || mb_fail "Cannot create owned launch link: $mb_al_path"
        mb_check_link "$mb_al_path" "$mb_al_new"
    fi
}

mb_rollback() (
    mb_rb_failed=0
    for mb_rb_id in $(sort -rn "$mb_work/order"); do
        IFS="$mb_tab" read -r mb_rb_type mb_rb_path mb_rb_before mb_rb_after mb_rb_backup < "$mb_work/journal/$mb_rb_id"
        if ! mb_no_links "${mb_rb_path%/*}"; then mb_rb_failed=1; continue; fi
        mb_rb_current=-
        if [ "$mb_rb_type" = link ]; then
            if [ -L "$mb_rb_path" ]; then
                mb_rb_current=$(readlink "$mb_rb_path") || { mb_rb_failed=1; continue; }
            elif [ -e "$mb_rb_path" ]; then
                mb_rb_current=unexpected
            fi
        elif [ -L "$mb_rb_path" ]; then
            mb_rb_current=unexpected
        elif [ -f "$mb_rb_path" ]; then
            mb_rb_current=$(mb_hash "$mb_rb_path") || { mb_rb_failed=1; continue; }
        elif [ -e "$mb_rb_path" ]; then
            mb_rb_current=unexpected
        fi
        [ "$mb_rb_current" != "$mb_rb_before" ] || continue
        if [ "$mb_rb_current" != "$mb_rb_after" ]; then
            printf 'midden bootstrap: refusing rollback over a changed path: %s\n' "$mb_rb_path" >&2
            mb_rb_failed=1
            continue
        fi
        if [ "$mb_rb_before" = - ]; then
            rm "$mb_rb_path" || mb_rb_failed=1
        elif [ "$mb_rb_type" = link ]; then
            ln -s "$mb_rb_before" "$mb_rb_path" || mb_rb_failed=1
        else
            if ! mb_check_file "$mb_rb_backup" "$mb_rb_before"; then mb_rb_failed=1; continue; fi
            mb_rb_temp=$(mktemp "${mb_rb_path%/*}/.midden-bootstrap.XXXXXXXX") ||
                { mb_rb_failed=1; continue; }
            printf '%s\n' "$mb_rb_temp" >> "$mb_work/temporaries"
            cp -p "$mb_rb_backup" "$mb_rb_temp" && mv -f "$mb_rb_temp" "$mb_rb_path" ||
                mb_rb_failed=1
        fi
    done
    return "$mb_rb_failed"
)

mb_cleanup() {
    mb_cl_status=$1
    set +e
    if [ -n "$mb_work" ]; then
        if [ "$mb_committed" = no ] && [ -s "$mb_work/order" ]; then
            if ! mb_rollback; then
                printf 'midden bootstrap: recovery required; backups/journal retained at %s; lock retained at %s\n' \
                    "$mb_work" "$mb_lock" >&2
                return 1
            fi
        fi
        while IFS= read -r mb_cl_temp; do
            if [ -e "$mb_cl_temp" ] || [ -L "$mb_cl_temp" ]; then
                rm "$mb_cl_temp" || mb_cl_status=1
            fi
        done < "$mb_work/temporaries"
        if [ "$mb_locked" = yes ]; then
            rmdir "$mb_lock" || mb_cl_status=1
            mb_locked=no
        fi
        if [ "$mb_committed" = no ]; then
            sort -r "$mb_work/directories.created" > "$mb_work/directories.cleanup"
            while IFS= read -r mb_cl_dir; do
                if [ -d "$mb_cl_dir" ] && [ -z "$(ls -A "$mb_cl_dir")" ]; then
                    rmdir "$mb_cl_dir" || mb_cl_status=1
                fi
            done < "$mb_work/directories.cleanup"
        fi
        # Only this private mktemp tree, never an installation/home/state tree.
        find "$mb_work" -depth ! -type d -exec rm -f {} + &&
            find "$mb_work" -depth -type d -exec rmdir {} + || mb_cl_status=1
        mb_work=
    fi
    return "$mb_cl_status"
}

mb_backend() {
    set -- "$mb_python" -I -B "$mb_backend_file" --project "$mb_project" --home-dir "$mb_home" \
        --bin-dir "$mb_root" --host "$mb_host" --distribution-dir "$mb_work/distribution"
    case "$1" in '') mb_fail "Missing Python interpreter" ;; esac
    if [ "$mb_operation" = verify ] || [ "$mb_operation" = uninstall ]; then
        set -- "$@" "--$mb_operation"
    else
        [ "$mb_operation" != upgrade ] || set -- "$@" --upgrade
    fi
    [ "$mb_dry_run" = no ] || set -- "$@" --dry-run
    "$@" </dev/null || mb_fail "Verified Python CLI backend failed; bootstrap changes will be rolled back"
}

midden_bootstrap_main() (
    set -efu
    umask 077
    LC_ALL=C
    export LC_ALL
    unset CDPATH TAR_OPTIONS GZIP
    mb_tab=$(printf '\t')
    mb_metadata_limit=1048576
    mb_archive_limit=268435456
    mb_mode=ui mb_mode_explicit=no mb_requested_version=latest mb_repository=xibodev/midden
    mb_distribution= mb_root_arg= mb_project_explicit=no mb_host_explicit=no
    mb_operation=install mb_host=copilot mb_path_links=yes mb_launch=yes mb_open=yes mb_dry_run=no
    mb_cwd=$(pwd -P) || mb_fail "Cannot resolve the current directory"
    mb_project=$mb_cwd
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --mode|--version|--install-dir|--project|--host|--distribution-dir|--repository)
                [ "$#" -ge 2 ] && [ -n "$2" ] || mb_fail "Missing value for $1"
                case "$2" in -*) mb_fail "Missing value for $1 (received option $2)" ;; esac
                case "$1" in
                    --mode) mb_mode=$2; mb_mode_explicit=yes ;;
                    --version) mb_requested_version=$2 ;;
                    --install-dir) mb_root_arg=$2 ;;
                    --project) mb_project=$2; mb_project_explicit=yes ;;
                    --host) mb_host=$2; mb_host_explicit=yes ;;
                    --distribution-dir) mb_distribution=$2 ;;
                    --repository) mb_repository=$2 ;;
                esac
                shift 2 ;;
            --no-path) mb_path_links=no; shift ;;
            --no-launch) mb_launch=no; shift ;;
            --no-open) mb_open=no; shift ;;
            --dry-run) mb_dry_run=yes; shift ;;
            --upgrade|--verify|--uninstall)
                [ "$mb_operation" = install ] || mb_fail "Choose only one of --upgrade, --verify, --uninstall"
                mb_operation=${1#--}; shift ;;
            --help|-h)
                printf '%s\n' \
                    'Usage: sh install.sh [--mode ui|core|cli] [--version latest|VERSION]' \
                    '  --install-dir DIR       Default: $HOME/.local/lib/midden' \
                    '  --distribution-dir DIR  Use a local verified release; never download' \
                    '  --repository OWNER/NAME Default: xibodev/midden; no ambient authentication' \
                    '  --project DIR --host copilot|claude|agents   CLI backend bindings' \
                    '  --no-path --no-launch --no-open --dry-run' \
                    '  --upgrade | --verify | --uninstall' \
                    'UI/core need native shell tools, not Go/Node/Python/jq. CLI needs Python 3.9+.' \
                    'UI/core install paths must not overlap UI data, MIDDEN_HOME or explicit COMPA_HOME.' \
                    'CLI --verify/--uninstall require --distribution-dir for the reviewed matching release.' \
                    'Checksums provide integrity, not cryptographic authenticity. Trust the download channel.'
                exit 0 ;;
            *) mb_fail "Unknown option: $1" ;;
        esac
    done
    case "$mb_mode" in ui|core|cli) ;; *) mb_fail "Unsupported mode: $mb_mode" ;; esac
    case "$mb_host" in copilot|claude|agents) ;; *) mb_fail "Unsupported host: $mb_host" ;; esac
    printf '%s\n' "$mb_repository" | awk '
        NR!=1 || $0 !~ /^[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9][A-Za-z0-9._-]*$/ {bad=1}
        END {exit bad}
    ' || mb_fail "Repository must be an explicit owner/name, not a URL or path"
    if [ "$mb_requested_version" != latest ]; then
        mb_requested_version=${mb_requested_version#v}
        printf '%s\n' "$mb_requested_version" | awk '
            NR!=1 || $0 !~ /^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.+-]+)?$/ {bad=1}
            END {exit bad}
        ' || mb_fail "Invalid release version"
    fi
    [ -n "${HOME:-}" ] || mb_fail "HOME is required for a per-user installation"
    mb_home=$(mb_path "$HOME")
    mb_root=$(mb_path "${mb_root_arg:-$mb_home/.local/lib/midden}")
    mb_project=$(mb_path "$mb_project")
    mb_bin=$mb_home/.local/bin
    mb_receipt=$mb_root/.midden-bootstrap-receipt.tsv
    mb_lock=$mb_root.midden-bootstrap.lock
    [ "$mb_root" != / ] && [ "$mb_root" != "$mb_home" ] && [ "$mb_root" != "$mb_project" ] ||
        mb_fail "Install into a dedicated directory, not /, HOME or the project root"
    mb_no_links "$mb_home"
    mb_no_links "$mb_root"
    mb_no_links "$mb_project"
    [ -d "$mb_home" ] && [ -d "$mb_project" ] || mb_fail "HOME and project must be existing directories"
    [ ! -e "$mb_root" ] || [ -d "$mb_root" ] || mb_fail "Install directory is not a directory"
    if [ -n "$mb_distribution" ]; then
        mb_distribution=$(mb_path "$mb_distribution")
        mb_no_links "$mb_distribution"
        [ -d "$mb_distribution" ] || mb_fail "--distribution-dir must be an existing directory"
        if mb_under "$mb_root" "$mb_distribution" || mb_under "$mb_distribution" "$mb_root"; then
            mb_fail "Installation and distribution directories must not overlap"
        fi
    fi
    [ ! -e "$mb_lock" ] && [ ! -L "$mb_lock" ] ||
        mb_fail "An installation operation or stale lock exists: $mb_lock"
    mb_system=$(uname -s) || mb_fail "Cannot determine native system"
    mb_machine=$(uname -m) || mb_fail "Cannot determine native architecture"
    case "$mb_system" in Linux) mb_system=linux ;; Darwin) mb_system=darwin ;;
        *) mb_fail "Unsupported native system: $mb_system" ;; esac
    case "$mb_machine" in x86_64|amd64) mb_arch=amd64 ;; arm64|aarch64) mb_arch=arm64 ;;
        *) mb_fail "Unsupported native architecture: $mb_machine" ;; esac
    mb_target=$mb_system/$mb_arch
    for mb_command in awk sort cut cmp head wc cat mkdir mktemp chmod cp mv ln rm rmdir find readlink ls; do
        command -v "$mb_command" >/dev/null 2>&1 || mb_fail "Required native tool missing: $mb_command"
    done
    if command -v sha256sum >/dev/null 2>&1; then mb_hasher=sha256sum
    elif command -v shasum >/dev/null 2>&1; then mb_hasher=shasum
    else mb_fail "SHA-256 verification requires sha256sum or shasum; nothing was installed"; fi
    mb_state_checked=no
    if [ "$mb_operation" = install ] || [ "$mb_mode_explicit" = yes ]; then
        mb_protect_state
        mb_state_checked=yes
    fi
    mb_temp_parent=$(CDPATH= cd -P "${TMPDIR:-/tmp}" && pwd -P) ||
        mb_fail "Temporary directory must already exist"
    mb_temp_parent=$(mb_path "$mb_temp_parent")
    mb_no_links "$mb_temp_parent"
    if mb_under "$mb_temp_parent" "$mb_root" || mb_under "$mb_temp_parent" "$mb_project" ||
        mb_under "$mb_temp_parent" "$mb_bin" ||
        { [ -n "$mb_distribution" ] && mb_under "$mb_temp_parent" "$mb_distribution"; }; then
        mb_fail "Staging requires an external temporary directory; set TMPDIR outside project, release and installation paths"
    fi
    mb_work=$(mktemp -d "$mb_temp_parent/midden-bootstrap.XXXXXXXX") ||
        mb_fail "Cannot create safe external staging directory"
    mb_locked=no mb_committed=no mb_sequence=0
    : > "$mb_work/order"
    : > "$mb_work/temporaries"
    : > "$mb_work/directories.created"
    trap 'mb_exit=$?; trap - 0 HUP INT TERM; mb_cleanup "$mb_exit"; exit $?' 0
    trap 'exit 129' HUP
    trap 'exit 130' INT
    trap 'exit 143' TERM
    mkdir "$mb_work/distribution" "$mb_work/backups" "$mb_work/journal"
    mb_read_receipt
    if [ "$mb_state_checked" = no ]; then mb_protect_state; fi
    mb_cli_lifecycle=no
    if [ "$mb_mode" = cli ]; then
        if [ "$mb_operation" = verify ] || [ "$mb_operation" = uninstall ]; then
            [ -n "$mb_distribution" ] ||
                mb_fail "CLI --$mb_operation requires --distribution-dir with the reviewed matching release"
            mb_cli_lifecycle=yes
            mb_lifecycle_version=$mb_version
        elif [ -z "$mb_old_receipt" ] &&
            { [ -e "$mb_root/midden-install-receipt.json" ] || [ -L "$mb_root/midden-install-receipt.json" ]; }; then
            mb_fail "Refusing an existing CLI backend receipt not owned by this bootstrap"
        fi
        mb_python_select
    fi
    : > "$mb_work/new.files"
    : > "$mb_work/new.links"
    if { [ "$mb_operation" != uninstall ] && [ "$mb_operation" != verify ]; } || [ "$mb_cli_lifecycle" = yes ]; then
        for mb_command in tar gzip; do
            command -v "$mb_command" >/dev/null 2>&1 || mb_fail "Required native tool missing: $mb_command"
        done
        if [ -z "$mb_distribution" ]; then
            command -v curl >/dev/null 2>&1 || mb_fail "Downloading requires curl; nothing was installed"
        fi
        mb_url=https://github.com/$mb_repository/releases/latest/download
        [ "$mb_requested_version" = latest ] ||
            mb_url=https://github.com/$mb_repository/releases/download/v$mb_requested_version
        mb_fetch SHA256SUMS "$mb_metadata_limit"
        mb_checksums
        for mb_metadata in build-manifest.json manifest.tsv; do
            mb_fetch "$mb_metadata" "$mb_metadata_limit"
            mb_checksum "$mb_metadata"
        done
        mb_manifest
        mb_version=$(awk -F '\t' '$1=="version"{print $2}' "$mb_work/distribution/manifest.tsv")
        if [ "$mb_cli_lifecycle" = yes ]; then
            [ "$mb_version" = "$mb_lifecycle_version" ] ||
                mb_fail "Distribution version $mb_version does not match installed CLI version $mb_lifecycle_version"
        fi
        [ "$mb_requested_version" = latest ] || [ "$mb_requested_version" = "$mb_version" ] ||
            mb_fail "Requested version differs from the verified release manifest"
        mb_product=$mb_mode
        [ "$mb_mode" != cli ] || mb_product=core
        mb_archive=$(mb_select "$mb_product" "$mb_target")
        mb_fetch "$mb_archive" "$mb_archive_limit"
        mb_checksum "$mb_archive"
        mb_extract_tar "$mb_product" "$mb_archive" "$mb_work/payload"
        if [ "$mb_mode" = cli ]; then
            mb_bundle_archive=$(mb_select bundle universal)
            mb_fetch "$mb_bundle_archive" "$mb_archive_limit"
            mb_checksum "$mb_bundle_archive"
            mb_extract_zip "$mb_bundle_archive" || mb_fail "Verified bundle extraction failed"
            mb_backend_file=$mb_work/bundle/installer/install.py
            mb_limit "$mb_backend_file" "$mb_metadata_limit"
            if [ "$mb_cli_lifecycle" = no ]; then
                cp "$mb_backend_file" "$mb_work/payload/.midden-bootstrap-cli.py"
                printf '.midden-bootstrap-cli.py\t%s\n' "$(mb_hash "$mb_backend_file")" > "$mb_work/new.files"
            fi
        else
            cp "$mb_work/$mb_product.files" "$mb_work/new.files"
        fi
    fi
    if [ "$mb_operation" = verify ]; then
        if [ "$mb_mode" = cli ]; then mb_backend; fi
        printf 'Verified Midden %s (%s): all receipt-owned files and launch links match.\n' "$mb_version" "$mb_mode"
        mb_committed=yes
        exit 0
    fi
    if [ "$mb_operation" != uninstall ]; then
        cp "$mb_work/old.links" "$mb_work/new.links"
        if [ "$mb_path_links" = yes ]; then
            printf 'midden\t%s/midden\n' "$mb_root" > "$mb_work/new.links"
            [ "$mb_mode" != ui ] || printf 'midden-ui\t%s/midden-ui\n' "$mb_root" >> "$mb_work/new.links"
        fi
        mb_preflight
        {
            printf 'format\tmidden-bootstrap-v1\nmode\t%s\nversion\t%s\ntarget\t%s\n' "$mb_mode" "$mb_version" "$mb_target"
            printf 'root\t%s\nhome\t%s\nproject\t%s\nhost\t%s\n' "$mb_root" "$mb_home" "$mb_project" "$mb_host"
            awk '{print "file\t" $0}' "$mb_work/new.files"
            awk '{print "link\t" $0}' "$mb_work/new.links"
        } > "$mb_work/receipt.new"
        mb_limit "$mb_work/receipt.new" "$mb_metadata_limit"
    fi
    if [ "$mb_dry_run" = yes ]; then
        if [ "$mb_mode" = cli ]; then mb_backend; fi
        printf 'Dry-run: %s Midden %s (%s, %s) in %s\n' "$mb_operation" "$mb_version" "$mb_mode" "$mb_target" "$mb_root"
        printf 'No installed files, PATH links or profiles changed; core/UI executables were not run.\n'
        printf 'Checksums establish integrity, not cryptographic authenticity.\n'
        mb_committed=yes
        exit 0
    fi
    mb_mkdir "${mb_root%/*}"
    mkdir "$mb_lock" || mb_fail "Cannot acquire installation lock: $mb_lock"
    mb_locked=yes
    mb_owned
    mb_preflight
    if [ "$mb_operation" != uninstall ] && [ "$mb_mode" != cli ]; then
        chmod 755 "$mb_work/payload/midden"
        mb_probe=$("$mb_work/payload/midden" version </dev/null 2>"$mb_work/probe.stderr") ||
            mb_fail "Verified core version probe failed"
        [ "$mb_probe" = "midden $mb_version" ] && [ ! -s "$mb_work/probe.stderr" ] ||
            mb_fail "Core version probe does not match the verified manifest"
        if [ "$mb_mode" = ui ]; then
            chmod 755 "$mb_work/payload/midden-ui"
            mb_probe=$("$mb_work/payload/midden-ui" --version </dev/null 2>"$mb_work/probe.stderr") ||
                mb_fail "Verified UI version probe failed"
            [ "$mb_probe" = "midden-ui $mb_version" ] && [ ! -s "$mb_work/probe.stderr" ] ||
                mb_fail "UI version probe does not match the verified manifest"
        fi
    fi
    while IFS="$mb_tab" read -r mb_name mb_digest; do
        mb_old=$(awk -F '\t' -v n="$mb_name" '$1==n{print $2}' "$mb_work/old.files")
        mb_permissions=644
        case "$mb_name" in midden|midden-ui|start.sh) mb_permissions=755 ;; esac
        mb_apply_file "$mb_root/$mb_name" "$mb_work/payload/$mb_name" "${mb_old:--}" "$mb_digest" "$mb_permissions"
    done < "$mb_work/new.files"
    while IFS="$mb_tab" read -r mb_name mb_digest; do
        if ! awk -F '\t' -v n="$mb_name" '$1==n{found=1}END{exit (!found)}' "$mb_work/new.files"; then
            mb_apply_file "$mb_root/$mb_name" - "$mb_digest" - 644
        fi
    done < "$mb_work/old.files"
    while IFS="$mb_tab" read -r mb_name mb_destination; do
        if ! awk -F '\t' -v n="$mb_name" '$1==n{found=1}END{exit (!found)}' "$mb_work/old.links"; then
            mb_apply_link "$mb_name" - "$mb_destination"
        fi
    done < "$mb_work/new.links"
    while IFS="$mb_tab" read -r mb_name mb_destination; do
        if ! awk -F '\t' -v n="$mb_name" '$1==n{found=1}END{exit (!found)}' "$mb_work/new.links"; then
            mb_apply_link "$mb_name" "$mb_destination" -
        fi
    done < "$mb_work/old.links"
    if [ "$mb_operation" = uninstall ]; then
        mb_apply_file "$mb_receipt" - "$mb_old_receipt" - 644
    else
        mb_apply_file "$mb_receipt" "$mb_work/receipt.new" "${mb_old_receipt:--}" "$(mb_hash "$mb_work/receipt.new")" 644
    fi
    # Publish bootstrap files first: the backend has its own rollback transaction.
    # A backend failure rolls these changes back as well.
    if [ "$mb_mode" = cli ]; then mb_backend; fi
    mb_committed=yes
    if [ "$mb_operation" = uninstall ]; then
        printf 'Removed unchanged receipt-owned Midden files and launch links; state and unowned files were preserved.\n'
        exit 0
    fi
    printf 'Installed Midden %s (%s, %s) in %s\n' "$mb_version" "$mb_mode" "$mb_target" "$mb_root"
    printf 'Checksums establish integrity, not cryptographic authenticity; trust the release channel.\n'
    printf 'Core executable: %s/midden\n' "$mb_root"
    if [ -s "$mb_work/new.links" ]; then
        printf 'Owned launch links: %s (no PATH or shell profile was changed)\n' "$mb_bin"
    fi
    if [ "$mb_mode" = ui ] && [ "$mb_launch" = yes ]; then
        mb_cleanup 0 || exit 1
        trap - 0 HUP INT TERM
        if [ "$mb_open" = no ]; then
            exec "$mb_root/midden-ui" --no-open </dev/null
        else
            exec "$mb_root/midden-ui" </dev/null
        fi
    fi
)

midden_bootstrap_main "$@"

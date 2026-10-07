#!/bin/sh
# Standalone POSIX entry: definitions are consumed before anything reads stdin.
# Installs, updates, verifies and removes Midden: Core, the Bundle and the App.
# One installer and one receipt for all three products; no Python, Go or Node.

mb_fail() {
    printf 'midden install: %s\n' "$*" >&2
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
    case "$mb_system" in
        darwin) mb_ps_app="$mb_home/Library/Application Support/Midden" ;;
        linux) mb_ps_app=${XDG_DATA_HOME:-$mb_home/.local/share}/midden ;;
    esac
    mb_state_disjoint "$mb_ps_app" "App data"
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

# Fetch $2 (an asset name, or a full URL for the pinned Pandoc) into $mb_work/distribution/$1.
mb_fetch_as() (
    mb_fa_name=$1 mb_fa_url=$2 mb_fa_limit=$3
    if [ -n "$mb_distribution" ]; then
        [ -e "$mb_distribution/$mb_fa_name" ] || [ -L "$mb_distribution/$mb_fa_name" ] ||
            mb_fail "--distribution-dir needs $mb_fa_name; download it from $mb_fa_url"
        mb_copy "$mb_distribution/$mb_fa_name" "$mb_work/distribution/$mb_fa_name" "$mb_fa_limit"
    else
        # -q must be first: no ~/.curlrc, netrc, token lookup, or auth fallback.
        (
            set +e
            curl -q -fsSL --proto '=https' --proto-redir '=https' \
                --connect-timeout 15 --max-time 600 --max-filesize "$mb_fa_limit" "$mb_fa_url"
            printf '%s\n' "$?" > "$mb_work/download.status"
        ) | head -c "$((mb_fa_limit + 1))" > "$mb_work/distribution/$mb_fa_name"
        mb_limit "$mb_work/distribution/$mb_fa_name" "$mb_fa_limit"
        [ -f "$mb_work/download.status" ] &&
            [ "$(cat "$mb_work/download.status")" = 0 ] ||
            mb_fail "Download failed: $mb_fa_url (no fallback was attempted)"
    fi
)

mb_fetch() {
    mb_fetch_as "$1" "$mb_url/$1" "$2"
}

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
        function product(p) {return (p=="core" || p=="app" || p=="bundle")}
        function target(p,t) {
            if(p=="bundle") return (t=="universal")
            return (t ~ /^(linux|darwin|windows)\/(amd64|arm64)$/)
        }
        function layout(p,s) {
            if(p=="core") return (s=="midden" || s=="midden.exe" || s=="LICENSE" || s=="THIRD_PARTY_NOTICES.txt")
            if(p=="app") return (s=="midden-ui" || s=="midden-ui.exe" || (s ~ /^app\// && s !~ /^app\/tools\//))
            return (s ~ /^skills\/midden-(investigation|article|presentation|long-form|shared)\/./)
        }
        NR==1 {if($0!="format\tmidden-release-v2") reject()}
        $1=="format" {if(NF!=2 || $2!="midden-release-v2" || format++) reject(); next}
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
        $1=="fetch" {
            asset=$4
            if(NF!=5 || $2!="pandoc" || $3 !~ /^(linux|darwin|windows)\/(amd64|arm64)$/ || fetched[$3]++ ||
                substr(asset,1,48)!="https://github.com/jgm/pandoc/releases/download/" ||
                substr(asset,49) !~ /^[0-9A-Za-z.]+\/[A-Za-z0-9._-]+$/ ||
                length($5)!=64 || $5 ~ /[^0-9a-fA-F]/) reject()
            next
        }
        $1=="take" {
            if(NF!=5 || $2!="pandoc" || !fetched[$3] || !path($4) || !path($5) ||
                $5 !~ /^app\/tools\/[^\/]+$/ || taken[$3 SUBSEP $4]++ || destination[$3 SUBSEP tolower($5)]++) reject()
            takes[$3]++
            next
        }
        {reject()}
        END {
            if(bad) exit 1
            if(format!=1 || commit!=1 || version=="") exit 1
            for(t in fetched) if(!takes[t]) exit 1
            for(key in archives) {
                t=targets[key]; p=products[key]; gsub(/\//,"_",t)
                suffix=".tar.gz"
                if(targets[key] ~ /^windows\//) suffix=".zip"
                if(p=="bundle") expected="midden-bundle_" version ".zip"
                else expected="midden-" p "_" version "_" t suffix
                if(archives[key]!=expected || !counts[key]) exit 1
                extension=""
                if(targets[key] ~ /^windows\//) extension=".exe"
                if(p=="core" && (!files[key SUBSEP "midden" extension] || !files[key SUBSEP "LICENSE"])) exit 1
                if(p=="app" && (!files[key SUBSEP "midden-ui" extension] ||
                    !files[key SUBSEP "app/LICENSE"] || !files[key SUBSEP "app/NOTICE"])) exit 1
                if(p=="bundle" && (!files[key SUBSEP "skills/midden-investigation/SKILL.md"] ||
                    !files[key SUBSEP "skills/midden-article/SKILL.md"] ||
                    !files[key SUBSEP "skills/midden-presentation/SKILL.md"] ||
                    !files[key SUBSEP "skills/midden-long-form/SKILL.md"] ||
                    !files[key SUBSEP "skills/midden-shared/sources.md"])) exit 1
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

# The bundle is one ZIP for every platform. unzip -p only writes to stdout, and
# every member must be exactly a listed name whose bytes match its checksum, so
# links, directories and extra or duplicate members cannot pass.
mb_extract_zip() (
    mb_xz_product=$1
    mb_xz_archive=$mb_work/distribution/$2
    mb_xz_dest=$3
    unzip -Z1 "$mb_xz_archive" > "$mb_work/zip.names" 2>"$mb_work/zip.errors" ||
        mb_fail "Invalid ZIP inventory: $2"
    [ "$(wc -l < "$mb_work/zip.names")" -le 4096 ] || mb_fail "Archive member limit exceeded: $2"
    sort "$mb_work/zip.names" > "$mb_work/zip.sorted"
    cut -f 1 "$mb_work/$mb_xz_product.files" | sort > "$mb_work/zip.expected"
    cmp -s "$mb_work/zip.sorted" "$mb_work/zip.expected" ||
        mb_fail "Archive members differ from the exact canonical file allowlist (unsafe, extra or duplicate member)"
    mkdir "$mb_xz_dest" || mb_fail "Cannot create private extraction directory"
    mb_xz_total=0
    while IFS="$mb_tab" read -r mb_xz_name mb_xz_hash; do
        mb_xz_file=$mb_xz_dest/$mb_xz_name
        mkdir -p "${mb_xz_file%/*}" || mb_fail "Cannot create private member directory"
        (
            set +e
            unzip -p "$mb_xz_archive" "$mb_xz_name"
            printf '%s\n' "$?" > "$mb_work/unzip.status"
        ) | head -c "$((mb_archive_limit - mb_xz_total + 1))" > "$mb_xz_file"
        mb_xz_total=$((mb_xz_total + $(wc -c < "$mb_xz_file")))
        [ "$mb_xz_total" -le "$mb_archive_limit" ] || mb_fail "Expanded archive size limit exceeded"
        [ -f "$mb_work/unzip.status" ] && [ "$(cat "$mb_work/unzip.status")" = 0 ] ||
            mb_fail "Archive member extraction failed: $mb_xz_name"
        [ "$(mb_hash "$mb_xz_file")" = "$mb_xz_hash" ] || mb_fail "File checksum mismatch: $mb_xz_name"
        chmod 644 "$mb_xz_file" || mb_fail "Cannot set private member permissions"
    done < "$mb_work/$mb_xz_product.files"
)

# Extract the pinned Pandoc members ($mb_work/pandoc.take: member TAB destination)
# from its verified archive into $mb_work/pandoc/<destination>.
mb_extract_pandoc() (
    mb_xp_archive=$mb_work/distribution/$1
    case "$1" in
        *.tar.gz)
            (
                set +e
                gzip -dc "$mb_xp_archive"
                printf '%s\n' "$?" > "$mb_work/gzip.status"
            ) | head -c "$((mb_tool_limit + 1))" > "$mb_work/pandoc.tar"
            mb_limit "$mb_work/pandoc.tar" "$mb_tool_limit"
            [ "$(cat "$mb_work/gzip.status")" = 0 ] || mb_fail "Invalid compressed Pandoc archive"
            ;;
        *.zip) ;;
        *) mb_fail "Unsupported Pandoc archive: $1" ;;
    esac
    while IFS="$mb_tab" read -r mb_xp_member mb_xp_destination; do
        mb_xp_file=$mb_work/pandoc/$mb_xp_destination
        mkdir -p "${mb_xp_file%/*}" || mb_fail "Cannot create private Pandoc directory"
        (
            set +e
            case "$1" in
                *.tar.gz) tar -xOP -f "$mb_work/pandoc.tar" "$mb_xp_member" ;;
                *) unzip -p "$mb_xp_archive" "$mb_xp_member" ;;
            esac
            printf '%s\n' "$?" > "$mb_work/pandoc.status"
        ) | head -c "$((mb_tool_limit + 1))" > "$mb_xp_file"
        mb_limit "$mb_xp_file" "$mb_tool_limit"
        [ "$(cat "$mb_work/pandoc.status")" = 0 ] && [ -s "$mb_xp_file" ] ||
            mb_fail "Pandoc archive lacks $mb_xp_member"
    done < "$mb_work/pandoc.take"
)

mb_check_file() (
    mb_no_links "$1"
    [ -f "$1" ] || mb_fail "Preserving a modified or missing owned file: $1"
    [ "$(mb_hash "$1")" = "$2" ] || mb_fail "Preserving a modified or missing owned file: $1"
)

mb_same_file() {
    [ ! -L "$1" ] && [ -f "$1" ] && [ "$(mb_hash "$1")" = "$2" ]
}

mb_check_link() (
    mb_no_links "${1%/*}"
    [ -L "$1" ] && [ "$(readlink "$1")" = "$2" ] || mb_fail "Modified or missing owned launch link: $1"
)

# Read the receipt into $mb_work/owned.{meta,products,places,files,links,fetched}.
# An installation by the 0.3 installer (.midden-bootstrap-receipt.tsv) is read
# into the same shape, so -Upgrade can replace it.
mb_read_receipt() {
    for mb_rr_part in meta products places files links fetched; do : > "$mb_work/owned.$mb_rr_part"; done
    mb_owned=none mb_old_receipt= mb_legacy_receipt=
    [ ! -e "$mb_root/midden-install-receipt.json" ] && [ ! -L "$mb_root/midden-install-receipt.json" ] ||
        mb_fail "This folder holds a Midden CLI installation made by the 0.3 installer (midden-install-receipt.json). Remove it with that release's install.sh (--mode cli --uninstall --distribution-dir <that release>), then run this again."
    if [ -e "$mb_root/.midden-bootstrap-receipt.tsv" ] || [ -L "$mb_root/.midden-bootstrap-receipt.tsv" ]; then
        [ ! -e "$mb_receipt" ] || mb_fail "Two installation receipts exist; inspect the installation before retrying"
        mb_legacy_receipt=$mb_root/.midden-bootstrap-receipt.tsv
        mb_copy "$mb_legacy_receipt" "$mb_work/receipt.legacy" "$mb_metadata_limit"
        awk -F '\t' -v root="$mb_root" '
            function bad() {failed=1; exit 1}
            $1=="file" {
                if(NF!=3 || $2 !~ /^[A-Za-z0-9.][A-Za-z0-9._+\/-]*$/ ||
                    $2 ~ /(^|\/)\.\.?($|\/)/ || $2 ~ /\/\// || $2 ~ /\/$/ ||
                    length($3)!=64 || $3 ~ /[^0-9a-f]/ || files[tolower($2)]++) bad()
                count++; next
            }
            $1=="link" {
                if(NF!=3 || ($2!="midden" && $2!="midden-ui") || links[$2]++ || $3!=root "/" $2) bad()
                next
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
                    meta["target"] !~ /^(linux|darwin)\/(amd64|arm64)$/ || meta["root"]!=root ||
                    meta["home"] !~ /^\// || count<1 || count>4096) exit 1
            }
        ' "$mb_work/receipt.legacy" || mb_fail "The 0.3 installer's receipt does not describe this installation"
        [ "$(awk -F '\t' '$1=="mode"{print $2}' "$mb_work/receipt.legacy")" != cli ] ||
            mb_fail "This folder holds a Midden CLI installation made by the 0.3 installer. Remove it with that release's install.sh (--mode cli --uninstall --distribution-dir <that release>), then run this again."
        mb_old_receipt=$(mb_hash "$mb_work/receipt.legacy")
        mb_owned=legacy
        awk -F '\t' '$1=="version" || $1=="target" {print}' "$mb_work/receipt.legacy" > "$mb_work/owned.meta"
        printf 'commit\t%s\nrepository\t%s\n' "$(printf '%040d' 0)" "$mb_repository" >> "$mb_work/owned.meta"
        if [ "$(awk -F '\t' '$1=="mode"{print $2}' "$mb_work/receipt.legacy")" = ui ]; then
            printf 'core\nbundle\napp\n' > "$mb_work/owned.products"
        else
            printf 'core\n' > "$mb_work/owned.products"
        fi
        awk -F '\t' '$1=="file"{print "root\t" $2 "\t" $3}' "$mb_work/receipt.legacy" > "$mb_work/owned.files"
        mb_rr_home=$(awk -F '\t' '$1=="home"{print $2}' "$mb_work/receipt.legacy")
        awk -F '\t' -v bin="$mb_rr_home/.local/bin" '$1=="link"{print bin "/" $2 "\t" $3}' \
            "$mb_work/receipt.legacy" > "$mb_work/owned.links"
        return
    fi
    if [ ! -e "$mb_receipt" ] && [ ! -L "$mb_receipt" ]; then return; fi
    mb_copy "$mb_receipt" "$mb_work/receipt.old" "$mb_metadata_limit"
    awk -F '\t' -v root="$mb_root" '
        function bad() {failed=1; exit 1}
        function path(s, a,n,i) {
            if (s !~ /^[A-Za-z0-9][A-Za-z0-9._+\/-]*$/) return 0
            n=split(s,a,"/")
            for(i=1;i<=n;i++) if(a[i]=="" || a[i]=="." || a[i]=="..") return 0
            return 1
        }
        $1 ~ /^(format|version|commit|repository|target|root)$/ {
            if(NF!=2 || ($1 in meta)) bad()
            meta[$1]=$2; next
        }
        $1=="product" {if(NF!=2 || $2 !~ /^(core|bundle|app)$/ || products[$2]++) bad(); next}
        $1=="fetched" {
            if(NF!=4 || $2!="pandoc" || fetched++ ||
                substr($3,1,48)!="https://github.com/jgm/pandoc/releases/download/" ||
                length($4)!=64 || $4 ~ /[^0-9a-f]/) bad()
            next
        }
        $1=="place" {
            if(NF!=7 || $2 !~ /^[1-9][0-9]*$/ || length($2)>6 || ($2 in kinds) || $7 !~ /^\// || dirs[$7]++ ||
                $6 !~ /^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.+-]+)?$/) bad()
            if($3=="skills") {if($4 !~ /^(copilot|claude|agents)$/ || $5 !~ /^(user|project)$/) bad()}
            else if($3=="start") {if($4!="-" || $5!="-") bad()}
            else bad()
            kinds[$2]=$3; next
        }
        $1=="file" {
            if(NF!=4 || ($2!="root" && !($2 in kinds)) || !path($3) ||
                length($4)!=64 || $4 ~ /[^0-9a-f]/ || seen[$2 SUBSEP tolower($3)]++) bad()
            count[$2]++; bases[++entries]=$2; names[entries]=tolower($3); next
        }
        $1=="link" {
            name=$2; sub(/^.*\//,"",name)
            if(NF!=3 || $2 !~ /^\// || (name!="midden" && name!="midden-ui") || links[$2]++ || $3!=root "/" name) bad()
            next
        }
        {bad()}
        END {
            if(failed) exit 1
            if(meta["format"]!="midden-install-v1" ||
                meta["version"] !~ /^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.+-]+)?$/ ||
                meta["commit"] !~ /^[0-9a-f]+$/ || (length(meta["commit"])!=40 && length(meta["commit"])!=64) ||
                meta["target"] !~ /^(linux|darwin)\/(amd64|arm64)$/ || meta["root"]!=root ||
                !products["core"] || !count["root"]) exit 1
            for(id in kinds) if(!count[id]) exit 1
            for(i=1;i<=entries;i++) {
                parent=names[i]
                while(sub(/\/[^\/]+$/,"",parent)) if((bases[i] SUBSEP parent) in seen) exit 1
            }
        }
    ' "$mb_work/receipt.old" || mb_fail "Invalid or relocated installation receipt; refusing its ownership"
    mb_old_receipt=$(mb_hash "$mb_work/receipt.old")
    mb_owned=current
    awk -F '\t' '$1 ~ /^(version|commit|repository|target)$/ {print}' "$mb_work/receipt.old" > "$mb_work/owned.meta"
    awk -F '\t' '$1=="product"{print $2}' "$mb_work/receipt.old" > "$mb_work/owned.products"
    awk -F '\t' '$1=="place"{print $2 "\t" $3 "\t" $4 "\t" $5 "\t" $6 "\t" $7}' "$mb_work/receipt.old" > "$mb_work/owned.places"
    awk -F '\t' '$1=="file"{print $2 "\t" $3 "\t" $4}' "$mb_work/receipt.old" > "$mb_work/owned.files"
    awk -F '\t' '$1=="link"{print $2 "\t" $3}' "$mb_work/receipt.old" > "$mb_work/owned.links"
    awk -F '\t' '$1=="fetched"{print $3 "\t" $4}' "$mb_work/receipt.old" > "$mb_work/owned.fetched"
    while IFS="$mb_tab" read -r mb_rr_id mb_rr_kind mb_rr_harness mb_rr_scope mb_rr_version mb_rr_dir; do
        [ "$(mb_path "$mb_rr_dir")" = "$mb_rr_dir" ] || mb_fail "Invalid receipt place folder: $mb_rr_dir"
    done < "$mb_work/owned.places"
}

mb_owned_meta() {
    awk -F '\t' -v key="$1" '$1==key {print $2}' "$mb_work/owned.meta"
}

mb_has_line() {
    awk -v value="$2" '$0==value {found=1} END {exit (!found)}' "$1"
}

mb_skill_dir() (
    case "$2:$1" in
        user:copilot) mb_sk=$mb_home/.copilot/skills ;;
        user:claude) mb_sk=$mb_home/.claude/skills ;;
        user:agents) mb_sk=$mb_home/.agents/skills ;;
        project:copilot) mb_sk=$mb_project/.github/skills ;;
        project:claude) mb_sk=$mb_project/.claude/skills ;;
        project:agents) mb_sk=$mb_project/.agents/skills ;;
        *) mb_fail "Unknown harness: $1" ;;
    esac
    if mb_under "$mb_sk" "$mb_root" || mb_under "$mb_root" "$mb_sk"; then
        mb_fail "A skills folder cannot overlap the installation folder: $mb_sk"
    fi
    mb_no_links "$mb_sk"
    printf '%s\n' "$mb_sk"
)

mb_start_dir() (
    case "$mb_system" in
        darwin) mb_path "$mb_home/Applications" ;;
        *) mb_path "${XDG_DATA_HOME:-$mb_home/.local/share}/applications" ;;
    esac
)

# Write the Start entry for this platform into $1 and list its files, TAB mode, in $2.
mb_write_start() (
    mkdir -p "$1" || mb_fail "Cannot stage the Start entry"
    case "$mb_system" in
        darwin)
            mkdir -p "$1/Midden.app/Contents/MacOS" || mb_fail "Cannot stage the Start entry"
            mb_ws_quoted=$(printf '%s\n' "$mb_root/midden-ui" | awk -v q="'" '{
                out = ""
                for (i = 1; i <= length($0); i++) {
                    c = substr($0, i, 1)
                    if (c == q) out = out q "\\" q q
                    else out = out c
                }
                print out
            }')
            {
                printf '#!/bin/sh\n'
                printf "exec '%s' \"\$@\"\n" "$mb_ws_quoted"
            } > "$1/Midden.app/Contents/MacOS/Midden"
            {
                printf '<?xml version="1.0" encoding="UTF-8"?>\n'
                printf '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">\n'
                printf '<plist version="1.0">\n<dict>\n'
                printf '  <key>CFBundleExecutable</key><string>Midden</string>\n'
                printf '  <key>CFBundleIdentifier</key><string>com.xibodev.midden</string>\n'
                printf '  <key>CFBundleName</key><string>Midden</string>\n'
                printf '  <key>CFBundlePackageType</key><string>APPL</string>\n'
                printf '  <key>CFBundleShortVersionString</key><string>%s</string>\n' "$mb_version"
                printf '</dict>\n</plist>\n'
            } > "$1/Midden.app/Contents/Info.plist"
            printf 'Midden.app/Contents/Info.plist\t644\nMidden.app/Contents/MacOS/Midden\t755\n' > "$2"
            ;;
        *)
            mb_ws_exec=$(printf '%s\n' "$mb_root/midden-ui" | awk '{
                out = ""
                for (i = 1; i <= length($0); i++) {
                    c = substr($0, i, 1)
                    if (c == "\"" || c == "`" || c == "$") out = out "\\" c
                    else if (c == "%") out = out "%%"
                    else out = out c
                }
                print out
            }')
            {
                printf '[Desktop Entry]\nType=Application\nName=Midden\n'
                printf 'Comment=Investigate your recorded AI sessions\n'
                printf 'Exec="%s"\nTerminal=false\nCategories=Development;\n' "$mb_ws_exec"
            } > "$1/midden.desktop"
            printf 'midden.desktop\t644\n' > "$2"
            ;;
    esac
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
        [ ! -e "$mb_af_dest" ] && [ ! -L "$mb_af_dest" ] ||
            mb_fail "Refusing to overwrite a file Midden does not own: $mb_af_dest"
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
        mb_af_temp=$(mktemp "${mb_af_dest%/*}/.midden-install.XXXXXXXX") || mb_fail "Cannot stage atomic publication"
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
    mb_al_path=$1 mb_al_old=$2 mb_al_new=$3
    mb_no_links "${mb_al_path%/*}"
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
        mb_mkdir "${mb_al_path%/*}"
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
            printf 'midden install: refusing rollback over a changed path: %s\n' "$mb_rb_path" >&2
            mb_rb_failed=1
            continue
        fi
        if [ "$mb_rb_before" = - ]; then
            rm "$mb_rb_path" || mb_rb_failed=1
        elif [ "$mb_rb_type" = link ]; then
            ln -s "$mb_rb_before" "$mb_rb_path" || mb_rb_failed=1
        else
            if ! mb_check_file "$mb_rb_backup" "$mb_rb_before"; then mb_rb_failed=1; continue; fi
            mkdir -p "${mb_rb_path%/*}" || { mb_rb_failed=1; continue; }
            mb_rb_temp=$(mktemp "${mb_rb_path%/*}/.midden-install.XXXXXXXX") ||
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
                printf 'midden install: recovery required; backups/journal retained at %s; lock retained at %s\n' \
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

# Remove folders a removal left empty, up to but not including $2.
mb_prune() (
    mb_pr_dir=${1%/*}
    while [ "$mb_pr_dir" != "$2" ] && mb_under "$mb_pr_dir" "$2"; do
        [ -d "$mb_pr_dir" ] && [ ! -L "$mb_pr_dir" ] && [ -z "$(ls -A "$mb_pr_dir")" ] || break
        rmdir "$mb_pr_dir" || break
        mb_pr_dir=${mb_pr_dir%/*}
    done
)

# Plan lines: file TAB path TAB old TAB new TAB source TAB mode TAB base, or
# link TAB path TAB old TAB new; "-" means absent.
mb_plan_file() {
    printf 'file\t%s\t%s\t%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$4" "$5" "$6" >> "$mb_work/plan"
}

mb_note() {
    printf '%s\n' "$*" >> "$mb_work/notes"
}

mb_mode_of() {
    case "$1" in midden|midden-ui|app/tools/pandoc|Midden.app/Contents/MacOS/Midden) printf '755\n' ;; *) printf '644\n' ;; esac
}

# Plan removals for one owned place; edited files are kept and noted.
mb_plan_place_removal() {
    mb_pr_id=$1 mb_pr_dir=$2
    awk -F '\t' -v id="$mb_pr_id" '$1==id {print $2 "\t" $3}' "$mb_work/owned.files" > "$mb_work/place.files"
    while IFS="$mb_tab" read -r mb_pr_rel mb_pr_hash; do
        if mb_same_file "$mb_pr_dir/$mb_pr_rel" "$mb_pr_hash"; then
            mb_plan_file "$mb_pr_dir/$mb_pr_rel" "$mb_pr_hash" - - 644 "$mb_pr_dir"
        elif [ -e "$mb_pr_dir/$mb_pr_rel" ] || [ -L "$mb_pr_dir/$mb_pr_rel" ]; then
            mb_note "Kept $mb_pr_dir/$mb_pr_rel: it was changed since installation"
        fi
    done < "$mb_work/place.files"
}

# Plan writing the release's skills into a place; $3 is the owned place id or "-".
mb_plan_place_write() {
    mb_pw_id=$1 mb_pw_dir=$2 mb_pw_old=$3
    : > "$mb_work/place.old"
    [ "$mb_pw_old" = - ] || awk -F '\t' -v id="$mb_pw_old" '$1==id {print $2 "\t" $3}' "$mb_work/owned.files" > "$mb_work/place.old"
    while IFS="$mb_tab" read -r mb_pw_rel mb_pw_hash mb_pw_source; do
        mb_pw_previous=$(awk -F '\t' -v n="$mb_pw_rel" '$1==n {print $2}' "$mb_work/place.old")
        printf '%s\t%s\t%s\n' "$mb_pw_id" "$mb_pw_rel" "$mb_pw_hash" >> "$mb_work/new.files"
        [ "$mb_pw_previous" != "$mb_pw_hash" ] || continue
        mb_plan_file "$mb_pw_dir/$mb_pw_rel" "${mb_pw_previous:--}" "$mb_pw_hash" "$mb_pw_source" 644 "$mb_pw_dir"
    done < "$mb_work/skill.files"
    while IFS="$mb_tab" read -r mb_pw_rel mb_pw_hash; do
        if ! awk -F '\t' -v n="$mb_pw_rel" '$1==n {found=1} END {exit (!found)}' "$mb_work/skill.files"; then
            mb_plan_file "$mb_pw_dir/$mb_pw_rel" "$mb_pw_hash" - - 644 "$mb_pw_dir"
        fi
    done < "$mb_work/place.old"
}

mb_place_unchanged() {
    awk -F '\t' -v id="$1" '$1==id {print $2 "\t" $3}' "$mb_work/owned.files" > "$mb_work/place.check"
    while IFS="$mb_tab" read -r mb_pu_rel mb_pu_hash; do
        mb_same_file "$2/$mb_pu_rel" "$mb_pu_hash" || return 1
    done < "$mb_work/place.check"
    return 0
}

mb_write_receipt() {
    {
        printf 'format\tmidden-install-v1\nversion\t%s\ncommit\t%s\nrepository\t%s\ntarget\t%s\nroot\t%s\n' \
            "$mb_version" "$mb_commit" "$mb_repository" "$mb_target" "$mb_root"
        for mb_wr_product in core bundle app; do
            if mb_has_line "$mb_work/new.products" "$mb_wr_product"; then printf 'product\t%s\n' "$mb_wr_product"; fi
        done
        awk -F '\t' '{print "fetched\tpandoc\t" $1 "\t" $2}' "$mb_work/new.fetched"
        awk '{print "place\t" $0}' "$mb_work/new.places"
        awk -F '\t' '$1=="root" {print "file\t" $0}' "$mb_work/new.files" | sort
        awk -F '\t' '$1!="root" {print "file\t" $0}' "$mb_work/new.files"
        awk '{print "link\t" $0}' "$mb_work/new.links"
    } > "$mb_work/receipt.new"
    mb_limit "$mb_work/receipt.new" "$mb_metadata_limit"
}

midden_install_main() (
    set -efu
    umask 077
    LC_ALL=C
    export LC_ALL
    unset CDPATH TAR_OPTIONS GZIP UNZIP ZIPINFO
    mb_tab=$(printf '\t')
    mb_metadata_limit=1048576
    mb_archive_limit=268435456
    mb_tool_limit=536870912
    mb_mode= mb_requested_version=latest mb_repository=xibodev/midden
    mb_distribution= mb_root_arg= mb_project= mb_harness_args=
    mb_operation=install mb_path_links=yes mb_launch=yes mb_open=yes mb_dry_run=no
    mb_cwd=$(pwd -P) || mb_fail "Cannot resolve the current directory"
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --mode|--version|--install-dir|--project|--harness|--distribution-dir|--repository)
                [ "$#" -ge 2 ] && [ -n "$2" ] || mb_fail "Missing value for $1"
                case "$2" in -*) mb_fail "Missing value for $1 (received option $2)" ;; esac
                case "$1" in
                    --mode) mb_mode=$2 ;;
                    --version) mb_requested_version=$2 ;;
                    --install-dir) mb_root_arg=$2 ;;
                    --project) mb_project=$2 ;;
                    --harness) mb_harness_args="$mb_harness_args,$2" ;;
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
                    'Usage: sh install.sh [--mode app|core|bundle] [--harness NAME] [--project DIR] [--version latest|VERSION]' \
                    '  --mode app (default)    Core, the skills and the App, with a Start entry and its own Pandoc' \
                    '  --mode core             Core only' \
                    '  --mode bundle           Core, and the skills in each --harness copilot|claude|agents folder' \
                    '  --project DIR           Put the skills in this project instead of the person'\''s folders' \
                    '  --install-dir DIR       Default: $HOME/.local/lib/midden' \
                    '  --distribution-dir DIR  Use a local verified release; never download' \
                    '  --repository OWNER/NAME Default: xibodev/midden; no ambient authentication' \
                    '  --no-path --no-launch --no-open --dry-run' \
                    '  --upgrade | --verify | --uninstall   (--uninstall --harness NAME removes only those skills)' \
                    'Needs only native tools: curl, tar, gzip, unzip and sha256sum or shasum. No Python, Go or Node.' \
                    'Install paths must not overlap App data, MIDDEN_HOME or an explicit COMPA_HOME.' \
                    'Checksums provide integrity, not cryptographic authenticity. Trust the download channel.'
                exit 0 ;;
            *) mb_fail "Unknown option: $1" ;;
        esac
    done
    case "$mb_mode" in ''|app|core|bundle) ;; *) mb_fail "Unsupported mode: $mb_mode (use app, core or bundle)" ;; esac
    mb_harnesses=$(printf '%s\n' "$mb_harness_args" | awk -F ',' '{
        for (i = 1; i <= NF; i++) { gsub(/^ +| +$/, "", $i); if ($i != "" && !seen[$i]++) out = out (out == "" ? "" : " ") $i }
        print out
    }')
    for mb_name in $mb_harnesses; do
        case "$mb_name" in copilot|claude|agents) ;; *) mb_fail "Unknown harness '$mb_name'; use copilot, claude or agents" ;; esac
    done
    case "$mb_operation" in
        install|upgrade)
            [ "$mb_mode" != bundle ] || [ -n "$mb_harnesses" ] || mb_fail "--mode bundle needs --harness copilot, claude or agents"
            [ -z "$mb_harnesses" ] || [ "$mb_mode" = bundle ] || mb_fail "--harness goes with --mode bundle" ;;
        verify)
            [ -z "$mb_harnesses" ] && [ -z "$mb_project" ] ||
                mb_fail "--verify checks the whole installation; leave out --harness and --project" ;;
    esac
    [ -z "$mb_project" ] || [ -n "$mb_harnesses" ] || mb_fail "--project goes with --harness"
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
    mb_bin=$mb_home/.local/bin
    mb_receipt=$mb_root/install-receipt.tsv
    mb_lock=$mb_root.midden-install.lock
    [ "$mb_root" != / ] && [ "$mb_root" != "$mb_home" ] || mb_fail "Install into a dedicated directory, not / or HOME"
    mb_no_links "$mb_home"
    mb_no_links "$mb_root"
    [ -d "$mb_home" ] || mb_fail "HOME must be an existing directory"
    [ ! -e "$mb_root" ] || [ -d "$mb_root" ] || mb_fail "Install directory is not a directory"
    if [ -n "$mb_project" ]; then
        mb_project=$(mb_path "$mb_project")
        mb_no_links "$mb_project"
        [ -d "$mb_project" ] || mb_fail "--project must be an existing folder"
        if mb_under "$mb_root" "$mb_project" || mb_under "$mb_project" "$mb_root"; then
            mb_fail "The installation folder and the project must not overlap"
        fi
    fi
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
    mb_protect_state
    mb_temp_parent=$(CDPATH= cd -P "${TMPDIR:-/tmp}" && pwd -P) ||
        mb_fail "Temporary directory must already exist"
    mb_temp_parent=$(mb_path "$mb_temp_parent")
    mb_no_links "$mb_temp_parent"
    if mb_under "$mb_temp_parent" "$mb_root" || mb_under "$mb_temp_parent" "$mb_bin" ||
        { [ -n "$mb_project" ] && mb_under "$mb_temp_parent" "$mb_project"; } ||
        { [ -n "$mb_distribution" ] && mb_under "$mb_temp_parent" "$mb_distribution"; }; then
        mb_fail "Staging requires an external temporary directory; set TMPDIR outside project, release and installation paths"
    fi
    mb_work=$(mktemp -d "$mb_temp_parent/midden-install.XXXXXXXX") ||
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
    for mb_part in plan notes requested.places new.products new.places new.files new.links new.fetched; do
        : > "$mb_work/$mb_part"
    done
    mb_read_receipt
    [ "$mb_operation" = install ] || [ "$mb_owned" != none ] ||
        mb_fail "No Midden installation in $mb_root; nothing to $mb_operation"
    mb_scope=user
    [ -z "$mb_project" ] || mb_scope=project
    for mb_name in $mb_harnesses; do
        mb_dir=$(mb_skill_dir "$mb_name" "$mb_scope")
        printf '%s\t%s\t%s\n' "$mb_name" "$mb_scope" "$mb_dir" >> "$mb_work/requested.places"
    done
    mb_version=$(mb_owned_meta version)

    if [ "$mb_operation" = verify ]; then
        : > "$mb_work/problems"
        while IFS="$mb_tab" read -r mb_base mb_rel mb_hash; do
            if [ "$mb_base" = root ]; then mb_dir=$mb_root
            else mb_dir=$(awk -F '\t' -v id="$mb_base" '$1==id {print $6}' "$mb_work/owned.places"); fi
            mb_same_file "$mb_dir/$mb_rel" "$mb_hash" || printf '%s\n' "$mb_dir/$mb_rel" >> "$mb_work/problems"
        done < "$mb_work/owned.files"
        while IFS="$mb_tab" read -r mb_link mb_link_target; do
            [ -L "$mb_link" ] && [ "$(readlink "$mb_link")" = "$mb_link_target" ] ||
                printf '%s\n' "$mb_link" >> "$mb_work/problems"
        done < "$mb_work/owned.links"
        if [ -s "$mb_work/problems" ]; then
            printf 'midden install: These Midden files were changed or removed since installation:\n' >&2
            awk '{print "  " $0}' "$mb_work/problems" >&2
            exit 1
        fi
        printf 'Verified Midden %s (%s) in %s: every owned file and launch link matches the receipt\n' \
            "$mb_version" "$(awk 'BEGIN {ORS=""} {print (NR>1?", ":"") $0}' "$mb_work/owned.products")" "$mb_root"
        awk -F '\t' -v version="$mb_version" '$2=="skills" && $5!=version {print "Older skills (" $5 ") for " $3 " in " $6}' \
            "$mb_work/owned.places"
        [ "$mb_owned" != legacy ] || printf 'Installed by the 0.3 installer; run with --upgrade to replace it\n'
        mb_committed=yes
        exit 0
    fi

    if [ "$mb_operation" = uninstall ]; then
        cp "$mb_work/owned.products" "$mb_work/new.products"
        cp "$mb_work/owned.fetched" "$mb_work/new.fetched"
        cp "$mb_work/owned.links" "$mb_work/new.links"
        mb_commit=$(mb_owned_meta commit)
        mb_target=$(mb_owned_meta target)
        if [ -n "$mb_harnesses" ]; then
            : > "$mb_work/removing"
            while IFS="$mb_tab" read -r mb_name mb_place_scope mb_dir; do
                mb_id=$(awk -F '\t' -v h="$mb_name" -v d="$mb_dir" '$2=="skills" && $3==h && $6==d {print $1}' "$mb_work/owned.places")
                [ -n "$mb_id" ] || mb_fail "Midden has no skills for $mb_name in $mb_dir"
                printf '%s\n' "$mb_id" >> "$mb_work/removing"
                mb_plan_place_removal "$mb_id" "$mb_dir"
            done < "$mb_work/requested.places"
            awk -F '\t' 'NR==FNR {gone[$1]=1; next} !($1 in gone)' "$mb_work/removing" "$mb_work/owned.places" > "$mb_work/new.places"
            awk -F '\t' 'NR==FNR {gone[$1]=1; next} !($1 in gone)' "$mb_work/removing" "$mb_work/owned.files" > "$mb_work/new.files"
            mb_write_receipt
            mb_plan_file "$mb_receipt" "$mb_old_receipt" "$(mb_hash "$mb_work/receipt.new")" "$mb_work/receipt.new" 644 "$mb_root"
        else
            while IFS="$mb_tab" read -r mb_base mb_rel mb_hash; do
                [ "$mb_base" = root ] || continue
                mb_check_file "$mb_root/$mb_rel" "$mb_hash"
                mb_plan_file "$mb_root/$mb_rel" "$mb_hash" - - 644 "$mb_root"
            done < "$mb_work/owned.files"
            while IFS="$mb_tab" read -r mb_id mb_kind mb_name mb_place_scope mb_place_version mb_dir; do
                mb_plan_place_removal "$mb_id" "$mb_dir"
            done < "$mb_work/owned.places"
            while IFS="$mb_tab" read -r mb_link mb_link_target; do
                if [ -L "$mb_link" ] && [ "$(readlink "$mb_link")" = "$mb_link_target" ]; then
                    printf 'link\t%s\t%s\t-\n' "$mb_link" "$mb_link_target" >> "$mb_work/plan"
                else
                    mb_note "Kept $mb_link: it no longer points at this Midden"
                fi
            done < "$mb_work/owned.links"
            if [ "$mb_owned" = legacy ]; then
                mb_plan_file "$mb_legacy_receipt" "$mb_old_receipt" - - 644 "$mb_root"
            else
                mb_plan_file "$mb_receipt" "$mb_old_receipt" - - 644 "$mb_root"
            fi
        fi
    else
        [ "$mb_owned" != legacy ] || [ "$mb_operation" = upgrade ] ||
            mb_fail "Midden $mb_version was installed by the 0.3 installer; run this with --upgrade to replace it"
        for mb_command in tar gzip unzip; do
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
        mb_owned_version=$mb_version
        mb_version=$(awk -F '\t' '$1=="version"{print $2}' "$mb_work/distribution/manifest.tsv")
        mb_commit=$(awk -F '\t' '$1=="commit"{print $2}' "$mb_work/distribution/manifest.tsv")
        [ "$mb_requested_version" = latest ] || [ "$mb_requested_version" = "$mb_version" ] ||
            mb_fail "Requested version differs from the verified release manifest"
        if [ "$mb_operation" = install ] && [ "$mb_owned" = current ] && [ "$mb_owned_version" != "$mb_version" ]; then
            mb_fail "Midden $mb_owned_version is installed. Run with --upgrade to move it to $mb_version, or add --version $mb_owned_version to add to it."
        fi
        case "$mb_mode" in
            core) mb_requested="core" ;;
            bundle) mb_requested="core bundle" ;;
            app) mb_requested="core bundle app" ;;
            *) if [ "$mb_operation" = install ]; then mb_requested="core bundle app"; else mb_requested=; fi ;;
        esac
        for mb_product in core bundle app; do
            if mb_has_line "$mb_work/owned.products" "$mb_product" ||
                case " $mb_requested " in *" $mb_product "*) true ;; *) false ;; esac; then
                printf '%s\n' "$mb_product" >> "$mb_work/new.products"
            fi
        done
        : > "$mb_work/desired.files"
        while IFS= read -r mb_product; do
            if [ "$mb_product" = bundle ]; then
                mb_archive=$(mb_select bundle universal)
            else
                mb_archive=$(mb_select "$mb_product" "$mb_target")
            fi
            mb_fetch "$mb_archive" "$mb_archive_limit"
            mb_checksum "$mb_archive"
            case "$mb_archive" in
                *.zip) mb_extract_zip "$mb_product" "$mb_archive" "$mb_work/payload-$mb_product" ;;
                *) mb_extract_tar "$mb_product" "$mb_archive" "$mb_work/payload-$mb_product" ;;
            esac
            while IFS="$mb_tab" read -r mb_rel mb_hash; do
                printf '%s\t%s\t%s\t%s\n' "$mb_rel" "$mb_hash" "$mb_work/payload-$mb_product/$mb_rel" "$(mb_mode_of "$mb_rel")" \
                    >> "$mb_work/desired.files"
            done < "$mb_work/$mb_product.files"
        done < "$mb_work/new.products"
        if mb_has_line "$mb_work/new.products" app; then
            mb_pin=$(awk -F '\t' -v t="$mb_target" '$1=="fetch" && $3==t {print $4 "\t" $5}' "$mb_work/distribution/manifest.tsv")
            [ -n "$mb_pin" ] || mb_fail "The release pins no Pandoc for $mb_target"
            mb_pin_url=${mb_pin%"$mb_tab"*}
            mb_pin_hash=$(printf '%s\n' "${mb_pin#*"$mb_tab"}" | awk '{print tolower($0)}')
            mb_pin_asset=${mb_pin_url##*/}
            awk -F '\t' -v t="$mb_target" '$1=="take" && $3==t {print $4 "\t" $5}' "$mb_work/distribution/manifest.tsv" \
                > "$mb_work/pandoc.take"
            printf '%s\t%s\n' "$mb_pin_url" "$mb_pin_hash" > "$mb_work/new.fetched"
            mb_reuse=no
            if [ "$(awk -F '\t' '{print $2}' "$mb_work/owned.fetched")" = "$mb_pin_hash" ]; then
                mb_reuse=yes
                while IFS="$mb_tab" read -r mb_member mb_destination; do
                    mb_hash=$(awk -F '\t' -v n="$mb_destination" '$1=="root" && $2==n {print $3}' "$mb_work/owned.files")
                    [ -n "$mb_hash" ] && mb_same_file "$mb_root/$mb_destination" "$mb_hash" || mb_reuse=no
                done < "$mb_work/pandoc.take"
            fi
            if [ "$mb_reuse" = yes ]; then
                while IFS="$mb_tab" read -r mb_member mb_destination; do
                    mb_hash=$(awk -F '\t' -v n="$mb_destination" '$1=="root" && $2==n {print $3}' "$mb_work/owned.files")
                    printf '%s\t%s\tkeep\t%s\n' "$mb_destination" "$mb_hash" "$(mb_mode_of "$mb_destination")" >> "$mb_work/desired.files"
                done < "$mb_work/pandoc.take"
            elif [ "$mb_dry_run" = yes ]; then
                if [ -n "$mb_distribution" ]; then
                    mb_fetch_as "$mb_pin_asset" "$mb_pin_url" "$mb_tool_limit"
                    [ "$(mb_hash "$mb_work/distribution/$mb_pin_asset")" = "$mb_pin_hash" ] ||
                        mb_fail "Pandoc archive checksum mismatch: $mb_pin_asset"
                else
                    mb_note "Pandoc is downloaded from $mb_pin_url when installing"
                fi
                while IFS="$mb_tab" read -r mb_member mb_destination; do
                    printf '%s\t-\tfetch\t%s\n' "$mb_destination" "$(mb_mode_of "$mb_destination")" >> "$mb_work/desired.files"
                done < "$mb_work/pandoc.take"
            else
                mb_fetch_as "$mb_pin_asset" "$mb_pin_url" "$mb_tool_limit"
                [ "$(mb_hash "$mb_work/distribution/$mb_pin_asset")" = "$mb_pin_hash" ] ||
                    mb_fail "Pandoc archive checksum mismatch: $mb_pin_asset"
                mb_extract_pandoc "$mb_pin_asset"
                while IFS="$mb_tab" read -r mb_member mb_destination; do
                    printf '%s\t%s\t%s\t%s\n' "$mb_destination" "$(mb_hash "$mb_work/pandoc/$mb_destination")" \
                        "$mb_work/pandoc/$mb_destination" "$(mb_mode_of "$mb_destination")" >> "$mb_work/desired.files"
                done < "$mb_work/pandoc.take"
            fi
        fi
        awk -F '\t' '{name=tolower($1); if (seen[name]++) bad=1; n=split($1,parts,"/"); prefix=""
            for (i=1;i<n;i++) {prefix=prefix (i>1?"/":"") tolower(parts[i]); dirs[prefix]=1}}
            END {for (name in seen) if (name in dirs) bad=1; exit bad}' "$mb_work/desired.files" ||
            mb_fail "Products collide on an installation path"
        while IFS="$mb_tab" read -r mb_rel mb_hash mb_source mb_permissions; do
            [ "$mb_source" != keep ] || continue
            mb_old=$(awk -F '\t' -v n="$mb_rel" '$1=="root" && $2==n {print $3}' "$mb_work/owned.files")
            printf 'root\t%s\t%s\n' "$mb_rel" "$mb_hash" >> "$mb_work/new.files"
            if [ -n "$mb_old" ] && [ "$mb_old" = "$mb_hash" ]; then
                mb_check_file "$mb_root/$mb_rel" "$mb_old"
                continue
            fi
            mb_plan_file "$mb_root/$mb_rel" "${mb_old:--}" "$mb_hash" "$mb_source" "$mb_permissions" "$mb_root"
        done < "$mb_work/desired.files"
        awk -F '\t' '$3=="keep" {print "root\t" $1 "\t" $2}' "$mb_work/desired.files" >> "$mb_work/new.files"
        while IFS="$mb_tab" read -r mb_base mb_rel mb_hash; do
            [ "$mb_base" = root ] || continue
            awk -F '\t' -v n="$mb_rel" '$1==n {found=1} END {exit (!found)}' "$mb_work/desired.files" ||
                mb_plan_file "$mb_root/$mb_rel" "$mb_hash" - - 644 "$mb_root"
        done < "$mb_work/owned.files"
        : > "$mb_work/skill.files"
        if mb_has_line "$mb_work/new.products" bundle; then
            awk -F '\t' -v base="$mb_work/payload-bundle/" '{rel=substr($1, 8); print rel "\t" $2 "\t" base $1}' \
                "$mb_work/bundle.files" > "$mb_work/skill.files"
        fi
        mb_next_id=$(awk -F '\t' 'BEGIN {max=0} {if ($1+0 > max) max=$1+0} END {print max+1}' "$mb_work/owned.places")
        while IFS="$mb_tab" read -r mb_id mb_kind mb_name mb_place_scope mb_place_version mb_dir; do
            [ "$mb_kind" = skills ] || continue
            mb_wanted=no
            awk -F '\t' -v d="$mb_dir" '$3==d {found=1} END {exit (!found)}' "$mb_work/requested.places" && mb_wanted=yes
            if [ "$mb_wanted" = no ] && { [ "$mb_operation" != upgrade ] || [ "$mb_place_scope" != user ]; }; then
                printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$mb_id" "$mb_kind" "$mb_name" "$mb_place_scope" "$mb_place_version" "$mb_dir" >> "$mb_work/new.places"
                awk -F '\t' -v id="$mb_id" '$1==id' "$mb_work/owned.files" >> "$mb_work/new.files"
                continue
            fi
            if ! mb_place_unchanged "$mb_id" "$mb_dir"; then
                [ "$mb_wanted" = no ] ||
                    mb_fail "The skills in $mb_dir were changed since Midden installed them; restore or move them, then run this again"
                mb_note "Left the skills in $mb_dir at $mb_place_version: they were changed since installation"
                printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$mb_id" "$mb_kind" "$mb_name" "$mb_place_scope" "$mb_place_version" "$mb_dir" >> "$mb_work/new.places"
                awk -F '\t' -v id="$mb_id" '$1==id' "$mb_work/owned.files" >> "$mb_work/new.files"
                continue
            fi
            printf '%s\tskills\t%s\t%s\t%s\t%s\n' "$mb_id" "$mb_name" "$mb_place_scope" "$mb_version" "$mb_dir" >> "$mb_work/new.places"
            printf '%s\n' "$mb_dir" >> "$mb_work/written"
            mb_plan_place_write "$mb_id" "$mb_dir" "$mb_id"
        done < "$mb_work/owned.places"
        while IFS="$mb_tab" read -r mb_name mb_place_scope mb_dir; do
            ! awk -F '\t' -v d="$mb_dir" '$6==d {found=1} END {exit (!found)}' "$mb_work/owned.places" || continue
            printf '%s\tskills\t%s\t%s\t%s\t%s\n' "$mb_next_id" "$mb_name" "$mb_place_scope" "$mb_version" "$mb_dir" >> "$mb_work/new.places"
            printf '%s\n' "$mb_dir" >> "$mb_work/written"
            mb_plan_place_write "$mb_next_id" "$mb_dir" -
            mb_next_id=$((mb_next_id + 1))
        done < "$mb_work/requested.places"
        if mb_has_line "$mb_work/new.products" app; then
            mb_start=$(mb_start_dir)
            mb_no_links "$mb_start"
            mb_write_start "$mb_work/start" "$mb_work/start.files"
            mb_start_id=$(awk -F '\t' '$2=="start" {print $1}' "$mb_work/owned.places")
            if [ -n "$mb_start_id" ] && ! mb_place_unchanged "$mb_start_id" "$mb_start" &&
                awk -F '\t' -v id="$mb_start_id" '$1==id {print $2}' "$mb_work/owned.files" |
                { while IFS= read -r mb_rel; do [ -e "$mb_start/$mb_rel" ] && exit 0; done; exit 1; }; then
                mb_note "Kept the Start entry in $mb_start: it was changed since installation"
                awk -F '\t' -v id="$mb_start_id" '$1==id' "$mb_work/owned.places" >> "$mb_work/new.places"
                awk -F '\t' -v id="$mb_start_id" '$1==id' "$mb_work/owned.files" >> "$mb_work/new.files"
            else
                [ -n "$mb_start_id" ] || { mb_start_id=$mb_next_id; mb_next_id=$((mb_next_id + 1)); }
                printf '%s\tstart\t-\t-\t%s\t%s\n' "$mb_start_id" "$mb_version" "$mb_start" >> "$mb_work/new.places"
                while IFS="$mb_tab" read -r mb_rel mb_permissions; do
                    mb_hash=$(mb_hash "$mb_work/start/$mb_rel")
                    mb_old=$(awk -F '\t' -v id="$mb_start_id" -v n="$mb_rel" '$1==id && $2==n {print $3}' "$mb_work/owned.files")
                    if [ -n "$mb_old" ] && ! mb_same_file "$mb_start/$mb_rel" "$mb_old"; then mb_old=; fi
                    printf '%s\t%s\t%s\n' "$mb_start_id" "$mb_rel" "$mb_hash" >> "$mb_work/new.files"
                    [ "$mb_old" != "$mb_hash" ] || continue
                    mb_plan_file "$mb_start/$mb_rel" "${mb_old:--}" "$mb_hash" "$mb_work/start/$mb_rel" "$mb_permissions" "$mb_start"
                done < "$mb_work/start.files"
            fi
        fi
        cp "$mb_work/owned.links" "$mb_work/new.links"
        if [ "$mb_path_links" = yes ]; then
            for mb_name in midden midden-ui; do
                [ "$mb_name" = midden ] || mb_has_line "$mb_work/new.products" app || continue
                ! awk -F '\t' -v p="$mb_bin/$mb_name" '$1==p {found=1} END {exit (!found)}' "$mb_work/owned.links" || continue
                [ ! -e "$mb_bin/$mb_name" ] && [ ! -L "$mb_bin/$mb_name" ] ||
                    mb_fail "Refusing unowned PATH entry: $mb_bin/$mb_name (use --no-path)"
                printf 'link\t%s\t-\t%s\n' "$mb_bin/$mb_name" "$mb_root/$mb_name" >> "$mb_work/plan"
                printf '%s\t%s\n' "$mb_bin/$mb_name" "$mb_root/$mb_name" >> "$mb_work/new.links"
            done
        fi
        mb_write_receipt
        if [ "$mb_owned" = legacy ]; then
            mb_plan_file "$mb_legacy_receipt" "$mb_old_receipt" - - 644 "$mb_root"
            mb_plan_file "$mb_receipt" - "$(mb_hash "$mb_work/receipt.new")" "$mb_work/receipt.new" 644 "$mb_root"
        else
            mb_plan_file "$mb_receipt" "${mb_old_receipt:--}" "$(mb_hash "$mb_work/receipt.new")" "$mb_work/receipt.new" 644 "$mb_root"
        fi
    fi

    # Every planned change is checked before anything is written.
    while IFS="$mb_tab" read -r mb_type mb_path mb_old mb_new mb_source mb_permissions mb_base; do
        if [ "$mb_type" = file ]; then
            mb_no_links "$mb_path"
            if [ "$mb_old" = - ]; then
                [ ! -e "$mb_path" ] && [ ! -L "$mb_path" ] ||
                    mb_fail "Refusing to overwrite a file Midden does not own: $mb_path"
            else
                mb_check_file "$mb_path" "$mb_old"
            fi
        else
            mb_no_links "${mb_path%/*}"
            [ ! -e "${mb_path%/*}" ] || [ -d "${mb_path%/*}" ] || mb_fail "PATH link directory is occupied: ${mb_path%/*}"
        fi
    done < "$mb_work/plan"

    if [ "$mb_dry_run" = yes ]; then
        printf 'Dry-run: %s Midden %s (%s) in %s\n' "$mb_operation" "$mb_version" \
            "$(awk 'BEGIN {ORS=""} {print (NR>1?", ":"") $0}' "$mb_work/new.products")" "$mb_root"
        awk -F '\t' '{
            action = ($3=="-") ? "create" : (($4=="-") ? "remove" : "replace")
            if ($1=="file" && $5=="fetch") action="fetch"
            print "  " action " " $2
        }' "$mb_work/plan"
        cat "$mb_work/notes"
        printf 'Nothing was written, extracted into place, probed or launched; no PATH link or profile changed.\n'
        printf 'Checksums establish integrity, not cryptographic authenticity.\n'
        mb_committed=yes
        exit 0
    fi
    if [ "$mb_operation" != uninstall ] && [ "$mb_operation" != verify ]; then
        chmod 755 "$mb_work/payload-core/midden"
        mb_probe=$("$mb_work/payload-core/midden" version </dev/null 2>"$mb_work/probe.stderr") ||
            mb_fail "Verified core version probe failed"
        [ "$mb_probe" = "midden $mb_version" ] && [ ! -s "$mb_work/probe.stderr" ] ||
            mb_fail "Core version probe does not match the verified manifest"
        if mb_has_line "$mb_work/new.products" app; then
            chmod 755 "$mb_work/payload-app/midden-ui"
            mb_probe=$("$mb_work/payload-app/midden-ui" --version </dev/null 2>"$mb_work/probe.stderr") ||
                mb_fail "Verified app version probe failed"
            [ "$mb_probe" = "midden-ui $mb_version" ] && [ ! -s "$mb_work/probe.stderr" ] ||
                mb_fail "App version probe does not match the verified manifest"
        fi
    fi
    mb_mkdir "${mb_root%/*}"
    mkdir "$mb_lock" || mb_fail "Cannot acquire installation lock: $mb_lock"
    mb_locked=yes
    # The receipts go last, so a failure anywhere before them leaves the old receipt describing the old files.
    awk -F '\t' -v receipt="$mb_receipt" -v legacy="$mb_legacy_receipt" \
        '$2!=receipt && $2!=legacy' "$mb_work/plan" > "$mb_work/plan.ordered"
    awk -F '\t' -v legacy="$mb_legacy_receipt" '$2==legacy' "$mb_work/plan" >> "$mb_work/plan.ordered"
    awk -F '\t' -v receipt="$mb_receipt" '$2==receipt' "$mb_work/plan" >> "$mb_work/plan.ordered"
    while IFS="$mb_tab" read -r mb_type mb_path mb_old mb_new mb_source mb_permissions mb_base; do
        if [ "$mb_type" = file ]; then
            mb_apply_file "$mb_path" "$mb_source" "$mb_old" "$mb_new" "$mb_permissions"
        else
            mb_apply_link "$mb_path" "$mb_old" "$mb_new"
        fi
    done < "$mb_work/plan.ordered"
    mb_committed=yes
    while IFS="$mb_tab" read -r mb_type mb_path mb_old mb_new mb_source mb_permissions mb_base; do
        [ "$mb_type" = file ] && [ "$mb_new" = - ] || continue
        mb_prune "$mb_path" "$mb_base"
    done < "$mb_work/plan"
    if [ "$mb_operation" = uninstall ]; then
        if [ -z "$mb_harnesses" ] && [ -d "$mb_root" ] && [ -z "$(ls -A "$mb_root")" ]; then
            rmdir "$mb_root" || true
        fi
        if [ -n "$mb_harnesses" ]; then
            printf 'Removed Midden'\''s skills for %s\n' "$mb_harnesses"
        else
            printf 'Removed unchanged Midden files and launch links. Core'\''s state, the App'\''s data and files Midden does not own were kept.\n'
        fi
        cat "$mb_work/notes"
        exit 0
    fi
    printf 'Installed Midden %s (%s) in %s\n' "$mb_version" \
        "$(awk 'BEGIN {ORS=""} {print (NR>1?", ":"") $0}' "$mb_work/new.products")" "$mb_root"
    if [ -s "$mb_work/written" ]; then
        while IFS= read -r mb_dir; do printf 'Skills: %s\n' "$mb_dir"; done < "$mb_work/written"
    fi
    awk -F '\t' -v version="$mb_version" '$2=="skills" && $4=="project" && $5!=version {
        project=$6; sub(/\/\.(github|claude|agents)\/skills$/, "", project)
        print "Older skills (" $5 ") in " project "; update them with: install.sh --mode bundle --harness " $3 " --project '\''" project "'\''"
    }' "$mb_work/new.places"
    cat "$mb_work/notes"
    printf 'Checksums establish integrity, not cryptographic authenticity; trust the release channel.\n'
    printf 'Core executable: %s/midden\n' "$mb_root"
    if [ -s "$mb_work/new.links" ]; then
        printf 'Owned launch links: %s (no PATH or shell profile was changed)\n' "$mb_bin"
    fi
    if mb_has_line "$mb_work/new.products" app && [ "$mb_launch" = yes ] &&
        { [ -z "$mb_mode" ] || [ "$mb_mode" = app ]; }; then
        mb_cleanup 0 || exit 1
        trap - 0 HUP INT TERM
        if [ "$mb_open" = no ]; then
            exec "$mb_root/midden-ui" --no-open </dev/null
        else
            exec "$mb_root/midden-ui" </dev/null
        fi
    fi
)

midden_install_main "$@"

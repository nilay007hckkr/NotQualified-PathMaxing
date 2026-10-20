# Shared helpers for snapshot-step.sh, verify-steps.sh and make-handoff.sh.
# This file is sourced, not executed.
#
# The scripts read the team split from the docs instead of keeping a second
# copy: members and parts come from PARTS.md, the commit order from
# COMMIT_PLAN.md, and file ownership from scripts/ownership.txt.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HANDOFF="$ROOT/handoff"
SHADOW="$ROOT/.handoff-shadow"
STEPS_DIR="$HANDOFF/steps"
STEPS_INDEX="$HANDOFF/steps.tsv"
OWNERSHIP="$ROOT/scripts/ownership.txt"

die() { printf 'error: %s\n' "$*" >&2; exit 1; }
note() { printf '%s\n' "$*" >&2; }

# step_num C07 -> 7. Base 10 so C08 and C09 are not read as octal.
step_num() { echo $((10#${1#C})); }

# list_files DIR prints every file under DIR that belongs in a commit, as
# sorted repo-relative paths. The exclusions mirror .gitignore: handoff
# output, secrets, dependencies and build output never reach a step.
list_files() {
  (cd "$1" && find . \
      \( -name .git -o -name node_modules -o -name .vite \
         -o -path ./handoff -o -path ./.handoff-shadow \
         -o -path ./web/dist -o -path ./web/coverage -o -path ./bin -o -path ./tmp \) -prune \
      -o -type f -print) |
    sed 's|^\./||' |
    awk '{
      n = split($0, parts, "/"); base = parts[n]
      if (base ~ /^\.env(\..+)?$/ && base != ".env.example") next
      if (base ~ /\.(exe|test|out|log|tsbuildinfo)$/ || base ~ /^__debug_bin/) next
      if ($0 == ".claude/settings.local.json") next
      print
    }' |
    LC_ALL=C sort
}

# hash_tree DIR prints "path<TAB>sha256" for every file list_files reports.
hash_tree() {
  local dir=$1
  [ -d "$dir" ] || return 0
  list_files "$dir" | (cd "$dir" && tr '\n' '\0' | xargs -0 -r sha256sum -b) |
    sed -E 's/^([0-9a-f]{64}) [ *](.*)$/\2\t\1/' | LC_ALL=C sort
}

# diff_trees OLD NEW prints "A|M|D<TAB>path" for files added, modified or
# deleted going from tree OLD to tree NEW.
diff_trees() {
  local old new
  old=$(mktemp) new=$(mktemp)
  hash_tree "$1" >"$old"
  hash_tree "$2" >"$new"
  # FILENAME, not NR == FNR: the old list is empty for the first step.
  awk -F'\t' '
    FILENAME == ARGV[1] { before[$1] = $2; next }
    { seen[$1] = 1
      if (!($1 in before)) print "A\t" $1
      else if (before[$1] != $2) print "M\t" $1 }
    END { for (p in before) if (!(p in seen)) print "D\t" p }
  ' "$old" "$new" | LC_ALL=C sort -t$'\t' -k2
  rm -f "$old" "$new"
}

# ---- ownership --------------------------------------------------------------

OWN_PART=()
OWN_GLOB=()

# load_ownership reads scripts/ownership.txt: "<part> <glob>" per line, where
# <part> is 1-5 or "*" for a shared file and "*" in a glob also matches "/".
load_ownership() {
  local file=${1:-$OWNERSHIP} part glob rest
  [ -f "$file" ] || die "missing $file"
  OWN_PART=() OWN_GLOB=()
  while read -r part glob rest || [ -n "$part" ]; do
    case $part in '' | '#'*) continue ;; esac
    [ -n "$glob" ] && [ -z "$rest" ] || die "bad line in ownership.txt: $part $glob $rest"
    [[ $part =~ ^([1-5]|\*)$ ]] || die "bad part '$part' in ownership.txt"
    OWN_PART+=("$part")
    OWN_GLOB+=("$glob")
  done <"$file"
}

# owner_of PATH prints the owning part, "*" for a shared file, "none" when no
# line matches, or "multiple:<parts>" when more than one line matches.
owner_of() {
  local path=$1 i found=()
  for i in "${!OWN_GLOB[@]}"; do
    # Unquoted right-hand side: the glob is a pattern, and * also matches /.
    # shellcheck disable=SC2053
    if [[ $path == ${OWN_GLOB[$i]} ]]; then found+=("${OWN_PART[$i]}"); fi
  done
  case ${#found[@]} in
    0) echo none ;;
    1) echo "${found[0]}" ;;
    *) echo "multiple:$(IFS=,; echo "${found[*]}")" ;;
  esac
}

# check_tree_ownership DIR fails if any file in DIR is unowned or owned twice.
check_tree_ownership() {
  local dir=$1 path owner bad=0
  while IFS= read -r path; do
    owner=$(owner_of "$path")
    case $owner in
      none) note "  unowned file: $path"; bad=1 ;;
      multiple:*) note "  file owned by more than one part (${owner#multiple:}): $path"; bad=1 ;;
    esac
  done < <(list_files "$dir")
  [ $bad -eq 0 ] || die "fix scripts/ownership.txt so every file has exactly one owner"
}

# ---- team and plan (parsed from the docs) -----------------------------------

# parts_table prints "part<TAB>part title<TAB>owner full name" from PARTS.md.
parts_table() {
  awk -F'|' '
    { for (i = 2; i <= 4; i++) gsub(/^[ \t]+|[ \t]+$/, "", $i) }
    $2 ~ /^[1-5]$/ && $4 != "" { print $2 "\t" $3 "\t" $4 }
  ' "$ROOT/PARTS.md"
}

# member_part MEMBER prints the part number owned by MEMBER (first name).
member_part() {
  local p
  p=$(parts_table | awk -F'\t' -v m="$1" '{ split($3, w, " "); if (w[1] == m) print $1 }')
  [ -n "$p" ] || die "unknown member '$1' (see the owner table in PARTS.md)"
  echo "$p"
}

member_full_name() { parts_table | awk -F'\t' -v m="$1" '{ split($3, w, " "); if (w[1] == m) print $3 }'; }
part_title() { parts_table | awk -F'\t' -v p="$1" '$1 == p { print $2 }'; }
all_members() { parts_table | awk -F'\t' '{ split($3, w, " "); print w[1] }'; }

# plan_rows prints "Cxx<TAB>member<TAB>folder<TAB>title" from the global
# order table in COMMIT_PLAN.md.
plan_rows() {
  awk -F'|' '
    { for (i = 2; i <= 5; i++) gsub(/^[ \t]+|[ \t]+$/, "", $i) }
    $2 ~ /^C[0-9][0-9]$/ { t = $5; gsub(/`/, "", t); print $2 "\t" $3 "\t" $4 "\t" t }
  ' "$ROOT/COMMIT_PLAN.md"
}

# ---- recorded steps ---------------------------------------------------------

# recorded_steps prints the steps index (handoff/steps.tsv) in commit order:
# "Cxx<TAB>member<TAB>folder<TAB>title".
recorded_steps() {
  [ -f "$STEPS_INDEX" ] || return 0
  LC_ALL=C sort "$STEPS_INDEX"
}

# apply_step STEP_DIR TARGET copies a step's files into TARGET and removes
# the files the step deleted, exactly as a member does before committing.
apply_step() {
  local step=$1 target=$2 f
  if [ -d "$step/files" ]; then cp -R "$step/files/." "$target/"; fi
  if [ -f "$step/DELETED.txt" ]; then
    while IFS= read -r f; do
      if [ -n "$f" ]; then rm -f "$target/$f"; fi
    done <"$step/DELETED.txt"
  fi
}

# steps_fingerprint prints one hash over every recorded step, so
# make-handoff.sh can tell whether the steps changed since the last full
# verify-steps.sh run.
steps_fingerprint() {
  [ -d "$STEPS_DIR" ] || { echo none; return; }
  { recorded_steps; hash_tree "$STEPS_DIR"; } | sha256sum | cut -c1-64
}

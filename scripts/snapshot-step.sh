#!/usr/bin/env bash
# Records one finished step as a commit folder that its owner commits by hand.
#
#   scripts/snapshot-step.sh <Cxx> <Member> <slug> "<commit title>" <<'EOF'
#   2-4 lines saying what changed and how it was tested.
#   EOF
#
# The commit body is read from stdin. The script compares the working tree
# with .handoff-shadow/ (the tree as of the previous step), copies every new
# or changed file into handoff/steps/<Cxx>-<slug>/files/, and writes
# FILES.txt, DELETED.txt (only when files were removed) and COMMIT_MSG.txt.
#
# It refuses a step whose files belong to another member's part (go.mod and
# go.sum are shared), a step that disagrees with COMMIT_PLAN.md, and a step
# number lower than the last recorded one. Running it again for the most
# recent step redoes that step.

. "$(dirname "$0")/lib.sh"

[ $# -eq 4 ] || die "usage: scripts/snapshot-step.sh <Cxx> <Member> <slug> \"<title>\" < body.txt"
id=$1 member=$2 title=$4
slug=${3#"$id"-}
folder="$id-$slug"

[[ $id =~ ^C[0-9]{2}$ ]] || die "step id must look like C07, got '$id'"
[[ $slug =~ ^[a-z0-9][a-z0-9-]*$ ]] || die "slug must be lowercase letters, digits and dashes, got '$slug'"
part=$(member_part "$member")

# The plan is the source of truth for who commits what, in which order.
row=$(plan_rows | awk -F'\t' -v id="$id" '$1 == id')
if [ -n "$row" ]; then
  IFS=$'\t' read -r _ plan_member plan_folder plan_title <<<"$row"
  [ "$plan_member" = "$member" ] || die "COMMIT_PLAN.md gives $id to $plan_member, not $member"
  [ "$plan_folder" = "$folder" ] || die "COMMIT_PLAN.md names the folder $plan_folder, not $folder"
  [ "$plan_title" = "$title" ] || die "title differs from COMMIT_PLAN.md: '$plan_title'"
else
  note "warning: $id is not in COMMIT_PLAN.md; recording it as an extra step"
fi

[ ! -t 0 ] || die "pipe the commit body (2-4 lines) on stdin"
# Drop trailing spaces and leading blank lines; $(...) drops trailing ones.
body=$(sed -e 's/[[:space:]]*$//' | sed -e '/./,$!d')
lines=$(printf '%s\n' "$body" | grep -c . || true)
[ "$lines" -ge 2 ] && [ "$lines" -le 4 ] || die "commit body must have 2-4 lines, got $lines"

load_ownership
check_tree_ownership "$ROOT"

mkdir -p "$STEPS_DIR"
# The step copies hold .go files inside the repository; a go.mod of their own
# makes handoff/ a separate module, so "go build ./..." in the repo skips them.
if [ ! -f "$HANDOFF/go.mod" ]; then
  printf 'module handoff\n\n// Keeps "go build ./..." in the repository out of the step copies.\n' >"$HANDOFF/go.mod"
fi
last=$(recorded_steps | tail -n 1 | cut -f1)
redo=0
if [ -n "$last" ]; then
  if [ "$(step_num "$id")" -lt "$(step_num "$last")" ]; then
    die "$id comes before the last recorded step $last; recorded steps are frozen, so put the fix in the owner's next step"
  fi
  if [ "$id" = "$last" ]; then redo=1; fi
fi

# The base is the tree as of the step before this one. Normally that is the
# shadow; a redo (or a missing shadow) rebuilds it from the recorded steps in
# a temp dir, so a failed run never leaves a half-built shadow behind.
base="$SHADOW"
tmp="$HANDOFF/.tmp-$id"
trap 'rm -rf "$tmp"; if [ "$base" != "$SHADOW" ]; then rm -rf "$base"; fi' EXIT
if [ $redo -eq 1 ] || { [ ! -d "$SHADOW" ] && [ -n "$last" ]; }; then
  [ $redo -eq 1 ] && note "redoing $id: rebuilding the previous tree from the earlier steps"
  base="$HANDOFF/.base-$id"
  rm -rf "$base"
  mkdir -p "$base"
  while IFS=$'\t' read -r sid _ sfolder _; do
    [ "$sid" = "$id" ] && continue
    apply_step "$STEPS_DIR/$sfolder" "$base"
  done < <(recorded_steps)
fi
mkdir -p "$base"

changes=$(diff_trees "$base" "$ROOT")
[ -n "$changes" ] || die "nothing changed since the previous step; a step can't be empty"

# Every changed file must belong to this member's part.
bad=0
while IFS=$'\t' read -r kind path; do
  case $path in go.mod | go.sum) continue ;; esac
  owner=$(owner_of "$path")
  if [ "$owner" != "$part" ]; then
    note "  $path belongs to part $owner, not part $part ($member)"
    bad=1
  fi
done <<<"$changes"
[ $bad -eq 0 ] || die "$id would commit another part's files; split the work or move it to the right step"

# Build the step folder next to its final place, then swap it in.
rm -rf "$tmp"
mkdir -p "$tmp/files"
: >"$tmp/FILES.txt"
while IFS=$'\t' read -r kind path; do
  case $kind in
    A | M)
      mkdir -p "$tmp/files/$(dirname "$path")"
      cp "$ROOT/$path" "$tmp/files/$path"
      printf '%s\n' "$path" >>"$tmp/FILES.txt"
      ;;
    D) printf '%s\n' "$path" >>"$tmp/DELETED.txt" ;;
  esac
done <<<"$changes"
printf '%s\n\n%s\n' "$title" "$body" >"$tmp/COMMIT_MSG.txt"

# Remove any earlier folder for this id (a redo may change the slug).
for old in "$STEPS_DIR/$id"-*; do
  if [ -e "$old" ]; then rm -rf "$old"; fi
done
mv "$tmp" "$STEPS_DIR/$folder"

# Bring the shadow up to date with this step.
if [ "$base" != "$SHADOW" ]; then
  rm -rf "$SHADOW"
  mv "$base" "$SHADOW"
fi
apply_step "$STEPS_DIR/$folder" "$SHADOW"

{
  recorded_steps | awk -F'\t' -v id="$id" '$1 != id'
  printf '%s\t%s\t%s\t%s\n' "$id" "$member" "$folder" "$title"
} >"$STEPS_INDEX.new"
LC_ALL=C sort "$STEPS_INDEX.new" >"$STEPS_INDEX"
rm -f "$STEPS_INDEX.new"

added=$(grep -c '^A' <<<"$changes" || true)
modified=$(grep -c '^M' <<<"$changes" || true)
deleted=$(grep -c '^D' <<<"$changes" || true)
echo "recorded $folder for $member: $added added, $modified modified, $deleted deleted"
echo "  handoff/steps/$folder/"

#!/usr/bin/env bash
# Replays the recorded steps C01..Cn into an empty temp dir, one at a time,
# exactly as the members will commit them, and runs the checks after each:
#
#   - every file has exactly one owner, and the step only touches its
#     member's part (go.mod and go.sum are shared);
#   - go build, go vet, gofmt and go test (once go.mod exists);
#   - npm ci (when package-lock.json changed), npm run build and npm test
#     (once web/package.json exists).
#
# After the last step it also checks that the replayed tree equals the
# working tree, so a change that was never snapshotted can't slip through.
# It stops at the first step that fails.
#
#   scripts/verify-steps.sh [--from Cxx] [--to Cxx] [--race] [--fresh] [--keep]
#
#   --from Cxx  apply earlier steps without checking them
#   --to Cxx    stop after this step (skips the working-tree comparison)
#   --race      go test -race (needs cgo and a C compiler; CI always uses it)
#   --fresh     go test -count=1, ignoring cached test results
#   --keep      keep the temp dir afterwards
#
# TEST_DATABASE_URL and GOTOOLCHAIN are passed through, so
# GOTOOLCHAIN=go1.24.0 scripts/verify-steps.sh proves the Go 1.24 build.

. "$(dirname "$0")/lib.sh"

from="" to="" race=0 fresh=0 keep=0
while [ $# -gt 0 ]; do
  case $1 in
    --from) from=$2; shift 2 ;;
    --to) to=$2; shift 2 ;;
    --race) race=1; shift ;;
    --fresh) fresh=1; shift ;;
    --keep) keep=1; shift ;;
    *) die "unknown option $1" ;;
  esac
done

steps=$(recorded_steps)
[ -n "$steps" ] || die "no recorded steps; run scripts/snapshot-step.sh first"
first=$(head -n 1 <<<"$steps" | cut -f1)
last=$(tail -n 1 <<<"$steps" | cut -f1)
from=${from:-$first}
to=${to:-$last}

load_ownership

work=$(mktemp -d "${TMPDIR:-/tmp}/pathmaxing-verify.XXXXXX")
logs="$work.logs"
mkdir -p "$logs"
cleanup() {
  if [ $keep -eq 1 ]; then note "kept $work"; else rm -rf "$work" "$logs"; fi
}
trap cleanup EXIT

gotest=(go test)
[ $race -eq 1 ] && gotest+=(-race)
[ $fresh -eq 1 ] && gotest+=(-count=1)

# run LABEL CMD...: runs CMD in the replay dir, logging its output, and
# prints the tail of the log when it fails.
run() {
  local label=$1 log
  shift
  log="$logs/$current-$label.log"
  if ! (cd "$work" && "$@") >"$log" 2>&1; then
    note ""
    note "FAIL $current: $label"
    tail -n 60 "$log" >&2
    note "full log: $log"
    keep=1
    exit 1
  fi
}

gofmt_check() {
  local dirs out
  dirs=$(go list -f '{{.Dir}}' ./...)
  out=$(gofmt -l $dirs)
  if [ -n "$out" ]; then
    echo "gofmt would change:"
    echo "$out"
    return 1
  fi
}

lock_hash=""
checked=0
while IFS=$'\t' read -r id member folder title; do
  current=$id
  step_dir="$STEPS_DIR/$folder"
  [ -d "$step_dir" ] || die "steps.tsv lists $folder but handoff/steps/$folder is missing"
  apply_step "$step_dir" "$work"

  if [ "$(step_num "$id")" -lt "$(step_num "$from")" ]; then continue; fi
  if [ "$(step_num "$id")" -gt "$(step_num "$to")" ]; then break; fi

  start=$SECONDS
  part=$(member_part "$member")
  check_tree_ownership "$work"
  while IFS= read -r path; do
    case $path in go.mod | go.sum | '') continue ;; esac
    owner=$(owner_of "$path")
    [ "$owner" = "$part" ] || die "$id ($member, part $part) contains $path, which belongs to part $owner"
  done <"$step_dir/FILES.txt"

  did=""
  if [ -f "$work/go.mod" ]; then
    run go-build go build ./...
    run go-vet go vet ./...
    run gofmt gofmt_check
    run go-test "${gotest[@]}" ./...
    did="go"
  fi
  if [ -f "$work/web/package.json" ]; then
    new_hash=$(sha256sum "$work/web/package-lock.json" 2>/dev/null | cut -c1-64 || echo none)
    if [ "$new_hash" != "$lock_hash" ] || [ ! -d "$work/web/node_modules" ]; then
      run npm-ci bash -c 'cd web && npm ci --no-audit --no-fund'
      lock_hash=$new_hash
    fi
    run npm-build bash -c 'cd web && npm run build'
    run npm-test bash -c 'cd web && npm test'
    did="${did:+$did+}web"
  fi
  printf '%-4s %-7s ok  %4ss  %s\n' "$id" "$member" "$((SECONDS - start))" "${did:-ownership only}"
  checked=$((checked + 1))
done <<<"$steps"

if [ "$to" = "$last" ]; then
  current=final
  diff=$(diff_trees "$work" "$ROOT")
  if [ -n "$diff" ]; then
    note "FAIL: the working tree differs from the replayed steps:"
    sed 's/^A\t/  not in any step: /; s/^M\t/  changed since its step: /; s/^D\t/  deleted but still in a step: /' <<<"$diff" >&2
    note "snapshot the pending changes (or undo them) and run again"
    exit 1
  fi
  echo "working tree matches the replayed steps $first..$last"
fi

echo "verified $checked step(s)"
if [ "$from" = "$first" ] && [ "$to" = "$last" ]; then
  {
    echo "fingerprint $(steps_fingerprint)"
    echo "steps $first..$last"
    echo "race $race"
    echo "go $(go env GOVERSION)"
    echo "date $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  } >"$HANDOFF/verified.txt"
  echo "recorded handoff/verified.txt"
fi

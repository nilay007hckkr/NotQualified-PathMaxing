#!/usr/bin/env bash
# Packs the recorded steps into one zip per member, plus the global order:
#
#   scripts/make-handoff.sh <REPO_URL> [--force]
#
# writes handoff/share/{Nilay,Mayank,Karan,Mansi,Manas}.zip and ORDER.txt.
# Each zip unpacks to <Member>/ with INSTRUCTIONS.txt, ORDER.txt and
# commits/<Cxx-slug>/ for every step that member commits.
#
# It refuses to run unless every step in COMMIT_PLAN.md is recorded and the
# last full scripts/verify-steps.sh run saw exactly these steps. --force skips
# both checks (for trying the script out, never for the real handoff).

. "$(dirname "$0")/lib.sh"

url="" force=0
while [ $# -gt 0 ]; do
  case $1 in
    --force) force=1; shift ;;
    -*) die "unknown option $1" ;;
    *) [ -z "$url" ] || die "only one repository URL"; url=$1; shift ;;
  esac
done
[ -n "$url" ] || die "usage: scripts/make-handoff.sh <REPO_URL> [--force]"
[[ $url =~ ^(https://|git@)[^[:space:]]+$ ]] || die "'$url' doesn't look like a git URL"
repo_dir=$(basename "${url%/}")
repo_dir=${repo_dir%.git}

steps=$(recorded_steps)
[ -n "$steps" ] || die "no recorded steps"

missing=$(plan_rows | cut -f1 | while read -r id; do
  grep -q "^$id"$'\t' <<<"$steps" || echo "$id"
done)
fingerprint=$(steps_fingerprint)
verified=$(sed -n 's/^fingerprint //p' "$HANDOFF/verified.txt" 2>/dev/null || true)
if [ $force -eq 0 ]; then
  [ -z "$missing" ] || die "steps not recorded yet: $(echo $missing)"
  [ "$verified" = "$fingerprint" ] || die "the steps changed since the last full scripts/verify-steps.sh run; run it again"
else
  [ -z "$missing" ] || note "warning (--force): steps not recorded yet: $(echo $missing)"
  [ "$verified" = "$fingerprint" ] || note "warning (--force): these steps have not passed a full verify-steps.sh run"
fi

stage="$HANDOFF/.stage"
share="$HANDOFF/share"
rm -rf "$stage" "$share"
mkdir -p "$stage" "$share"
trap 'rm -rf "$stage"' EXIT

# ---- ORDER.txt --------------------------------------------------------------

{
  echo "PATHMAXING ($repo_dir): GLOBAL COMMIT ORDER"
  echo "Repository: $url"
  echo
  echo "Commit strictly in this order. Your turn comes when the commit just"
  echo "before yours is pushed. After you push, post \"Cxx pushed\" in the"
  echo "team WhatsApp group so the next person can go."
  echo
  while IFS=$'\t' read -r id member folder title; do
    printf '%-4s  %-7s %s\n' "$id" "$member" "$title"
  done <<<"$steps"
} >"$share/ORDER.txt"

# ---- zips -------------------------------------------------------------------

make_zip() { # make_zip NAME: zips $stage/NAME into $share/NAME.zip
  local name=$1 py
  if command -v zip >/dev/null 2>&1; then
    (cd "$stage" && zip -qr -X "../share/$name.zip" "$name")
  elif py=$(command -v python3 || command -v python); then
    (cd "$stage" && "$py" -m zipfile -c "../share/$name.zip" "$name")
  else
    die "need zip or python to build the zips"
  fi
}

indent() { sed 's/^/   /; s/^   $//'; }

for member in $(all_members); do
  part=$(member_part "$member")
  full=$(member_full_name "$member")
  ptitle=$(part_title "$part")
  mine=$(awk -F'\t' -v m="$member" '$2 == m' <<<"$steps")
  if [ -z "$mine" ]; then
    note "warning: $member has no recorded steps; skipping their zip"
    continue
  fi
  ids=$(cut -f1 <<<"$mine" | paste -sd, - | sed 's/,/, /g')
  count=$(wc -l <<<"$mine" | tr -d ' ')
  dir="$stage/$member"
  mkdir -p "$dir/commits"
  cp "$share/ORDER.txt" "$dir/ORDER.txt"

  {
    echo "PATHMAXING: COMMIT INSTRUCTIONS FOR $(tr '[:lower:]' '[:upper:]' <<<"$full")"
    echo "Team: Not Qualified    Your part: Part $part, $ptitle"
    echo "Repository: $url"
    echo "Your commits: $ids ($count in total)"
    echo
    echo "Judges look at meaningful commits from every member, and they may ask you"
    echo "about your code. Read docs/parts/part$part-*.md before the demo."
    echo
    echo "================================================================"
    echo "ONE-TIME SETUP"
    echo "================================================================"
    echo "1. Install Git. On Windows, install \"Git for Windows\" and run every"
    echo "   command below in \"Git Bash\", not in CMD."
    if [ "$part" = 5 ]; then
      echo "   Also install Node.js 20 or newer (https://nodejs.org/)."
    else
      echo "   Also install Go 1.24 or newer (https://go.dev/dl/)."
    fi
    echo "2. Accept the GitHub invitation to the repository."
    if grep -q "^C01"$'\t' <<<"$mine"; then
      echo "3. Clone the repository now. It stays empty until you push C01, so"
      echo "   git will warn \"You appear to have cloned an empty repository\";"
      echo "   that is expected:"
    else
      echo "3. Wait until the first commit (C01) has been pushed, then clone the"
      echo "   repository:"
    fi
    echo "      git clone $url"
    echo "   This creates the folder \"$repo_dir\"."
    echo "4. Set your identity. Use the email on YOUR GitHub account, otherwise"
    echo "   your commits will not count as yours:"
    echo "      git config --global user.name  \"$full\""
    echo "      git config --global user.email \"<your GitHub email>\""
    echo "5. Unzip $member.zip into the SAME folder that contains \"$repo_dir\":"
    echo "      some-folder/"
    width=$((${#repo_dir} + 3))
    printf '        %-*s<- the repository\n' "$width" "$repo_dir/"
    printf '        %-*s<- this zip\n' "$width" "$member/"
    if [ "$part" != 5 ]; then
      echo
      echo "About the checks: run go build, go vet and go test exactly as written,"
      echo "WITHOUT -race. The race detector needs a C compiler (gcc), which you"
      echo "probably don't have. GitHub Actions runs the tests with -race on every"
      echo "push, so races are still caught."
    fi
    echo
    echo "================================================================"
    echo "FOR EACH OF YOUR COMMITS (in order, only when it is your turn)"
    echo "================================================================"
    echo "Your turn comes when the commit just before yours in ORDER.txt is"
    echo "pushed. Check the team WhatsApp group or the GitHub commit list."

    while IFS=$'\t' read -r id _ folder title; do
      src="$STEPS_DIR/$folder"
      cp -R "$src" "$dir/commits/$folder"
      rel="../$member/commits/$folder"
      echo
      echo "---------------------------------------------------------------"
      echo "$id  $title"
      echo "---------------------------------------------------------------"
      echo "Message (already in COMMIT_MSG.txt, nothing to type):"
      indent <"$src/COMMIT_MSG.txt"
      echo
      echo "Commands:"
      {
        echo "cd $repo_dir"
        if [ "$id" != C01 ]; then echo "git pull --rebase"; fi
        echo "cp -r $rel/files/. ."
        if [ -f "$src/DELETED.txt" ]; then
          echo "git rm --pathspec-from-file=$rel/DELETED.txt"
        fi
        if [ "$part" = 5 ]; then
          echo "cd web && npm ci && npm run build && npm test && cd .."
        else
          echo "go build ./... && go vet ./... && go test ./..."
        fi
        echo "git add --pathspec-from-file=$rel/FILES.txt"
        # Git for Windows records files as non-executable; mark the shell
        # scripts so they run directly on Linux and macOS clones.
        scripts=$(grep -E '\.sh$' "$src/FILES.txt" | paste -sd' ' - || true)
        if [ -n "$scripts" ]; then echo "git update-index --chmod=+x $scripts"; fi
        echo "git commit -F $rel/COMMIT_MSG.txt"
        if [ "$id" = C01 ]; then
          echo "git branch -M main"
          echo "git push -u origin main"
        else
          echo "git push"
        fi
        echo "cd .."
      } | indent
      echo "Then post \"$id pushed\" in the WhatsApp group."
    done <<<"$mine"

    echo
    echo "================================================================"
    echo "IF SOMETHING GOES WRONG"
    echo "================================================================"
    echo "- \"git push\" rejected: run  git pull --rebase  then  git push  again."
    echo "- Build or tests fail: do NOT push. Tell the team in WhatsApp, and"
    echo "  include the error message."
    echo "- Wrong file committed: do NOT force-push. Tell the team; the fix"
    echo "  becomes a new commit."
    echo "- Never commit files from another member's folder, and never commit .env."
  } >"$dir/INSTRUCTIONS.txt"

  make_zip "$member"
  echo "wrote handoff/share/$member.zip ($count commits)"
done

echo "wrote handoff/share/ORDER.txt"

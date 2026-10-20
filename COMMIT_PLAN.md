# COMMIT_PLAN.md: 15 commits from all 5 members

The hackathon judges whether commits are meaningful and come from all five members. The team chose **15 commits**: 5 from Nilay, 5 from Mayank, 2 from Manas, 2 from Karan and 1 from Mansi. Each commit is made by its owner from their own GitHub account.

The work was built and tested as 40 small steps (the "build steps" C01–C40 used in PARTS.md and in the migration file names). Those steps were then squashed into the 15 commits below. Every commit contains only its owner's files (plus `go.mod`/`go.sum`), and applying commits 1..k in order always builds and passes the tests.

## Rules for every commit

- **One real step per commit.** Each commit is a coherent feature, fix, test suite or doc. No empty commits, formatting-only commits, or work split up just to raise the count.
- **The repo works after every commit.** Applying C01..Ck in order must build and pass tests (`go build ./...`, `go vet ./...`, `go test ./...`, plus `npm run build` and `npm test` once `web/` exists). `scripts/verify-steps.sh` checks every prefix.
- **Owners commit only files in their own part**, with one exception: `go.mod` and `go.sum` travel with whichever commit adds a dependency.
- **Messages use Conventional Commits.** The title is exactly as listed below. The body is 2–4 lines that say what changed and how it was tested, written from the actual changes.

## Members and zips

| Member | Part | Zip | Commits | Commit numbers |
|---|---|---|---|---|
| Nilay Joshi | 3: Redirect engine & trust layer | `Nilay.zip` | 5 | C03, C06, C08, C10, C15 |
| Mayank Joshi | 4: Data, API & governance | `Mayank.zip` | 5 | C04, C07, C09, C11, C14 |
| Manas Singh Bisht | 1: Foundation & tooling | `Manas.zip` | 2 | C01, C13 |
| Karan Pandey | 5: Frontend & deployment | `Karan.zip` | 2 | C05, C12 |
| Mansi Pandey | 2: Database setup & load testing | `Mansi.zip` | 1 | C02 |

## Global commit order

Commits must be pushed in this order. Each one depends only on the commits before it. "Build steps" shows which of the original steps each commit contains.

| # | Member | Folder | Commit title | Build steps |
|---|---|---|---|---|
| C01 | Manas | C01-foundation | `chore: bootstrap server registry, link rules and team docs` | C01, C05 |
| C02 | Mansi | C02-database | `feat(db): add migrations, core schema, demo seed and load tests` | C03, C18, C31, C37 |
| C03 | Nilay | C03-redirect-engine | `feat(redirect): add shared contracts, rule engine and short-link redirects` | C02, C06, C08 |
| C04 | Mayank | C04-store-clicks | `feat(store): add Postgres link store and async click recording` | C07, C09 |
| C05 | Karan | C05-web-app | `feat(web): add web app with login, dashboard, link wizard and link details` | C04, C10, C15, C21 |
| C06 | Nilay | C06-cache-trust-checks | `feat(trust): add link cache, threat-feed clients and SSRF-safe checker` | C12, C14, C17 |
| C07 | Mayank | C07-auth-links-api | `feat(api): add accounts, sessions and the links API` | C11, C13 |
| C08 | Nilay | C08-trust-states | `feat(trust): add trust states, auto-quarantine and the re-scan scheduler` | C20, C23 |
| C09 | Mayank | C09-analytics | `feat(analytics): add campaigns, tags, folders and the analytics API` | C16, C19, C22 |
| C10 | Nilay | C10-classifier-alerts | `feat(classify): add honest-clicks classifier, alerts and transparency page` | C25, C29, C39 |
| C11 | Mayank | C11-qr-bio-bulk-admin | `feat(api): add QR codes, bio pages, CSV import and admin governance` | C24, C26, C30 |
| C12 | Karan | C12-web-screens-deploy | `feat(web): add campaign, import, bio and admin screens, redesign and deploy config` | C27, C32, C33, C36, C40 |
| C13 | Manas | C13-ci-docs | `ci: add GitHub Actions and Dockerfile, finish README and part 1 guide` | C28, C38 |
| C14 | Mayank | C14-docs-data-api | `docs(api): add part 4 owner guide` | C35 |
| C15 | Nilay | C15-docs-engine-trust | `docs(trust): add part 3 owner guide with classifier accuracy` | C34 |

Migration files keep the build-step numbers in their names (`C03_core_schema.sql`, `C09_click_events.sql`, `C20_trust.sql`, ...). The runner applies any file it hasn't applied yet in name order, and these migrations don't depend on each other's order beyond C03, so a database that receives them commit by commit ends up the same as a fresh one.

**Not built:** India mode was optional and is left out of this build.

**Fixes after handout:** once commits have been handed out, never rewrite them. Any later fix becomes a new commit, numbered after the last one, by the owner of the files it changes, with a `fix(<scope>): …` title.

## How the handoff is produced

Each step is recorded as a snapshot instead of a git commit, so members commit their own work.

1. **Build in the order above.** After finishing step Cxx and passing all checks, run the following, with the 2–4 line commit body on stdin:
   `scripts/snapshot-step.sh <Cxx> <Member> <folder-slug> "<commit title>" < body.txt`
2. **`scripts/snapshot-step.sh`** works as follows:
   - Checks the step against the order table above: the member, the folder name and the exact title.
   - Compares the working tree with a shadow copy in `.handoff-shadow/`.
   - Excluded from the comparison: `handoff/`, `.handoff-shadow/`, `.env`, `node_modules/`, `web/dist/`, and build output.
   - Copies new and changed files, with their full contents and repo-relative paths, into `handoff/steps/<Cxx>-<slug>/files/`.
   - Writes into the step folder:
     - `FILES.txt`: one repo-relative path per line.
     - `DELETED.txt`: only if files were removed.
     - `COMMIT_MSG.txt`: the title, a blank line, then the body.
   - Updates the shadow copy and the step index `handoff/steps.tsv`.
   - Fails if any changed file belongs to a different member's part, except `go.mod` and `go.sum`, and on any unowned file.
   - Recorded steps are frozen: it refuses a step number lower than the last one. Running it again for the most recent step redoes that step. A bug found in an older step is fixed in its owner's next step, and that commit's body says so.
3. **`scripts/verify-steps.sh`** applies the steps C01..Cn one after another into an empty temp directory and runs the checks after each one. It fails on the first step that doesn't build or test. After the last step it checks that the replayed tree equals the working tree, so nothing un-snapshotted is left out. A full run records `handoff/verified.txt`.
4. **`scripts/make-handoff.sh <REPO_URL>`** writes `handoff/share/` with `Nilay.zip`, `Mayank.zip`, `Karan.zip`, `Mansi.zip`, `Manas.zip`, and `ORDER.txt` (the global order, for the WhatsApp group). It refuses to run unless every step above is recorded and the last full verify run saw exactly these steps. Each zip unpacks to:

```
<Member>/
  INSTRUCTIONS.txt          # filled-in copy of the template below
  ORDER.txt                 # who commits when
  commits/
    C07-auth/
      COMMIT_MSG.txt
      FILES.txt
      DELETED.txt           # only if needed
      files/<repo-relative paths...>
    C12-links-api/
    ...
```

## INSTRUCTIONS.txt template

`make-handoff.sh` fills in `<...>` for each member and lists that member's commits, each with its title and full message.

```
PATHMAXING: COMMIT INSTRUCTIONS FOR <MEMBER FULL NAME>
Team: Not Qualified    Your part: Part <N>, <part title>
Repository: <REPO_URL>
Your commits: <list, e.g. C02, C06, C08, C12, C14, C17, C20, C23, C25, C29, C34> (<count> in total)

Judges look at meaningful commits from every member, and they may ask you
about your code. Read docs/parts/part<N>-*.md before the demo.

================================================================
ONE-TIME SETUP
================================================================
1. Install Git. On Windows, install "Git for Windows" and run every
   command below in "Git Bash", not in CMD.
   <Parts 1-4: also install Go 1.24 or newer.>
   <Part 5: also install Node.js 20 or newer.>
2. Accept the GitHub invitation to the repository.
3. <Manas: clone the repository now; it stays empty until you push C01.>
   <Everyone else: wait until C01 has been pushed, then clone it.>
      git clone <REPO_URL>
   This creates the folder "NotQualified-PathMaxing".
4. Set your identity. Use the email on YOUR GitHub account, otherwise
   your commits will not count as yours:
      git config --global user.name  "<Member Full Name>"
      git config --global user.email "<your GitHub email>"
5. Unzip <Member>.zip into the SAME folder that contains "NotQualified-PathMaxing":
      some-folder/
        NotQualified-PathMaxing/  <- the repository
        <Member>/                 <- this zip

<Parts 1-4:>
About the checks: run go build, go vet and go test exactly as written,
WITHOUT -race. The race detector needs a C compiler (gcc), which you
probably don't have. GitHub Actions runs the tests with -race on every
push, so races are still caught.

================================================================
FOR EACH OF YOUR COMMITS (in order, only when it is your turn)
================================================================
Your turn comes when the commit just before yours in ORDER.txt is
pushed. Check the team WhatsApp group or the GitHub commit list.

---------------------------------------------------------------
<Cxx>  <commit title>
---------------------------------------------------------------
Message (already in COMMIT_MSG.txt, nothing to type):
   <title>

   <body>

Commands:
   cd NotQualified-PathMaxing
   git pull --rebase                       <not for C01: the repo is still empty>
   cp -r ../<Member>/commits/<Cxx-slug>/files/. .
   <only if DELETED.txt exists:>
   git rm --pathspec-from-file=../<Member>/commits/<Cxx-slug>/DELETED.txt
   <Parts 1-4:> go build ./... && go vet ./... && go test ./...
   <Part 5:>   cd web && npm ci && npm run build && npm test && cd ..
   git add --pathspec-from-file=../<Member>/commits/<Cxx-slug>/FILES.txt
   <only if the commit adds shell scripts, so they stay executable on Linux/macOS:>
   git update-index --chmod=+x <the .sh files>
   git commit -F ../<Member>/commits/<Cxx-slug>/COMMIT_MSG.txt
   git push                                <C01: git branch -M main, then git push -u origin main>
   cd ..
Then post "<Cxx> pushed" in the WhatsApp group.

<repeat for every commit of this member>

================================================================
IF SOMETHING GOES WRONG
================================================================
- "git push" rejected: run  git pull --rebase  then  git push  again.
- Build or tests fail: do NOT push. Tell the team in WhatsApp, and
  include the error message.
- Wrong file committed: do NOT force-push. Tell the team; the fix
  becomes a new commit.
- Never commit files from another member's folder, and never commit .env.
```

#!/bin/sh
# Offline tests of the release machinery: release-gate.sh decides which commit
# is a release, publish-release.sh publishes one, and the dist workflow lets
# only the gate's answer reach the publish job. Real git runs against local
# repositories; the GitHub CLI is a fake first on PATH. Needs git and a POSIX
# shell, nothing else.
#
#   scripts/test-release.sh
set -u

root=$(cd "$(dirname "$0")/.." && pwd -P)
work=$(mktemp -d "${TMPDIR:-/tmp}/release-test.XXXXXX") || exit 2
trap 'rm -rf "$work"' EXIT

# No user or system configuration, and one identity for every commit.
GIT_CONFIG_GLOBAL=/dev/null
GIT_CONFIG_SYSTEM=/dev/null
GIT_AUTHOR_NAME=tester
GIT_AUTHOR_EMAIL=tester@example.com
GIT_COMMITTER_NAME=tester
GIT_COMMITTER_EMAIL=tester@example.com
GIT_TERMINAL_PROMPT=0
export GIT_CONFIG_GLOBAL GIT_CONFIG_SYSTEM GIT_AUTHOR_NAME GIT_AUTHOR_EMAIL \
  GIT_COMMITTER_NAME GIT_COMMITTER_EMAIL GIT_TERMINAL_PROMPT

failures=0
checks=0

ok() {
  checks=$((checks + 1))
  echo "ok: $1"
}
fail() {
  checks=$((checks + 1))
  failures=$((failures + 1))
  echo "FAIL: $1" >&2
}
# aborted <name>: the last publish() stopped with a failure status.
aborted() {
  if [ "$pub_code" -ne 0 ]; then ok "$1"; else fail "$1"; fi
}
# expect <name> <want> <got>
expect() {
  if [ "$2" = "$3" ]; then
    ok "$1"
  else
    fail "$1"
    printf '  want: %s\n  got:  %s\n' "$2" "$3" >&2
  fi
}

# A repository with one commit per call of commit(), in the current directory.
newrepo() {
  repo=$work/$1
  mkdir -p "$repo"
  git init -q "$repo"
  git -C "$repo" symbolic-ref HEAD refs/heads/main
  cd "$repo" || exit 2
  n=0
}
# commit <subject> [version]: a commit that writes VERSION when a version is
# given and changes a file otherwise; whatever else is in the working tree goes
# in as well.
commit() {
  n=$((n + 1))
  if [ $# -ge 2 ]; then
    printf '%s\n' "$2" >VERSION
  else
    echo "$n" >change
  fi
  git add -A
  git commit -q -m "$1"
  git rev-parse HEAD
}

# gate <sha>: runs release-gate.sh in the current repository; sets gate_out
# and gate_code.
gate() {
  gate_out=$(sh "$root/scripts/release-gate.sh" "$1" 2>"$work/gate.err")
  gate_code=$?
}

# --- release-gate.sh --------------------------------------------------------

newrepo gate-release
commit "chore: bootstrap" 1.6.0 >/dev/null
commit "feat: something" >/dev/null
release=$(commit "chore: release 1.6.1" 1.6.1)
gate "$release"
expect "gate: a release commit exits 0" 0 "$gate_code"
expect "gate: a release commit names its version" "release=true
version=1.6.1" "$gate_out"

# not_release <name> <want-reason> <sha>
not_release() {
  gate "$3"
  expect "gate: $1 exits 0" 0 "$gate_code"
  expect "gate: $1 is not a release" "release=false
reason=$2" "$gate_out"
}

newrepo gate-ordinary
commit "chore: bootstrap" 1.6.0 >/dev/null
sha=$(commit "feat: something")
not_release "an ordinary commit" 'the subject is not "chore: release <version>"' "$sha"

for subject in "Chore: release 1.6.1" "chore(release): 1.6.1" "chore: release"; do
  newrepo gate-subject
  commit "chore: bootstrap" 1.6.0 >/dev/null
  sha=$(commit "$subject" 1.6.1)
  not_release "the subject '$subject'" 'the subject is not "chore: release <version>"' "$sha"
done

for subject in "chore: release v1.6.1" "chore: release 1.6.1 (#12)" "chore: release  1.6.1"; do
  newrepo gate-subject-version
  commit "chore: bootstrap" 1.6.0 >/dev/null
  sha=$(commit "$subject" 1.6.1)
  not_release "the subject '$subject'" "VERSION does not match the subject" "$sha"
done

newrepo gate-other-file
commit "chore: bootstrap" 1.6.0 >/dev/null
echo x >extra
sha=$(commit "chore: release 1.6.1" 1.6.1)
not_release "a release commit that also adds a file" "the commit changes more than VERSION" "$sha"

newrepo gate-nested
commit "chore: bootstrap" 1.6.0 >/dev/null
mkdir sub
echo 1.6.1 >sub/VERSION
sha=$(commit "chore: release 1.6.1" 1.6.1)
not_release "a release commit that also changes a nested VERSION" "the commit changes more than VERSION" "$sha"

newrepo gate-no-version-change
commit "chore: bootstrap" 1.6.0 >/dev/null
sha=$(commit "chore: release 1.6.1")
not_release "a release commit that leaves VERSION alone" "the commit changes more than VERSION" "$sha"

newrepo gate-mismatch
commit "chore: bootstrap" 1.6.0 >/dev/null
sha=$(commit "chore: release 1.6.1" 1.6.2)
not_release "a subject that names another version than VERSION" "VERSION does not match the subject" "$sha"

newrepo gate-multiline
commit "chore: bootstrap" 1.6.0 >/dev/null
sha=$(commit "chore: release 1.6.1" "1.6.1
garbage")
not_release "a VERSION with a second line" "VERSION does not match the subject" "$sha"

newrepo gate-root
sha=$(commit "chore: release 1.6.1" 1.6.1)
not_release "a root commit" "the commit has no parent" "$sha"

for version in 1.6 v1.6.1 01.6.1 1.6.1-; do
  newrepo gate-semver
  commit "chore: bootstrap" 1.6.0 >/dev/null
  sha=$(commit "chore: release $version" "$version")
  not_release "the version '$version'" "VERSION is not a semantic version" "$sha"
done

newrepo gate-prerelease
commit "chore: bootstrap" 1.6.0 >/dev/null
sha=$(commit "chore: release 1.7.0-rc.1" 1.7.0-rc.1)
not_release "a pre-release version" "VERSION has a pre-release part, and dist takes none" "$sha"

newrepo gate-unknown
commit "chore: bootstrap" 1.6.0 >/dev/null
gate 0000000000000000000000000000000000000000
expect "gate: an unknown commit exits 2" 2 "$gate_code"

# --- publish-release.sh -----------------------------------------------------

# The fake GitHub CLI. It logs each call to $ORDER_LOG, answers the release
# lookup as FAKEGH_API says (found, 404, 401, 500 or neterr; 404 when unset),
# keeps the notes file of `release create` in $FAKEGH_NOTES, and refuses a
# create that does not ask to verify the tag or whose tag is not on the remote
# yet, as the real one does.
mkdir "$work/bin"
cat >"$work/bin/gh" <<'FAKE'
#!/bin/sh
line=gh
prev=
for a in "$@"; do
  if [ "$prev" = "--notes-file" ]; then
    [ -z "${FAKEGH_NOTES:-}" ] || cp "$a" "$FAKEGH_NOTES"
    a=FILE
  fi
  line="$line $a"
  prev=$a
done
echo "$line" >>"$ORDER_LOG"
case $1 in
api)
  case ${FAKEGH_API:-404} in
  found) echo '{"tag_name":"found"}' ;;
  404) echo "gh: Not Found (HTTP 404)" >&2; exit 1 ;;
  401) echo "gh: Bad credentials (HTTP 401)" >&2; exit 1 ;;
  500) echo "gh: Server Error (HTTP 500)" >&2; exit 1 ;;
  neterr) echo "error connecting to api.github.com" >&2; exit 1 ;;
  esac
  ;;
release)
  [ "$2" = create ] || exit 99
  case " $* " in
  *" --verify-tag "*) ;;
  *) echo "fake gh: release create without --verify-tag" >&2; exit 1 ;;
  esac
  if ! git ls-remote "$FAKEGH_ORIGIN" "refs/tags/$3" | grep -q .; then
    echo "fake gh: tag $3 is not on the remote" >&2
    exit 1
  fi
  exit "${FAKEGH_CREATE_EXIT:-0}"
  ;;
*) exit 99 ;;
esac
FAKE
chmod +x "$work/bin/gh"

# A git that fails the listing of origin's dist branch when FAKEGIT_FAIL_LS_DIST
# is set, and is the real git otherwise.
real_git=$(command -v git)
cat >"$work/bin/git" <<FAKE
#!/bin/sh
if [ -n "\${FAKEGIT_FAIL_LS_DIST:-}" ] && [ "\$1 \$2 \$3" = "ls-remote origin refs/heads/dist" ]; then
  echo "fatal: unable to access origin" >&2
  exit 128
fi
exec "$real_git" "\$@"
FAKE
chmod +x "$work/bin/git"

# newpub <name>: a repository with a bare origin that logs each ref pushed to
# it in $log, and a dist tree in $tree.
newpub() {
  newrepo "$1"
  origin=$work/$1.origin.git
  git init -q --bare "$origin"
  git -C "$origin" symbolic-ref HEAD refs/heads/main
  git remote add origin "$origin"
  log=$work/$1.log
  : >"$log"
  printf '#!/bin/sh\necho "push $1" >>"%s"\n' "$log" >"$origin/hooks/update"
  chmod +x "$origin/hooks/update"
  tree=$work/$1.tree
  mkdir -p "$tree/.claude-plugin"
  echo '{"name":"x"}' >"$tree/.claude-plugin/plugin.json"
  echo "built readme" >"$tree/README.md"
  notes=$work/$1.notes
}
# history: bootstrap, a feature, release 1.6.1 (in $a), pushed to origin.
history() {
  commit "chore: bootstrap" 1.6.0 >/dev/null
  commit "feat: something" >/dev/null
  a=$(commit "chore: release 1.6.1" 1.6.1)
  git push -q origin main 2>/dev/null
  : >"$log"
}
# publish <sha> [VAR=value...]: runs publish-release.sh against $origin; sets pub_code. The calls the fake gh and the origin saw are in $log.
# The extra settings reach only that run: a setting put before a call to this function stays set after it in bash 3.2's POSIX mode.
publish() {
  pub_sha=$1
  shift
  PATH="$work/bin:$PATH" ORDER_LOG=$log FAKEGH_ORIGIN=$origin FAKEGH_NOTES=$notes \
    env "$@" sh "$root/scripts/publish-release.sh" "$pub_sha" "$tree" >"$work/pub.out" 2>"$work/pub.err"
  pub_code=$?
}
ref() { git -C "$origin" rev-parse --verify --quiet "$1" || true; }
calls() { sed 's/ --notes-file FILE//' "$log"; }

lookup="gh api repos/{owner}/{repo}/releases/tags/v1.6.1"

newpub publish-new
history
publish "$a"
expect "publish: a new release exits 0" 0 "$pub_code"
expect "publish: the lookup, the tag, the release and dist go in this order" "$lookup
push refs/tags/v1.6.1
gh release create v1.6.1 --verify-tag --title v1.6.1
push refs/heads/dist" "$(calls)"
tagged=$(ref refs/tags/v1.6.1)
expect "publish: dist is the tagged commit" "$tagged" "$(ref refs/heads/dist)"
expect "publish: the tagged commit is an orphan dist build of the source" "dist: build of $a|" \
  "$(git -C "$origin" log -1 --format='%s|%P' "$tagged")"
expect "publish: the tagged commit holds the dist tree" "built readme" "$(git -C "$origin" show "$tagged:README.md")"
expect "publish: the build commit is by the bot" "github-actions[bot]" "$(git -C "$origin" log -1 --format=%an "$tagged")"
expect "publish: the notes name the source and list its feature" "Build of $a.
### Features
- feat: something ($(git rev-parse --short "$(git rev-list --grep='^feat: something' main)"))" \
  "$(grep -v '^$' "$notes")"

for mode in 401 500 neterr; do
  newpub publish-lookup-$mode
  history
  publish "$a" FAKEGH_API=$mode
  aborted "publish: a failed lookup ($mode) aborts"
  expect "publish: a failed lookup ($mode) leaves everything as it was" "$lookup" "$(calls)"
  expect "publish: a failed lookup ($mode) creates no tag" "" "$(ref refs/tags/v1.6.1)"
  expect "publish: a failed lookup ($mode) creates no dist" "" "$(ref refs/heads/dist)"
done

newpub publish-not-release
history
sha=$(commit "feat: later")
publish "$sha"
expect "publish: a commit that is not a release exits 2" 2 "$pub_code"
expect "publish: a commit that is not a release calls nothing" "" "$(calls)"
expect "publish: a commit that is not a release pushes nothing" "" "$(ref refs/heads/dist)"

# A run that fails after the tag is pushed, rerun at the same source commit.
newpub publish-rerun
history
publish "$a" FAKEGH_CREATE_EXIT=1
aborted "publish: a failed release creation fails the run"
tagged=$(ref refs/tags/v1.6.1)
expect "publish: a failed release creation leaves dist alone" "" "$(ref refs/heads/dist)"
: >"$log"
echo "a rebuilt readme" >"$tree/README.md"
publish "$a"
expect "publish: the rerun exits 0" 0 "$pub_code"
expect "publish: the rerun creates the release and moves dist, and pushes no tag" "$lookup
gh release create v1.6.1 --verify-tag --title v1.6.1
push refs/heads/dist" "$(calls)"
expect "publish: the rerun leaves the tag where it was" "$tagged" "$(ref refs/tags/v1.6.1)"
expect "publish: the rerun does not rebuild the tag" "built readme" "$(git -C "$origin" show "$tagged:README.md")"
expect "publish: the rerun moves dist to the tagged commit" "$tagged" "$(ref refs/heads/dist)"

# The release exists and dist is missing: only dist is left to do.
: >"$log"
git -C "$origin" update-ref -d refs/heads/dist
publish "$a" FAKEGH_API=found
expect "publish: a run with only dist missing exits 0" 0 "$pub_code"
expect "publish: a run with only dist missing moves dist and nothing else" "$lookup
push refs/heads/dist" "$(calls)"
expect "publish: a run with only dist missing puts dist on the tagged commit" "$tagged" "$(ref refs/heads/dist)"

# Everything is done: a rerun changes nothing.
: >"$log"
publish "$a" FAKEGH_API=found
expect "publish: a rerun of a finished release exits 0" 0 "$pub_code"
expect "publish: a rerun of a finished release only looks the release up" "$lookup" "$(calls)"

# A tag that is not a dist build of the source is never reused or moved.
newpub publish-tag-elsewhere
history
other=$(git rev-parse HEAD~1)
git push -q origin "$other:refs/tags/v1.6.1" 2>/dev/null
: >"$log"
publish "$a"
aborted "publish: a tag on an ordinary commit aborts"
expect "publish: a tag on an ordinary commit stays where it was" "$other" "$(ref refs/tags/v1.6.1)"
expect "publish: a tag on an ordinary commit creates no release and no dist" "$lookup" "$(calls)"

newpub publish-tag-other-build
history
other=$(printf 'x' | git commit-tree "$(git rev-parse "$a^{tree}")" -m "dist: build of 0000000000000000000000000000000000000001")
git push -q origin "$other:refs/tags/v1.6.1" 2>/dev/null
: >"$log"
publish "$a"
aborted "publish: a tag on a build of another source aborts"
expect "publish: a tag on a build of another source stays where it was" "$other" "$(ref refs/tags/v1.6.1)"
expect "publish: a tag on a build of another source creates no release and no dist" "$lookup" "$(calls)"

newpub publish-release-without-tag
history
publish "$a" FAKEGH_API=found
aborted "publish: a release without its tag aborts"
expect "publish: a release without its tag pushes nothing" "$lookup" "$(calls)"

newpub publish-origin-unreachable
history
git remote set-url origin "$work/no-such-origin.git"
publish "$a"
aborted "publish: an unreachable origin aborts"
expect "publish: an unreachable origin creates no release" "$lookup" "$(calls)"

# --- what dist holds when a release is published ---------------------------

# dist_of <subject>: puts an orphan commit with that subject on origin's dist.
dist_of() {
  built=$(printf 'x' | git commit-tree "$(git hash-object -t tree /dev/null)" -m "$1")
  git push -q --force origin "$built:refs/heads/dist" 2>/dev/null
  : >"$log"
}

# Dist holds the build of an earlier commit: the release replaces it.
newpub publish-dist-older
history
dist_of "dist: build of $(git rev-parse HEAD~1)"
publish "$a"
expect "publish: a release over an older build exits 0" 0 "$pub_code"
expect "publish: a release over an older build moves dist" "$(ref refs/tags/v1.6.1)" "$(ref refs/heads/dist)"

# Dist holds an earlier build of the same commit: the tagged build replaces it.
newpub publish-dist-same-source
history
dist_of "dist: build of $a"
publish "$a"
expect "publish: a release over another build of its source exits 0" 0 "$pub_code"
expect "publish: a release over another build of its source moves dist" "$(ref refs/tags/v1.6.1)" "$(ref refs/heads/dist)"

# Dist holds the build of a newer release: an older release run (a rerun, or
# one that GitHub dropped from the queue) finishes its tag and release and
# leaves dist alone.
newpub publish-dist-newer
history
b=$(commit "chore: release 1.6.2" 1.6.2)
git push -q origin main 2>/dev/null
dist_of "dist: build of $b"
newer=$(ref refs/heads/dist)
publish "$a"
expect "publish: an older release under a newer dist exits 0" 0 "$pub_code"
expect "publish: an older release under a newer dist tags and releases, and leaves dist" "$lookup
push refs/tags/v1.6.1
gh release create v1.6.1 --verify-tag --title v1.6.1" "$(calls)"
expect "publish: an older release under a newer dist leaves dist where it was" "$newer" "$(ref refs/heads/dist)"

# Dist holds a build that is neither before nor after the release.
newpub publish-dist-diverged
history
git checkout -q -b side "$a~1"
side=$(commit "feat: elsewhere")
git checkout -q main
dist_of "dist: build of $side"
publish "$a"
aborted "publish: a dist built from another line of history aborts"
expect "publish: a dist built from another line of history changes nothing" "" "$(calls | sed '/^gh api/d')"

newpub publish-dist-unknown-source
history
dist_of "dist: build of 0000000000000000000000000000000000000001"
publish "$a"
aborted "publish: a dist built from a commit this checkout lacks aborts"
expect "publish: a dist built from a commit this checkout lacks changes nothing" "" "$(calls | sed '/^gh api/d')"

newpub publish-dist-unrecognised
history
dist_of "something else"
publish "$a"
aborted "publish: a dist commit of no known form aborts"
expect "publish: a dist commit of no known form changes nothing" "" "$(calls | sed '/^gh api/d')"

# A listing of dist that fails is not a dist that is missing: the run stops
# before it tags or releases anything.
newpub publish-dist-list-fails
history
publish "$a" FAKEGIT_FAIL_LS_DIST=1
aborted "publish: a failed listing of dist aborts"
expect "publish: a failed listing of dist stops before any tag or release" "$lookup" "$(calls)"
expect "publish: a failed listing of dist creates no tag" "" "$(ref refs/tags/v1.6.1)"
expect "publish: a failed listing of dist says what failed" 1 "$(grep -c '^publish-release: cannot list the branches of origin$' "$work/pub.err")"

# main has moved on since the release commit: the release still publishes, now
# or on a rerun, because what stops a stale build is dist's own source.
newpub publish-main-moved-on
history
commit "feat: after the release" >/dev/null
git push -q origin main 2>/dev/null
: >"$log"
publish "$a"
expect "publish: a release that main has moved past exits 0" 0 "$pub_code"
expect "publish: a release that main has moved past still moves dist" "$(ref refs/tags/v1.6.1)" "$(ref refs/heads/dist)"

# --- .github/workflows/dist.yml ---------------------------------------------

wf=$root/.github/workflows/dist.yml
ci_wf=$root/.github/workflows/ci.yml

# block <start-regex> <file>: the lines from the first one matching the regex
# up to the next line indented no deeper than it.
block() {
  awk -v start="$1" '
    found && $0 !~ /^[ ]*$/ { match($0, /^ */); if (RLENGTH <= depth) exit }
    !found && $0 ~ start { found = 1; match($0, /^ */); depth = RLENGTH }
    found { print }
  ' "$2"
}
has() { printf '%s\n' "$1" | grep -Fq -- "$2"; }
wf_has() { # <name> <text> <block>
  if has "$3" "$2"; then ok "workflow: $1"; else fail "workflow: $1 (no '$2')"; fi
}
wf_lacks() { # <name> <text> <block>
  if has "$3" "$2"; then fail "workflow: $1 (has '$2')"; else ok "workflow: $1"; fi
}

whole=$(cat "$wf")
check_job=$(block '^  check:' "$wf")
publish_job=$(block '^  publish:' "$wf")
ci_job=$(block '^  ci:' "$wf")
top_permissions=$(block '^permissions:' "$wf")
ci_triggers=$(block '^on:' "$ci_wf")

wf_has "a push to main triggers it" "branches: [main]" "$whole"
expect "workflow: no concurrency group covers the whole workflow" "" "$(grep -n '^concurrency:' "$wf" || true)"
wf_has "the workflow can only read by default" "contents: read" "$top_permissions"
wf_lacks "no run cancels another in progress" "cancel-in-progress: true" "$whole"
wf_lacks "the workflow file pushes nothing itself" "git push" "$whole"
wf_has "the check job exists" "runs-on" "$check_job"
wf_has "the check job asks the gate" "scripts/release-gate.sh" "$check_job"
wf_has "the check job hands the gate's answer on" 'release: ${{ steps.gate.outputs.release }}' "$check_job"
wf_lacks "the check job cannot write" "contents: write" "$check_job"
wf_has "the ci job calls the ci workflow" "uses: ./.github/workflows/ci.yml" "$ci_job"
wf_has "the ci workflow can be called" "workflow_call:" "$ci_triggers"
wf_has "the ci workflow still runs on a push" "push:" "$ci_triggers"
wf_has "the ci workflow still runs on a pull request" "pull_request:" "$ci_triggers"
wf_has "the publish job waits for the checks and the ci job" "needs: [check, ci]" "$publish_job"
wf_has "only a release enters the publish job" "if: needs.check.outputs.release == 'true'" "$publish_job"
wf_has "the publish job has a group of its own" "group: dist-publish" "$publish_job"
wf_has "the publish job never cancels a run in progress" "cancel-in-progress: false" "$publish_job"
wf_has "the publish job can write" "contents: write" "$publish_job"
wf_has "the publish job runs the publish script" "scripts/publish-release.sh" "$publish_job"
wf_has "the publish job scans the tree it publishes" "piiscan" "$publish_job"

echo "$checks checks, $failures failed"
[ "$failures" -eq 0 ]

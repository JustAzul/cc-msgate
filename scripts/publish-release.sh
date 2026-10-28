#!/bin/sh
# Publish the release at a source commit: its dist build as one orphan commit,
# the immutable tag v<version> on that commit, the GitHub release of the tag,
# and the dist branch moved to that commit, last. Runs inside the repository,
# with origin pushable and gh signed in. The source commit must pass
# release-gate.sh.
#
#   scripts/publish-release.sh <source-sha> <dist-tree>
#
# Every step is skipped when it is already done, so rerunning a failed run at
# the same source commit finishes what is missing:
#
#   - The release is looked up by its exact tag through the API. Found means
#     it exists; HTTP 404 means it does not; any other failure stops the run.
#   - A tag that exists is reused after checking that it points at a dist build
#     of the source commit. The tree is then not used, the tag is not moved,
#     and a tag on anything else stops the run.
#   - dist moves to the tagged commit unless it already holds a build of a
#     commit after the source, which means a newer release is on dist: then
#     the run still tags and releases and leaves dist alone. A dist of unknown
#     origin, or built from another line of history, stops the run before it
#     changes anything. main moving on does not matter, because what keeps an
#     older run from overwriting dist is the source dist holds.
set -eu

die() {
  echo "publish-release: $*" >&2
  exit "${2:-1}"
}

[ $# -eq 2 ] || die "usage: publish-release.sh <source-sha> <dist-tree>" 2
here=$(dirname "$0")
source=$(git rev-parse --verify --quiet "$1^{commit}") || die "$1 is not a commit" 2
tree=$2
[ -d "$tree" ] || die "$tree is not a directory" 2

verdict=$(sh "$here/release-gate.sh" "$source") || die "the release gate failed" 2
case $verdict in
release=true*) ;;
*) die "$source is not a release: $(printf '%s' "$verdict" | tr '\n' ' ')" 2 ;;
esac
version=$(printf '%s\n' "$verdict" | sed -n 's/^version=//p')
tag=v$version
subject="dist: build of $source"

if found=$(gh api "repos/{owner}/{repo}/releases/tags/$tag" 2>&1); then
  release=found
elif printf '%s\n' "$found" | grep -q '(HTTP 404)'; then
  release=absent
else
  die "the release lookup for $tag failed: $found"
fi

scratch=$(mktemp -d "${TMPDIR:-/tmp}/publish-release.XXXXXX")
trap 'rm -rf "$scratch"' EXIT

current=$(git ls-remote origin refs/heads/dist | cut -f1) || die "cannot list the branches of origin"
move_dist=yes
if [ -n "$current" ]; then
  git fetch -q origin refs/heads/dist || die "cannot fetch dist"
  held=$(git log -1 --format=%s FETCH_HEAD)
  held=${held#dist: build of }
  printf '%s\n' "$held" | grep -Eq '^[0-9a-f]{40}$' || die "dist holds a commit that is not a dist build: $current"
  git cat-file -e "$held^{commit}" 2>/dev/null ||
    die "dist holds a build of $held, which this checkout does not have; fetch the full history"
  if [ "$held" = "$source" ] || git merge-base --is-ancestor "$held" "$source"; then
    :
  elif git merge-base --is-ancestor "$source" "$held"; then
    move_dist=no
    echo "publish-release: dist holds a build of $held, after $source; a newer release is on dist, so it stays"
  else
    die "dist holds a build of $held, which is neither before nor after $source"
  fi
fi

tag_line=$(git ls-remote origin "refs/tags/$tag") || die "cannot list the tags of origin"
if [ -n "$tag_line" ]; then
  git fetch -q origin "refs/tags/$tag" || die "cannot fetch $tag"
  dist=$(git rev-parse --verify "FETCH_HEAD^{commit}")
  [ "$(git log -1 --format=%s "$dist")" = "$subject" ] ||
    die "tag $tag points at $dist, which is not a dist build of $source; a pushed tag is never moved"
else
  [ "$release" = absent ] || die "release $tag exists but its tag does not"
  gitdir=$(git rev-parse --absolute-git-dir)
  (cd "$tree" && GIT_INDEX_FILE=$scratch/index git --git-dir="$gitdir" --work-tree=. add -A -f .) ||
    die "cannot stage $tree"
  built=$(GIT_INDEX_FILE=$scratch/index git write-tree)
  bot="github-actions[bot]"
  email="41898282+github-actions[bot]@users.noreply.github.com"
  dist=$(GIT_AUTHOR_NAME=$bot GIT_AUTHOR_EMAIL=$email GIT_COMMITTER_NAME=$bot GIT_COMMITTER_EMAIL=$email \
    git commit-tree "$built" -m "$subject")
  git push -q origin "$dist:refs/tags/$tag" || die "cannot push $tag"
fi

if [ "$release" = absent ]; then
  sh "$here/release-notes.sh" "$source" >"$scratch/notes"
  gh release create "$tag" --verify-tag --title "$tag" --notes-file "$scratch/notes"
fi

if [ "$move_dist" = yes ] && [ "$current" != "$dist" ]; then
  git push -q --force-with-lease="refs/heads/dist:$current" origin "$dist:refs/heads/dist" ||
    die "cannot move dist to $dist"
fi

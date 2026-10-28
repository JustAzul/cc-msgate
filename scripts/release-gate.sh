#!/bin/sh
# Decide whether a commit is a release. A release is a commit whose subject is
# exactly `chore: release <version>`, whose diff against its first parent
# touches only VERSION, and whose VERSION is that <version>, a Semantic
# Versioning 2.0.0 version with no pre-release part (dist takes none). Only a
# release publishes. Runs inside the repository.
#
#   scripts/release-gate.sh <commit>
#
# Prints `release=true` and `version=<version>`, or `release=false` and
# `reason=<why>`, one per line, so the output can go to $GITHUB_OUTPUT as it
# is, and exits 0 for either answer. It exits 2 when the commit cannot be
# resolved or git fails.
set -eu

die() {
  echo "release-gate: $*" >&2
  exit 2
}

not_release() {
  printf 'release=false\nreason=%s\n' "$1"
  exit 0
}

. "$(dirname "$0")/semver.sh"

[ $# -eq 1 ] || die "usage: release-gate.sh <commit>"
sha=$(git rev-parse --verify --quiet "$1^{commit}") || die "$1 is not a commit"

git rev-parse --verify --quiet "$sha^1" >/dev/null || not_release "the commit has no parent"

subject=$(git log -1 --format=%s "$sha") || die "git cannot read the subject of $sha"
case $subject in
"chore: release "?*) ;;
*) not_release 'the subject is not "chore: release <version>"' ;;
esac
named=${subject#chore: release }

changed=$(git diff-tree -r --no-renames --name-only "$sha^1" "$sha") || die "git cannot diff $sha"
[ "$changed" = "VERSION" ] || not_release "the commit changes more than VERSION"

version=$(git show "$sha:VERSION") || die "git cannot read VERSION at $sha"
[ "$version" = "$named" ] || not_release "VERSION does not match the subject"
is_semver "$version" || not_release "VERSION is not a semantic version"
if has_prerelease "$version"; then
  not_release "VERSION has a pre-release part, and dist takes none"
fi

printf 'release=true\nversion=%s\n' "$version"

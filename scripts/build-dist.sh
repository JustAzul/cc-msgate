#!/bin/sh
# Build the dist tree: the plugin content, four static binaries in libexec/,
# LICENSE, README.md and SHA256SUMS. Runs in CI and inside `make dist`.
#
#   scripts/build-dist.sh <outdir>
#
# The version comes from the VERSION file; it is also the version field of the
# dist manifest. SOURCE_SHA and BUILD_SEQ may be set in the environment (make
# dist does, because git metadata is not visible inside the container);
# otherwise they come from git, which must then hold the full history, because
# build_seq is the commit count.
set -eu

die() {
  echo "build-dist: $*" >&2
  exit 2
}

[ $# -eq 1 ] || die "usage: build-dist.sh <outdir>"
root=$(cd "$(dirname "$0")/.." && pwd -P)
cd "$root"

out=$1
if [ -e "$out" ]; then
  [ -d "$out" ] || die "$out exists and is not a directory"
  [ -z "$(ls -A "$out")" ] || die "$out is not empty"
fi
mkdir -p "$out"
out=$(cd "$out" && pwd -P)

version=$(cat VERSION)
# The version is written into a JSON string, so it may hold only the characters
# a Semantic Versioning 2.0.0 version is made of.
case $version in
"") die "VERSION is empty" ;;
*[!0-9A-Za-z.+-]*) die "VERSION holds a character a version does not use: $version" ;;
esac
if [ -z "${SOURCE_SHA:-}" ]; then
  SOURCE_SHA=$(git rev-parse HEAD 2>/dev/null) || die "SOURCE_SHA is not set and git cannot name HEAD"
fi
if [ -z "${BUILD_SEQ:-}" ]; then
  if [ "$(git rev-parse --is-shallow-repository 2>/dev/null)" = "true" ]; then
    die "git history is shallow, so build_seq would be wrong; fetch full history (fetch-depth: 0) or set BUILD_SEQ"
  fi
  BUILD_SEQ=$(git rev-list --count HEAD 2>/dev/null) || die "BUILD_SEQ is not set and git cannot count commits"
fi

pkg=github.com/JustAzul/cc-msgate/internal/version
ldflags="-s -w -X $pkg.Version=$version -X $pkg.SourceCommit=$SOURCE_SHA -X $pkg.BuildSeq=$BUILD_SEQ"

cp -R plugin/. "$out/"
# Mod tests and the type declarations Claude Code generates beside the mod are
# development files; they are not part of the shipped plugin.
rm -rf "$out/tests" "$out/.claude-plugin/types" "$out/tsconfig.json"

# The source manifest carries no version, so VERSION stays the only source: the
# dist manifest gets it as its second field, and a plugin installed from dist is
# cached and updated by it.
manifest="$out/.claude-plugin/plugin.json"
awk -v v="$version" '{ print } /^  "name": /{ printf "  \"version\": \"%s\",\n", v }' "$manifest" >"$manifest.tmp"
mv "$manifest.tmp" "$manifest"
grep -q '^  "version": ' "$manifest" || die "plugin.json has no name line to put the version after"
cp LICENSE README.md "$out/"
chmod 755 "$out/bin/msgate"
mkdir -p "$out/libexec"

platforms="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64"
for p in $platforms; do
  os=${p%/*}
  arch=${p#*/}
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -buildvcs=false -ldflags "$ldflags" \
    -o "$out/libexec/msgate-$os-$arch" ./cmd/msgate
done

if command -v sha256sum >/dev/null 2>&1; then
  sum() { sha256sum "$@"; }
else
  sum() { shasum -a 256 "$@"; }
fi
(
  cd "$out"
  : >SHA256SUMS
  for p in $platforms; do
    sum "libexec/msgate-${p%/*}-${p#*/}" >>SHA256SUMS
  done
)
echo "build-dist: wrote $out (version $version, build_seq $BUILD_SEQ)"

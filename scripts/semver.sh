# Sourced by the release scripts: the Semantic Versioning 2.0.0 check.

# The regular expression semver.org suggests, in POSIX ERE.
_number='(0|[1-9][0-9]*)'
_identifier='(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)'
_semver="^$_number\\.$_number\\.$_number(-$_identifier(\\.$_identifier)*)?(\\+[0-9A-Za-z-]+(\\.[0-9A-Za-z-]+)*)?\$"

# is_semver <version>: succeeds when the version is a semantic version of one
# line. grep matches line by line, so a second line would pass unchecked.
is_semver() {
  case $1 in
  *"
"*) return 1 ;;
  esac
  printf '%s\n' "$1" | grep -Eq "$_semver"
}

# has_prerelease <version>: succeeds when the version has a pre-release part.
has_prerelease() {
  case ${1%%+*} in
  *-*) return 0 ;;
  *) return 1 ;;
  esac
}

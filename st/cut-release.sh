#!/usr/bin/env bash
# Cut a Sastaticket gc release: merge an upstream main commit into st/release,
# tag it st-YYYYMMDD-<upstream-sha12>, and (with --push) push the branch and tag.
# The tag push runs .github/workflows/st-release.yml, which builds and
# publishes the pre-release. See st/README.md.
#
# usage: st/cut-release.sh [--push] [<upstream-ref>]   (default: upstream/main)
set -euo pipefail

push=0
ref=""
for arg in "$@"; do
  case "$arg" in
    --push) push=1 ;;
    -h|--help) sed -n 2,7p "$0"; exit 0 ;;
    -*) echo "unknown option: $arg" >&2; exit 2 ;;
    *) [ -z "$ref" ] || { echo "one upstream ref only" >&2; exit 2; }; ref="$arg" ;;
  esac
done

cd "$(git rev-parse --show-toplevel)"

if [ "$(git remote get-url origin)" != "https://github.com/sastaticket/gascity.git" ] &&
   [ "$(git remote get-url origin)" != "git@github.com:sastaticket/gascity.git" ]; then
  echo "origin is not sastaticket/gascity: $(git remote get-url origin)" >&2
  exit 1
fi
git remote get-url upstream >/dev/null 2>&1 ||
  git remote add upstream https://github.com/gastownhall/gascity.git
if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
  echo "working tree has changes; commit or stash them first" >&2
  exit 1
fi

git fetch --quiet origin
git fetch --quiet --no-tags upstream main
base="$(git rev-parse "${ref:-upstream/main}^{commit}")"
if ! git merge-base --is-ancestor "$base" upstream/main; then
  echo "$base is not on upstream main" >&2
  exit 1
fi

git switch --quiet st/release
if git rev-parse --verify --quiet origin/st/release >/dev/null; then
  git merge --quiet --ff-only origin/st/release
fi
if ! git merge-base --is-ancestor "$base" HEAD; then
  git merge --no-edit --quiet -m "Merge upstream ${base:0:12} into st/release" "$base"
fi
if [ "$(git merge-base HEAD upstream/main)" != "$base" ]; then
  echo "st/release already contains upstream $(git merge-base HEAD upstream/main | cut -c1-12)," >&2
  echo "which is newer than ${base:0:12}; a release never moves its base backwards" >&2
  exit 1
fi

echo "building gc for darwin/arm64 to check the merged tree compiles..."
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o /dev/null ./cmd/gc

tag="st-$(date -u +%Y%m%d)-${base:0:12}"
if git rev-parse --verify --quiet "refs/tags/$tag" >/dev/null ||
   git ls-remote --exit-code --tags origin "refs/tags/$tag" >/dev/null 2>&1; then
  n=2
  while git rev-parse --verify --quiet "refs/tags/$tag.$n" >/dev/null ||
        git ls-remote --exit-code --tags origin "refs/tags/$tag.$n" >/dev/null 2>&1; do
    n=$((n + 1))
  done
  tag="$tag.$n"
fi
git tag -a "$tag" -m "gc $tag (upstream gastownhall/gascity@$base)"

echo
echo "tagged $tag at $(git rev-parse --short=12 HEAD) (upstream base ${base:0:12})"
git log --no-merges --format='  %h %s' "$base..HEAD"
if [ "$push" = 1 ]; then
  git push origin st/release "refs/tags/$tag"
else
  echo
  echo "to publish:  git -C $(pwd) push origin st/release refs/tags/$tag"
fi

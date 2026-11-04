# Sastaticket gc builds

`sastaticket/gascity` is Sastaticket's fork of
[gastownhall/gascity](https://github.com/gastownhall/gascity). It exists so
Sastaticket's laptops can run a pinned `gc` built from upstream `main`, ahead of
upstream's tagged releases. It is also where Sastaticket's upstream PRs and
issues start.

## Branches and tags

| Ref | What it holds |
| --- | --- |
| `main` | an exact mirror of upstream `main`. Sync it with GitHub's *Sync fork* button; never commit to it. |
| `feat/**`, `fix/**` | one upstream PR each, cut from upstream `main`. Upstream's `fork-verify` workflow checks them on this fork. |
| `st/release` | upstream `main` merged in at each release, plus this directory, `.github/workflows/st-release.yml`, and any Sastaticket-only patch. |
| `st-YYYYMMDD-<upstream-sha12>[.N]` | one published build. The `<upstream-sha12>` is the upstream commit it is based on. |

**Never push a `v*` tag here.** Upstream's release workflows react to `v*`
tags. They are guarded to `gastownhall/gascity` today, but a fork must not depend on
that guard.

A patch goes on `st/release` only when a Sastaticket city is hitting the
problem it fixes. Offer it upstream at the same time, from a `fix/**` branch.
Drop it from `st/release` once upstream merges it.

## Cut a release

From a clone with `origin` pointing at this fork:

```sh
st/cut-release.sh                 # base: current upstream main
st/cut-release.sh <upstream-sha>  # base: an earlier upstream main commit
```

The script:

1. merges the upstream commit into `st/release`;
2. refuses to move the base backwards;
3. compiles `gc` for darwin/arm64;
4. tags the merge.

It prints the push command; `--push` pushes the branch and the tag itself.

The tag push runs `st release`. That workflow checks the tag names the real
upstream base. It then builds upstream's goreleaser archives in snapshot mode
(darwin and linux, amd64 and arm64), checks the binary reports the tagged
commit, and attests the checksums. Finally it publishes a pre-release whose
notes list every commit on top of upstream.

`gc version` prints the tag. Upstream's product metrics classify these builds
as canaries.

## Install on a laptop

```sh
sh st/install-gc.sh st-20261104-4118c6ab7f33    # PREFIX=… to change ~/.local/bin
```

The installer downloads the archive for this OS and architecture and checks its
sha256. When `gh` is installed, it also checks the provenance attestation. It
installs `gc`, `gc-osc7501` and `gc-write-mint` side by side and clears macOS
quarantine. Graphviz also ships a `gc`, so it warns when another `gc` comes
first on `PATH`.

Every city that shares a store runs the same tag, together with the bd and Dolt
versions that tag was tested with.

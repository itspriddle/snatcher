---
name: bump-version
description: Cut and push a new semver git tag for the snatcher CLI. Use this skill whenever the user asks to bump the version, cut a release, tag a release, ship a new version, push a tag, or get a new snatcher build out — including phrases like "bump version", "tag a release", "cut v0.x.y", "release a new snatcher", or "ship it". Trigger even when the user doesn't name a specific version.
---

# bump-version

## Overview

Cut a new snatcher release by tagging `main` with the next semver version and
pushing the tag. The `release.yml` workflow triggers on `v*` tags and runs
GoReleaser to build and publish the GitHub release. That's it — a lazy tag and
push.

## Prerequisites

- Working directory must be the snatcher repo root (or a subdirectory).
- `main` branch checked out, clean working tree, and up to date with `origin/main`.
- Push access to `origin` (`git@github.com:itspriddle/snatcher.git`).

## Steps

### 1. Determine the current and next version

Run `git tag --sort=-version:refname | head -5` to see recent tags. The latest
tag is the current version (format: `vMAJOR.MINOR.PATCH`).

Ask the user which part to bump if not specified:
- **patch** (`v0.1.2` → `v0.1.3`): bug fixes
- **minor** (`v0.1.2` → `v0.2.0`): new features
- **major** (`v0.1.2` → `v1.0.0`): breaking changes

### 2. Verify the working tree is clean and synced

```sh
git status --porcelain               # must be empty
git rev-parse --abbrev-ref HEAD      # must be "main"
git fetch origin
git rev-list --count HEAD..origin/main   # must be 0 (not behind)
```

If any check fails, stop and tell the user what needs fixing.

### 3. Create and push the tag

```sh
git tag vX.Y.Z
git push origin vX.Y.Z
```

### 4. Confirm the release

Tell the user the tag was pushed and the Release workflow (GoReleaser) will
build and publish it. Link to the Actions run if possible:

```sh
gh run list --workflow=release.yml --limit 1
```

## Notes

- Never bump version by editing files — snatcher uses git tags as the source of
  truth (version is injected via ldflags into `internal/version` at build time).
- Don't create GitHub releases manually; GoReleaser handles that on tag push
  (`.github/workflows/release.yml`, triggered on `v*`).
- Use annotated tags only if the user asks; default to lightweight tags.
- Optional pre-flight: `make release-check` runs the full gate (check +
  replace-check + vuln + race-test) before tagging if the user wants extra
  confidence. Skip it for a quick lazy bump.

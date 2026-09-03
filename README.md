# gh-combine

A [GitHub CLI](https://cli.github.com) extension that combines several pull requests into a single one.

It cherry-picks the commits from each PR onto a fresh branch off `main`, pushes it, and opens a combined PR that links back to the originals.

Documentation: <https://enxtur.github.io/gh-combine/>

## Installation

```sh
gh extension install enxtur/gh-combine
```

## Usage

Run from a clone of the repository the PRs belong to:

```sh
gh combine 12 34 56
```

Each argument is anything `gh pr view` accepts — a PR number, URL, or branch name.

This creates a branch named `combine-prs-12-34-56`, cherry-picks the commits of PR #12, #34, and #56 onto it in that order, force-pushes it to `origin`, and opens a PR titled `Combine #12, #34, #56`.

## Notes

- The base branch is `main`.
- The head branch is force-pushed, and re-running the same command recreates it from scratch — so you can rerun after a PR is updated.
- If a cherry-pick conflicts, the run aborts it and exits with the error. Resolve the conflict in the source PR and try again.

## Development

```sh
go build
gh extension install .
```

Releases are built by [`cli/gh-extension-precompile`](https://github.com/cli/gh-extension-precompile) when a `v*` tag is pushed.

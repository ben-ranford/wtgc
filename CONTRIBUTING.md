# Contributing

## Development setup

Requirements:

- Go `1.26.5` or newer
- Git `2.36+`
- `make`

Install tools and run the full local gate:

```bash
make setup
make ci
```

For a faster edit loop:

```bash
make format-check
make test
make build
```

Install repository-managed hooks:

```bash
make hooks-install
```

## Workflow

1. Create a focused branch using the existing prefix style, for example
   `feat/<issue>-short-description` or `bug/<issue>-short-description`.
2. Use Conventional Commits so Release Please can assemble the next release PR.
3. Add or update tests for behavior changes.
4. Keep commits scoped to one concern.
5. Open a pull request with validation evidence from `make ci`.

## Pull request expectations

Include:

- Problem statement and intended behavior.
- Summary of changes.
- Test and validation evidence.
- Compatibility or migration notes.
- Safety impact for worktree deletion behavior.
- Release impact if the change affects the next release note or version bump.

Use `.github/PULL_REQUEST_TEMPLATE.md`.

Participation in the project is governed by the
[Code of Conduct](CODE_OF_CONDUCT.md).

## Reporting bugs and requesting features

Use the issue templates in `.github/ISSUE_TEMPLATE/`.

Use [GitHub Discussions](https://github.com/ben-ranford/wtgc/discussions) for
usage questions and early feature ideas. See [SUPPORT.md](SUPPORT.md) for the
full support policy.

For safety-related bugs, include whether the worktree was dirty, locked,
detached, primary, locally merged, and remotely reachable.

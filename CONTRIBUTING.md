# Contributing

## Development setup

Requirements:

- Go `1.26.x` or newer
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
2. Add or update tests for behavior changes.
3. Keep commits scoped to one concern.
4. Open a pull request with validation evidence from `make ci`.

## Pull request expectations

Include:

- Problem statement and intended behavior.
- Summary of changes.
- Test and validation evidence.
- Compatibility or migration notes.
- Safety impact for worktree deletion behavior.

Use `.github/PULL_REQUEST_TEMPLATE.md`.

## Reporting bugs and requesting features

Use the issue templates in `.github/ISSUE_TEMPLATE/`.

For safety-related bugs, include whether the worktree was dirty, locked,
detached, primary, locally merged, and remotely reachable.

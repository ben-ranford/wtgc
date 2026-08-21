# Support

## Usage questions

Use [GitHub Discussions](https://github.com/ben-ranford/wtgc/discussions) for
installation help, configuration questions, and ideas that are not yet concrete
bug reports. Search existing discussions and documentation before starting a
new thread.

## Bug reports

Use the repository's bug-report template for reproducible defects. Include:

- `wtgc --version`, `git --version`, and the operating system.
- The command that was run, with sensitive paths or remote URLs redacted.
- Human or JSON output showing the affected classification and action.
- Whether the worktree was primary, dirty, detached, locked, locally merged,
  and remotely reachable.

Never publish suspected vulnerabilities or sensitive repository data in a
public issue. Follow [SECURITY.md](SECURITY.md) instead.

## Supported versions

Before the first tagged release, support targets the default branch. After
releases begin, the latest stable release receives fixes; older releases may be
supported when a security or data-safety issue warrants a backport.

wtgc is maintained without a commercial support SLA. Maintainers prioritize
credible data-loss and unsafe-deletion reports above compatibility questions and
feature requests.

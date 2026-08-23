# Security Policy

## Supported Versions

Security fixes are supported on the current release line and the default branch.

| Version | Supported |
| --- | --- |
| 1.0.x | :white_check_mark: |
| main | :white_check_mark: |

## Reporting a Vulnerability

Use [GitHub private vulnerability reporting](https://github.com/ben-ranford/wtgc/security/advisories/new)
from the repository Security tab.

If the Security tab is unavailable, contact the repository owner through their
GitHub profile and avoid filing public issues for suspected data-loss,
credential, or command-execution vulnerabilities.

Please include:

- The affected version or commit.
- Reproduction steps.
- Whether data loss, unintended deletion, command execution, or credential
  exposure is possible.
- The Git state involved, including worktree path, branch, dirty status, local
  ancestry, and remote reachability where relevant.

## Safety-Sensitive Areas

Treat these as security-relevant changes:

- Worktree deletion and branch deletion.
- Git command execution and argument construction.
- Path canonicalization and traversal.
- JSON output consumed by automation.
- Scheduled cleanup examples.

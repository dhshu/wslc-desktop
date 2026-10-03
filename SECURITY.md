# Security policy

## Supported versions

| Version | Supported |
| --- | --- |
| latest `main` | ✅ |
| prior tags | only critical fixes |

## Reporting a vulnerability

**Do not open a public issue.** Use GitHub's private vulnerability reporting:

1. Click the **Security** tab on the repository's right sidebar.
2. Choose **Report a vulnerability**.
3. Fill in the form with reproduction steps and impact assessment.

Alternatively, contact the maintainer directly.

## Response timeline

- **Acknowledge** within 3 business days.
- **Initial assessment** within 7 business days.
- **Patch** as soon as the severity warrants.

## Scope

This policy covers:

- Command injection via container names, image refs, or user-supplied arguments
  (mitigated by always using `exec.Command` with an argument slice — see
  `internal/service/*`).
- Path traversal in settings file resolution (`settingsPath`).

If you find a bypass, please report privately.

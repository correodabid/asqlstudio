# Security Policy

## Reporting a vulnerability

**Please do not report security vulnerabilities through public issues,
discussions, or pull requests.**

Instead, use one of the following private channels:

1. **Preferred**: [open a private security advisory on GitHub](https://github.com/correodabid/asqlstudio/security/advisories/new).
   This is the fastest path and keeps the report confidential until a
   fix is ready to disclose.
2. Email the maintainer at the address listed on the maintainer's
   GitHub profile, with `[asqlstudio-security]` in the subject line.

You should receive an acknowledgement within **72 hours**. If you do
not, please follow up — your first report may have been missed.

## What to include

Give us enough information to reproduce and assess the issue:

- The version or commit SHA of ASQL Studio you were running.
- Steps to reproduce, including any configuration, SQL, or connection
  flow required.
- The impact: what can an attacker read, write, escalate to, or crash?
- Any proof-of-concept code or configuration samples.

## What you should expect

- **Acknowledgement** within 72 hours.
- **An initial assessment** within 7 days, including severity agreement.
- **A patched release** within 30 days for confirmed high- or
  critical-severity issues. Longer timelines for lower-severity issues
  will be communicated up front.
- **Coordinated disclosure**: we will work with you on a public
  advisory timeline once the fix is available.

## Scope

In scope:

- Any component in the `github.com/correodabid/asqlstudio` repository,
  including the Go backend (`app/`) and the React frontend (`webapp/`).
- The Wails bridge surface and the connection/auth flows.
- Supply-chain concerns (dependency vulnerabilities reachable from
  our code paths).

Out of scope:

- Issues in third-party dependencies not reachable from ASQL Studio
  code paths — report those upstream.
- Theoretical vulnerabilities without demonstrable impact.
- Vulnerabilities in the ASQL engine itself — report those to
  [github.com/correodabid/asql](https://github.com/correodabid/asql/security/advisories/new).

## Supported versions

ASQL Studio does not yet have a formal release cadence. Security fixes
are applied to the `main` branch.

## Attribution

We are happy to credit reporters in the published advisory unless you
prefer to remain anonymous. Please tell us your preference when you
report.

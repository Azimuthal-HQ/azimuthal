# Security Policy

Azimuthal is early, actively developed software. Security reports are welcome and are treated as
the highest-priority class of issue here.

## Supported versions

| Version | Supported |
|---|---|
| v0.4.x | ✅ Current release line — fixes land here |
| ≤ v0.3.x | ❌ No longer receiving fixes |

There is one supported line at a time, and it is the latest minor. Fixes are shipped forward in a
new patch release rather than backported, so the remedy for a security issue on an older version
is to upgrade — see [docs/upgrade.md](docs/upgrade.md).

## Reporting a vulnerability

**Report privately. Do not open a public issue, pull request, or discussion for a security
problem.**

Use GitHub's private vulnerability reporting:

**[github.com/Azimuthal-HQ/azimuthal/security/advisories/new](https://github.com/Azimuthal-HQ/azimuthal/security/advisories/new)**

This opens a draft advisory visible only to you and the maintainer. It is the preferred channel
because it keeps the report, the discussion, the fix and the eventual published advisory in one
place.

> **If that link gives you a 404 or you cannot find "Report a vulnerability" on the Security tab,**
> private reporting has not been enabled on the repository yet. Do not fall back to filing the
> details publicly. Open an ordinary issue that says only that you have a security report and are
> asking for a private channel — no reproduction steps, no affected endpoint, no payload — and wait
> for a reply.

A useful report contains:

- the version or commit you tested,
- how the instance was configured (the environment variables that matter, with secrets redacted),
- what an attacker can do that they should not be able to,
- the steps to reproduce it, and
- if you have one, a suggested fix.

You do not need a working exploit. A clear description of the flaw is enough.

## What to expect

| | |
|---|---|
| **First response** | Within 7 days of the report |
| **Assessment** | An initial severity call and a plan, in the advisory thread |
| **Fix and release** | Timeline stated in the thread once the assessment is done, and updated if it slips |
| **Credit** | Named in the published advisory, or kept anonymous — your choice |

Azimuthal is maintained by one person. Seven days is the window that can actually be kept, not an
aspiration; if a report needs longer than the stated plan, you will be told rather than left
waiting.

## Coordinated disclosure

The request is the ordinary one: give the fix time to ship before the details are public.

- Work with the maintainer through the advisory thread while a fix is prepared.
- A fix is released, then the advisory is published with the details and credit.
- If you intend to publish independently, say so in the thread and name your date. A disagreement
  about timing is better had early than discovered on the day.

Nothing here is a legal constraint on you, and none of it is a condition of being taken seriously.
Reports that arrive outside this process are still acted on.

## Scope

In scope: anything in this repository — the Go server, the React frontend, the migrations, the
shipped `build/docker-compose.yml` and Dockerfiles, and the CI and release workflows.

Out of scope: findings that depend on a deployment doing something this project documents as
unsafe (for instance running with a development-only setting enabled on a production host — see
`AZIMUTHAL_PORTAL_DISCLOSE_LINK` in [README.md](README.md#invitations-and-the-customer-portal)),
vulnerabilities in third-party dependencies with no reachable path in Azimuthal, and automated
scanner output submitted without a demonstrated impact.

If you are unsure whether something is in scope, report it. Deciding that is the maintainer's job,
not yours.

## Where security issues are *not* tracked

[`docs/known-issues.md`](docs/known-issues.md) is a public log of ordinary defects. Security
findings do not go there while they are live — they live in the private GitHub Security Advisory
until a fix ships, and in the published advisory afterwards.

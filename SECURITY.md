# Security Policy

Hivemind is a control plane that holds credentials and drives Docker Swarm
clusters, so we take security reports seriously.

## Supported versions

Hivemind is pre-1.0 and under active development. Security fixes are applied to
the `main` branch and the latest tagged release. There is no long-term support
branch yet.

## Reporting a vulnerability

**Please do not open a public issue for security vulnerabilities.**

Instead, report privately through one of:

- GitHub's [private vulnerability reporting](https://github.com/open226/hivemind/security/advisories/new)
  (preferred — "Report a vulnerability" on the Security tab), or
- email to the maintainers at **security@open226.dev**.

Include, where possible:

- a description of the issue and its impact,
- the affected component (`hivemind`, `hivemind-agent`, or `hivemind-gui`) and version/commit,
- reproduction steps or a proof of concept,
- any suggested remediation.

We aim to acknowledge a report within **72 hours** and to provide a remediation
timeline after triage. We will credit reporters in the release notes unless you
ask us not to.

## Scope & hardening notes

Some defaults are intentionally permissive for local development and are called
out in the docs and logs. In production you should:

- set a persistent `JWT_PRIVATE_KEY_PATH` (ephemeral keys log a warning),
- set a 32-byte base64 `AES_KEY` (secret/env values are stored unencrypted otherwise),
- run agents in **mTLS mode** (the token-mode tunnel is for trusted/dev networks),
- serve the API/UI behind TLS and restrict the agent hub to the intended hosts.

See the [Security model](https://open226.github.io/hivemind-doc/concepts/security/)
and [Production checklist](https://open226.github.io/hivemind-doc/operations/production-checklist/)
in the documentation.

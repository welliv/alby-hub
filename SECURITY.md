# Security

## Key facts

- API keys are native to the daemon that minted them. They are not a security boundary: the spend gate on the completions path is the Cashu wallet balance, not the key.
- The daemon binds `*:8008` with unauthenticated admin endpoints and no key check on completions.

## Required hardening

**Block port 8008 in the firewall before the wallet holds sats.** Anyone who can reach it can manage clients and spend a funded wallet. See [docs/security.md](docs/security.md) for details.

## Reporting

Open an issue, or contact the maintainers directly for sensitive findings.
# Security Policy

## Reporting a Vulnerability

Please report suspected security vulnerabilities privately by emailing [security@getalby.com](mailto:security@getalby.com). Do not open a public issue or disclose the vulnerability publicly until we have coordinated a fix.

Please include the affected version or component, the potential impact, and clear steps to reproduce the issue. We will acknowledge your report and keep you informed as we investigate and address it.

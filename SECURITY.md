# Security policy

## Reporting a vulnerability

Please **do not** open a public issue. Report privately through GitHub Private Vulnerability Reporting on the `tolkovo/tolkovo` repository ("Security" → "Report a vulnerability").

Include what is affected, how to reproduce it and the impact you expect. We aim to acknowledge within 3 working days and to agree on a disclosure date with you; credit is given unless you prefer otherwise.

## Supported versions

Before v1.0 only the latest release receives security fixes.

## Scope

In scope: the server, panel, widget, official Docker images and the hosted cloud. Especially valuable: cross-tenant data access, authentication and authorization bypass, injection, SSRF, leaking channel tokens or AI keys, prompt injection that makes the AI disclose data or act outside its permissions.

Out of scope: findings that need a compromised server or browser, missing best-practice headers without a demonstrated impact, volumetric denial of service.

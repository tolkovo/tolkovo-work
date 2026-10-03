# Contributing to Tolkovo

Thank you for helping. This page is the short version; the full standard is [docs/00-engineering-rules.md](docs/00-engineering-rules.md) and applies to people and AI agents alike.

## Before you start

- For anything bigger than a small fix, open an issue or a Discussion first so we agree on the approach.
- Decisions that are hard to undo (technology, data format, protocol, module boundary, security model) need an ADR in `docs/adr/` before code. Template: [docs/00 appendix A](docs/00-engineering-rules.md).

## Workflow

1. Branch from `main`: `<type>/<scope>-<short>`, e.g. `feat/inbox-hotkeys`. Branches live up to 3 days.
2. Contract first: change `api/openapi.yaml` or `api/ws/*.schema.json`, run `make gen`, then write code. Never edit generated code.
3. Run `make check` before opening a PR.
4. Keep PRs under ~400 changed lines excluding generated code. Unfinished features hide behind a feature flag.

## Commits

- [Conventional Commits](https://www.conventionalcommits.org/): `feat(inbox): …`, `fix(telegram): …`, `docs: …`, `feat!:` for breaking changes. The scope is the module or app.
- **DCO:** every commit is signed off (`git commit -s`), certifying the [Developer Certificate of Origin](https://developercertificate.org/). We do not use a CLA.
- Never commit secrets, `.env` files, database dumps, real personal data or build artifacts.

## Tests

- Integration tests use a real PostgreSQL, never mocks (see README for `TOLKOVO_TEST_DATABASE_URL`).
- Every repository and endpoint gets a tenant isolation test: data created in workspace A is invisible from workspace B.
- Every protected endpoint gets "allowed" and "forbidden" tests.
- Every fixed bug gets a test that failed before the fix.

## Dependencies

A new direct dependency needs maintainer approval and a short check in the PR: is it needed, is the license compatible (MIT, BSD, Apache-2.0, MPL-2.0, ISC; MIT-compatible only in the widget and SDKs), is it maintained and safe.

## Code of conduct

Participation is governed by [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).

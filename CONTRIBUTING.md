# Contributing to zlatan

Read [`README.md`](README.md) and [`docs/design/README.md`](docs/design/README.md) before writing code. The full design lives in the homelab repository, `docs/zlatan-service.md`.

## The rules

1. The identity comes only from the forward-auth header, and only from the configured proxy. Never from a URL parameter or a cookie: a person can only ever see their own migration, by construction.
2. Secrets never touch git or logs, and never travel in a URL. Google refresh tokens, Nextcloud app passwords and Immich API keys are sealed with AES-GCM before they reach the database.
3. Each person acts as themselves. No shared credential writes into anyone's account: the Nextcloud app password and the Immich key belong to the person whose data is moving.
4. Fail loud. A missing credential or a failed step stops that track with a message the person can act on, never a silent fallback or a screen that spins forever.
5. Every step can be run again. rclone skips what is already there and immich-go discards duplicates by hash, so a restart or a retry never copies twice. Keep it that way.
6. The two tracks are independent. The state of `drive` never touches `photos`.
7. No claim the code does not back. A screen promises only what the service actually does.

## Development

```shell
make check     # gofmt, vet, tests with the race detector
make screens   # render every wizard screen, in every language
make image
```

Run `make screens` after touching a template, the stylesheet or a catalogue, and commit the result: `docs/design/screens/` is generated, never edited by hand.

## Conventions

- Code, comments, commit messages and docs are in English. The wizard speaks English and Italian, and both catalogues in `internal/i18n` must have the same keys and the same placeholders; tests check it.
- Every action is a form POST or a link, and works with JavaScript off.
- Prefer the standard library and well-maintained packages. Justify anything heavy in the PR.
- Configuration is environment based, documented in `.env.example`. No interactive setup.

## Style

- Do not use em-dashes. Use a comma, a colon, parentheses, or a plain hyphen. This applies to code comments, docs, commit messages and PR descriptions.
- Do not hard-wrap Markdown. Write one paragraph per line and let the renderer wrap it. Tables and code blocks are the only exception.
- Keep comments short and rare. Explain why, never what. If a comment restates the code, delete it.
- No comment banners, no section dividers, no decorative ASCII beyond what already exists.
- Write plainly. No emoji.
- Commit messages have an imperative subject, and a one-line subject is usually enough. Add a body only when the why is genuinely non-obvious.

## Pull requests

- One concern per PR, with a focused diff. Do not reformat unrelated code.
- Include tests for the new behavior.
- No secrets in code, logs or fixtures.
- Update the docs, and the screens if the wizard changed.

## Reporting bugs

Include the zlatan version, which track (Drive or Photos) and which step, what you expected, what happened, and the relevant logs with tokens and passwords redacted. Never paste credentials.

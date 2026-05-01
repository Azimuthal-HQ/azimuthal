# Contributing to Azimuthal

Thank you for your interest in contributing to Azimuthal.

## Contributor License Agreement (CLA)

**Before your first pull request can be merged, you must sign the Azimuthal CLA.**

By submitting a contribution, you agree that:

1. You have the right to grant the license below.
2. You grant Azimuthal HQ a perpetual, worldwide, non-exclusive, royalty-free
   license to use, reproduce, modify, distribute, and sublicense your
   contribution as part of the project.
3. Your contribution does not include code under a GPL, AGPL, or LGPL license.

The CLA bot will automatically prompt you to sign when you open your first PR.

## Code of Conduct

Be respectful. No harassment, discrimination, or hostility of any kind.
Violations may result in a permanent ban.

## How to Contribute

### Reporting Bugs

Open an issue with:
- Steps to reproduce
- Expected behaviour
- Actual behaviour
- Azimuthal version (from `azimuthal --version`)

### Proposing Features

Open an issue tagged `enhancement` before writing code.
Large features need a design discussion first.

### Pull Requests

1. Fork the repo and create a branch: `feat/short-description`
2. Follow the development setup in `CLAUDE.md`
3. Run `make pre-push` — all checks must pass
4. Write tests first (TDD — see CLAUDE.md)
5. Keep PRs small and focused — one concern per PR
6. Reference the issue your PR closes: `Closes #123`

### Commit Style

```
type: short imperative description

Longer explanation if needed. Wrap at 72 characters.
```

Types: `feat`, `fix`, `docs`, `test`, `refactor`, `chore`

### License Compliance

Run `go-licenses check ./...` before adding any new dependency.
Never add a dependency under GPL, AGPL, or LGPL.

## Getting Help

- GitHub Issues for bugs and features
- GitHub Discussions for questions and ideas

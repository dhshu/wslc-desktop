# Contributing to wslc Desktop

Thanks for your interest in contributing. This project is a community-driven
front-end for [wslc](https://learn.microsoft.com/en-us/windows/wsl/wsl-container),
and small improvements matter a lot.

## Ways to contribute

- **Bug reports** — file an [issue](https://github.com/dhshu/wslc-desktop/issues)
  with your wslc version, WSL version, and the exact command that fails.
- **Feature requests** — describe the user story and the wslc sub-command
  involved. We prioritize features that work around gaps in upstream wslc.
- **Pull requests** — see the workflow below.
- **Documentation** — typos, screenshots, and translations are all welcome.

## Development workflow

```powershell
# 1. Clone and install prerequisites
git clone https://github.com/dhshu/wslc-desktop.git
cd wslc-desktop

go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0

# 2. Verify your local setup
wails doctor
go test ./... -count=1
node frontend/tools/selfcheck.mjs
node frontend/tools/smoke.mjs

# 3. Make changes on a branch
git checkout -b feat/my-change

# 4. Build and test
wails build -s
go test ./... -count=1
go test -tags integration ./... -count=1   # requires real wslc.exe

# 5. Open a PR
```

## Branch naming

- `feat/<short-name>` — new feature
- `fix/<short-name>` — bug fix
- `docs/<short-name>` — documentation only
- `refactor/<short-name>` — no behavior change

## Commit messages

Conventional Commits are encouraged but not enforced:

```
feat(settings): add proxy loopback rewrite
fix(wslc): handle localized Chinese error message
docs(readme): update install instructions
```

## Code style

- Go: `gofmt` + `go vet`. Follow `internal/wslc` and `internal/service` for
  existing idioms. Never `exec.Command("sh", "-c", <concat>)` — always pass an
  argument slice.
- Frontend: plain HTML/CSS/JS, no build step. Keep new DOM ids in sync with
  `frontend/tools/selfcheck.mjs` (there's a placeholder list in
  `index.html`).
- The `docs/CONTRACT.md` file is the frozen front-end / back-end contract.
  If you change a bound method signature, update CONTRACT.md and both the
  Go test (`internal/service/binding_test.go`) and the JS selfcheck.

## Security-sensitive changes

Anything touching command construction, proxy URL injection, or user settings
parsing must come with a unit test. Run the race detector:

```powershell
$env:CGO_ENABLED = "1"
# Make sure a C toolchain is on PATH
go test ./... -race -count=1
```

## Reporting security issues

Please do **not** open a public issue for vulnerabilities. Contact the
maintainer directly or use GitHub's private vulnerability reporting
([Security tab](https://github.com/dhshu/wslc-desktop/security/advisories)).

## License

By contributing, you agree that your contributions are licensed under the
[MIT License](LICENSE).

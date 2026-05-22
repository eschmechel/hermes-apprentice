# Repository Rules

## Branch protection (recommended)

Settings → Branches → Add rule for `main`:

- [x] Require a pull request before merging
- [x] Require approvals (1)
- [x] Require status checks to pass before merging
  - `go-test (proxy)`
  - `go-test (dataset-builder)`
  - `go-test (registry-service)`
  - `go-test (burst)`
  - `python-test (orchestrator)`
  - `python-test (trainer)`
  - `python-test (validator)`
  - `ruff`
  - `golangci-lint`
- [x] Require conversation resolution before merging
- [x] Require linear history (no merge commits)

## Secret scanning

Settings → Code security → Secret scanning:

- Enable push protection
- Custom pattern: `openrouter.*key.*sk-or-v1-[a-zA-Z0-9]{64}` (OpenRouter API keys)
- Custom pattern: `telegram.*token.*[0-9]{8,10}:[a-zA-Z0-9_-]{35}` (Telegram bot tokens)
- Custom pattern: `runpod.*key.*rpa_[a-zA-Z0-9]{32}` (RunPod API keys)

## CODEOWNERS

See `.github/CODEOWNERS`.

## Commit conventions

All commits must follow [Conventional Commits](https://www.conventionalcommits.org/). See `.commitlintrc.json` for allowed types and scopes.

## Security

- API keys are stored in `~/.apprentice/.env` (local), `$ENV` (CI), or passed via CLI flags — never committed
- `.gitattributes` marks `*.lock`, `*.enc`, `*.key` as binary (no diff)
- Training manifests are Ed25519-signed; validator enforces signature check
- Registry manifests are Ed25519-signed; proxy can verify offline

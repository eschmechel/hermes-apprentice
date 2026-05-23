# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 0.2.x   | :white_check_mark: |
| 0.1.x   | :x:                |

## Reporting a Vulnerability

Do **not** open a public issue.

Email `security@andrew.menu` with:

- A clear description of the vulnerability
- Steps to reproduce or a proof of concept
- Affected components and versions

You'll get an acknowledgment within 48 hours. We'll follow up within 7 days with a timeline, then keep you updated as the fix progresses.

### What to expect

1. Acknowledgment — within 48 hours
2. Assessment — severity and scope confirmed, usually within 5 days
3. Fix + advisory — coordinated disclosure after the patch ships

### Scope

The Hermes Apprentice stack includes the Go proxy, the Python orchestrator/trainer/validator pipelines, the Telegram bot, and the installer. The following are in scope:

- API key exposure or credential leaks in logs, env files, or configs
- Authentication bypass in the proxy or tenant middleware
- Prompt injection through the chat completions endpoint that escapes routing
- Arbitrary code execution in the pipeline (subprocess calls, shell injection)
- Model file tampering through the registry service

### Out of scope

- Vulnerabilities in the base models (Qwen, Llama) — report those to HuggingFace or the model authors
- OpenRouter API issues — report to OpenRouter
- RunPod API issues — report to RunPod
- Dependabot / GitHub Actions vulnerabilities in the CI configs

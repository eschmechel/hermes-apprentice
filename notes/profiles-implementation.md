# Hermes profiles implementation

Source: `hermes_cli/profiles.py` (+ user-guide `website/docs/user-guide/profiles.md`) in Hermes v0.14.0 (release tag `v2026.5.16`, commit `a91a57fa`). Path inside the Firecracker rootfs: `/usr/local/lib/hermes-agent/hermes_cli/profiles.py`.

## Isolation model — `HERMES_HOME` is the boundary

A profile is **a fully independent Hermes home directory**. Everything Hermes resolves via `hermes_constants.get_hermes_home()` re-points: `config.yaml`, `.env`, `SOUL.md`, `state.db`, `sessions/`, `skills/`, `memories/`, `cron/`, `logs/`, gateway PID, gateway state, plugin data. Per the user-guide docs (line 236), 119+ files in the codebase route through `get_hermes_home()`, so the entire state surface re-scopes the moment `HERMES_HOME` changes.

Disk layout:

```
~/.hermes/                                ← the "default" profile (no separate dir)
├── config.yaml, .env, SOUL.md
├── memories/, sessions/, skills/, cron/, …
├── active_profile                        ← sticky-default file (optional)
└── profiles/
    ├── coder/                            ← named profile
    │   ├── config.yaml, .env, SOUL.md
    │   ├── memories/, sessions/, skills/, cron/, …
    │   └── home/                         ← per-profile $HOME for subprocesses
    └── researcher/
        └── … (same layout as coder)
```

`_get_profiles_root()` (line 159) anchors to **the default hermes root, not the current `HERMES_HOME`** — so `coder profile list` from inside the `coder` profile still sees all siblings.

### What profiles are NOT

The user-guide docs are explicit (lines 109–115):
- A **profile** isolates state.
- A **workspace** is the agent's terminal `cwd` — controlled separately by `terminal.cwd`.
- A **sandbox** is filesystem-access enforcement — profiles do **not** provide this. The agent has the same filesystem access as the host user account.

On the local terminal backend, `cwd: "."` means "the directory hermes was launched from", **not** the profile dir. If you want a profile pinned to a project tree, set an explicit absolute `terminal.cwd` in that profile's `config.yaml`.

## Inheritance model — none, by design

Profiles are **independent siblings**, not a stack. There is no runtime "fall through to default" — once `HERMES_HOME` is set, that profile's files are the only ones consulted. Inheritance happens **only at creation time**, by copying state from a source profile. After creation, the new profile is its own root of truth.

Three create modes (`create_profile()` at line 440):

| Mode | What it copies from the source | What it does NOT copy | Use case |
|---|---|---|---|
| Blank (default) | Nothing. Bootstraps directory skeleton, optionally seeds bundled skills. | Everything | Fresh agent |
| `--clone` | `config.yaml`, `.env`, `SOUL.md`, **all installed skills** (`skills/` subtree), `memories/MEMORY.md`, `memories/USER.md` | Sessions, state.db, cron jobs, gateway tokens/PID | "Same brain, fresh sessions" |
| `--clone-all` | Full `shutil.copytree` of the source profile | `gateway.pid`, `gateway_state.json`, `processes.json` (`_CLONE_ALL_STRIP`); also `profiles/` is excluded from the source root to prevent recursive nesting | Backup, fork an agent mid-context |

You can target a non-default source with `--clone-from <name>`; otherwise both `--clone` and `--clone-all` pull from the **currently active profile** (resolved via `get_hermes_home()` at create time, line 497).

Constants driving each mode:

```python
_CLONE_CONFIG_FILES   = ["config.yaml", ".env", "SOUL.md"]
_CLONE_SUBDIR_FILES   = ["memories/MEMORY.md", "memories/USER.md"]
_CLONE_ALL_STRIP      = ["gateway.pid", "gateway_state.json", "processes.json"]
```

The `--clone` copy of `skills/` uses `shutil.copytree(..., dirs_exist_ok=True)` (line 537), so both bundled AND user-installed skills come across — matching the dashboard "clone from default" UX expectation.

### Recursion guard

`_clone_all_copytree_ignore` (line 91) excludes the literal name `profiles` **only at the source root level**. Without this, `--clone-all` from the default profile would recursively duplicate `~/.hermes/profiles/...` inside the new profile.

### Default-`SOUL.md` seeding

After cloning (or in blank mode), if no `SOUL.md` ended up in the profile dir, `create_profile` writes the bundled default from `hermes_cli.default_soul.DEFAULT_SOUL_MD` (line 549). Best-effort — failures don't break profile creation.

### `--no-skills` opt-out

Pass `--no-skills` and `create_profile` writes a `.no-bundled-skills` marker file (`NO_BUNDLED_SKILLS_MARKER`, line 80). `seed_profile_skills()` and the `hermes update` all-profile sync loop both check `has_bundled_skills_opt_out()` and skip the profile when the marker is present. The user can still install skills manually or delete the marker to re-enable. Mutually exclusive with `--clone`/`--clone-all` (those explicitly copy skills, so opting out would contradict).

## Activation — three independent mechanisms

1. **Wrapper command alias.** `create_wrapper_script(name)` (line 294) writes an executable shim to `~/.local/bin/<name>` (`_get_wrapper_dir`, line 189). The shim sets `HERMES_HOME=~/.hermes/profiles/<name>` and execs `hermes`. So `coder chat` is exactly `HERMES_HOME=~/.hermes/profiles/coder hermes chat`. `--no-alias` on `create_profile` skips this.
2. **`-p`/`--profile` flag.** Per the user-guide docs, accepted in any position by the CLI. The CLI rewrites `HERMES_HOME` before resolving subcommands.
3. **Sticky default.** `set_active_profile(name)` (line 812) writes the profile name to `~/.hermes/active_profile` (atomic write via `.tmp` + `replace`). `get_active_profile()` (line 797) reads it; missing/empty file means `"default"` (= bare `~/.hermes`). To clear the stickiness, `set_active_profile("default")` `unlink`s the file. The default-profile case is **the absence of the file**, not a value `"default"` in it — keeps `~/.hermes` zero-migration compatible.

### Inferring which profile is active

`get_active_profile_name()` (line 837) does the opposite — given the current process's `HERMES_HOME`, infer the profile name:

- `HERMES_HOME == ~/.hermes` (resolved) → `"default"`
- `HERMES_HOME` is `<profiles_root>/<name>` and `<name>` matches `_PROFILE_ID_RE` → `<name>`
- Otherwise → `"custom"` (e.g. Docker mount at `/opt/data`)

The CLI uses this for the prompt prefix and startup banner.

## Name validation

`_PROFILE_ID_RE = re.compile(r"^[a-z0-9][a-z0-9_-]{0,63}$")` — lowercase, alphanumeric + `_-`, 1–64 chars, can't start with `-` or `_`.

`validate_profile_name` (line 216) also rejects:

| Blocked set | Names | Why |
|---|---|---|
| `_RESERVED_NAMES` | `hermes`, `default`, `test`, `tmp`, `root`, `sudo` | Conflict with shell builtins / privilege confusion / reserved meaning |
| `_HERMES_SUBCOMMANDS` | `chat`, `model`, `gateway`, `setup`, `whatsapp`, `login`, `logout`, `status`, `cron`, `doctor`, `dump`, `config`, `pairing`, `skills`, `tools`, `mcp`, `sessions`, `insights`, `version`, `update`, `uninstall`, `profile`, `plugins`, `honcho`, `acp` | The wrapper alias would shadow the corresponding `hermes` subcommand |

`create_profile` additionally hard-blocks `name == "default"` (line 483) — the default profile is `~/.hermes` itself and isn't recreated.

`check_alias_collision(name)` (line 254) warns when a name would shadow an existing executable on `$PATH`.

## Directory bootstrap (blank create)

Every fresh profile gets these subdirectories created (`_PROFILE_DIRS`, line 36):

```
memories/  sessions/  skills/  skins/  logs/  plans/  workspace/  cron/  home/
```

`home/` is notable: it becomes the per-profile `$HOME` for subprocesses (via `hermes_constants.get_subprocess_home()`). This is what isolates `git`, `ssh`, `gh`, `npm`, etc. between profiles — credentials configured under one profile don't bleed into another. In Docker deployments it also lands inside the persistent volume.

## Skill seeding

`seed_profile_skills(profile_dir, quiet=False)` (line 573) runs `tools.skills_sync.sync_skills` **in a subprocess** (line 593). The comment explains: `sync_skills()` caches `HERMES_HOME` at module-load time, so importing it in-process would read the wrong home. The subprocess gets `env={...os.environ, "HERMES_HOME": str(profile_dir)}` so the cache resolves against the target.

`hermes update` calls `seed_profile_skills` against **every** profile (default + all named), which is why the user-guide says "Skills synced: default (up to date), coder (+2 new), assistant (+2 new)" — sync is fan-out, not single-profile. User-modified skills are never overwritten (the sync compares hashes upstream).

## Export exclusions for the default profile

`_DEFAULT_EXPORT_EXCLUDE_ROOT` (line 117) strips a long list when exporting the default profile, because `~/.hermes` carries infrastructure that named profiles don't (multi-GB repo checkout, worktrees, sibling profiles, binaries, etc.). Notable items:

- Infrastructure: `hermes-agent` (repo), `.worktrees`, `profiles`, `bin`, `node_modules`
- Databases: `state.db{,-shm,-wal}`, `response_store.db{,-shm,-wal}`, `hermes_state.db`
- Runtime/auth: `gateway.pid`, `gateway_state.json`, `processes.json`, `auth.json`, `.env`, `auth.lock`, `active_profile`, `.update_check`, `errors.log`, `.hermes_history`
- Caches: `image_cache`, `audio_cache`, `document_cache`, `browser_screenshots`, `checkpoints`, `sandboxes`, `logs`

Exports for **named** profiles don't apply this list — they're smaller by construction and treat the whole profile dir as exportable.

## Implications for Apprentice

- **Run the trainer in its own profile.** `hermes profile create apprentice-trainer --no-skills` (no bundled skills, the trainer brings its own), then point all Apprentice-side code at `HERMES_HOME=~/.hermes/profiles/apprentice-trainer`. Sessions, memories, and skills produced during training stay in their own namespace and won't pollute other profiles.
- **Fork an existing agent for evaluation.** `hermes profile create eval-XYZ --clone-all --clone-from prod` gives a frozen copy of `prod`'s state — full session history, memories, skills. Run the candidate model against it, compare outcomes, then `hermes profile delete eval-XYZ`. Stripped runtime files (`_CLONE_ALL_STRIP`) mean no gateway-PID collisions with the source.
- **Per-profile subprocess credentials.** The `home/` subdir means each profile has its own git/ssh identity surface. If the Apprentice will commit training artifacts to git via the agent, configure that profile's `~/.hermes/profiles/<name>/home/.gitconfig` rather than the host user's.
- **No filesystem sandbox.** Profiles are *not* what isolates the agent from the host. That's Firecracker's job — the apprentice pipeline still relies on the microVM boundary, not on profiles, for safety.
- **The active_profile file is host-process state.** The Apprentice should NOT call `set_active_profile()` from automation — it'd change which profile interactive `hermes` invocations target. Use the `-p` flag or set `HERMES_HOME` in the subprocess env.
- **Cloning costs disk.** `--clone-all` of a long-running profile can be hundreds of MB to multi-GB (session DB, logs, skill assets). For eval forks where only config/identity matters, prefer `--clone` (only config + skills + identity memory files).
- **Profile-vs-process isolation.** Two `hermes` processes pointing at the **same** profile share a `state.db` and contend for its WAL lock (see foundation-03 notes on the jittered-retry concurrency model). Two `hermes` processes on **different** profiles never touch each other's state — but they DO contend for `~/.local/bin/<name>` wrapper slots if names collide, and for any shared bot tokens (the gateway token-lock blocks the second one).

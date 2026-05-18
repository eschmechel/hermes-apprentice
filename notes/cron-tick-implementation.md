# Hermes cron tick implementation

Source: `cron/scheduler.py` (+ `cron/jobs.py`) in Hermes v0.14.0 (release tag `v2026.5.16`, commit `a91a57fa`). Path inside the Firecracker rootfs: `/usr/local/lib/hermes-agent/cron/`.

## Top-level: `tick(verbose=True, adapters=None, loop=None) -> int`

Defined at `scheduler.py:1578`. Returns the number of jobs that ran (0 if another tick is already in flight).

### Lifecycle

1. **Acquire single-tick file lock.** `~/.hermes/cron/.tick.lock` is opened and `flock(LOCK_EX | LOCK_NB)` is taken (cross-platform: `fcntl` on Unix, `msvcrt` on Windows). If the lock can't be acquired non-blocking, the tick logs `"Tick skipped — another instance holds the lock"` and returns 0. This is what lets the gateway's in-process ticker, a standalone daemon, and a manual CLI tick coexist safely.
2. **Find due jobs.** `due_jobs = get_due_jobs()` (from `jobs.py`).
3. **Advance schedules BEFORE running.** For every due job, `advance_next_run(job["id"])` is called *under the lock, before any execution*. This preserves at-most-once semantics: if a job crashes mid-run, its `next_run_at` is already advanced — it won't re-fire on the next tick.
4. **Resolve parallelism.** Max workers: `HERMES_CRON_MAX_PARALLEL` env var → `cron.max_parallel_jobs` config → unbounded. Set `HERMES_CRON_MAX_PARALLEL=1` to force serial execution.
5. **Partition jobs.** Jobs with a non-empty `workdir` field are queued for **sequential** execution because `run_job` mutates `os.environ["TERMINAL_CWD"]` (process-global) when the agent path is used; concurrent workdir jobs would corrupt each other's `TERMINAL_CWD`. Jobs without a workdir run in a `ThreadPoolExecutor`.
6. **Per-job: `_process_job(job)`.** Each job in either pass runs in a fresh `contextvars.copy_context()` so per-job context vars (logging, telemetry, etc.) don't leak across the pool.
   - `run_job(job)` returns `(success, output, final_response, error)`
   - `save_job_output(job["id"], output)` writes a Markdown record
   - `deliver_content` is the `final_response` on success, an `⚠️ Cron job '...' failed:` alert on failure
   - The agent can return `SILENT_MARKER` (literal "[SILENT]") in `final_response` to suppress delivery — output is still saved
   - Successful jobs with empty `final_response` are downgraded to failure with `error = "Agent completed but produced empty response (model error, timeout, or misconfiguration)"` (issue #8585) so `last_status` doesn't show "ok" for an empty response
   - `mark_job_run(job["id"], success, error, delivery_error=...)` writes the run record
7. **Post-tick orphan cleanup.** Best-effort `_kill_orphaned_mcp_children()` reaps MCP stdio subprocesses that survived session teardown. Only PIDs explicitly flagged as orphans in `tools.mcp_tool._run_stdio`'s `finally` block — live sessions are never touched.
8. **Release the lock**, return the count of successfully-processed jobs.

## `no_agent` mode

Defined as the very first branch of `run_job` (`scheduler.py:955`, no_agent block at line 983).

`no_agent: bool = False` is a per-job flag declared in `jobs.py:438` (`save_job` / `add_job` signature). When `True`, the cron job skips the LLM entirely — **the script IS the job**. The classic use case is a bash watchdog that polls something and either emits a notification line or stays silent.

### What no_agent buys you

- `run_job` exits BEFORE importing `run_agent` or constructing `SessionDB` (the no_agent block is "self-contained" by design, per the comment at line 974). A pure-script tick pays for zero LLM machinery.
- No prompt building, no tool loop, no token spend, no provider API call.
- `prompt` and `skills` fields on the job are ignored when `no_agent=True` (they'd be meaningless without an agent to consume them).
- `workdir` is still respected — it becomes the subprocess `cwd` for the script. The `TERMINAL_CWD` env-bridge that the agent path uses is not involved.

### Validation (in `jobs.py` job-creation path, line 521)

```python
if normalized_no_agent and not normalized_script:
    raise ValueError("no_agent=True requires a script — with no agent and no script, ...")
```

So `no_agent` jobs always have a `script`.

### Output handling (per the comment block at scheduler.py:976–982)

| Script result | Delivered? | `success` field |
|---|---|---|
| Non-zero exit / timeout / crash | Yes — as `⚠ Cron watchdog '...' script failed\n\n<output>\n\nTime: ...` alert | `False` |
| Exit 0, empty stdout | No (silent run) | `True` |
| Exit 0, last line is JSON `{"wakeAgent": false}` | No (silent run, gate respected) | `True` |
| Exit 0, non-empty stdout otherwise | Yes — stdout delivered verbatim as the final message | `True` |

### `wakeAgent` gate (`_parse_wake_gate`, scheduler.py:756)

Convention ported from nanoclaw #1232. Looks at the **last non-empty stdout line** of a script and tries to parse it as JSON. If the result is `{"wakeAgent": false}` (or anything matching `gate.get("wakeAgent", True) is not False`), the agent is skipped for that tick. Any non-JSON line, missing `wakeAgent` key, or `wakeAgent: true` means wake/deliver. For `no_agent` jobs it acts as a "stay silent this tick" signal — same effect as empty stdout.

## Script resolution: `_run_job_script(script_path) -> (success, output)`

Defined at `scheduler.py:655`. Used by **both** the no_agent path (where the script IS the job) and the agent path's pre-run script feature (where the script's stdout becomes input to the prompt).

### Sandboxing — scripts MUST live in `~/.hermes/scripts/`

```python
scripts_dir = _get_hermes_home() / "scripts"
scripts_dir.mkdir(parents=True, exist_ok=True)
scripts_dir_resolved = scripts_dir.resolve()

raw = Path(script_path).expanduser()
if raw.is_absolute():
    path = raw.resolve()
else:
    path = (scripts_dir / raw).resolve()

try:
    path.relative_to(scripts_dir_resolved)
except ValueError:
    return False, f"Blocked: script path resolves outside the scripts directory ..."
```

- Relative paths resolve against `HERMES_HOME/scripts/`.
- Absolute paths and `~`-prefixed paths are accepted but validated against the same directory — `path.resolve()` follows symlinks, so even a symlink-escape inside `scripts/` is blocked.
- `..` traversal and absolute-path injection both surface as the same "resolves outside the scripts directory" error.
- Two follow-up checks: `path.exists()` and `path.is_file()`. Either failure returns `(False, "Script not found: ...")` / `"Script path is not a file: ..."`.

### Interpreter selection (by extension, NOT by shebang)

```python
suffix = path.suffix.lower()
if suffix in (".sh", ".bash"):
    argv = ["/bin/bash", str(path)]
else:
    argv = [sys.executable, str(path)]
```

The comment explains the deliberate choice (line 712): "We deliberately do NOT honour the file's own shebang: the scripts dir is trusted, but keeping the interpreter choice explicit here keeps the allowed surface small and auditable." So `.sh`/`.bash` → bash; everything else → the same Python that Hermes itself is running under.

### Execution

```python
result = subprocess.run(
    argv,
    capture_output=True,
    text=True,
    timeout=_get_script_timeout(),
    cwd=str(path.parent),
)
```

- `cwd` is the script's own directory (so relative-path I/O inside scripts is predictable).
- `text=True` decodes stdout/stderr as UTF-8.
- Timeout comes from `_get_script_timeout()` (next section).

### Secret redaction (always applied)

```python
from agent.redact import redact_sensitive_text
stdout = redact_sensitive_text(stdout)
stderr = redact_sensitive_text(stderr)
```

Both streams are scrubbed before being returned to the caller, regardless of success/failure. The `try/except: pass` around the import means missing redact module degrades silently — script output flows through unmodified in that case.

### Failure encoding

| Condition | Return |
|---|---|
| Exit code 0 | `(True, stdout.strip())` |
| Exit code != 0 | `(False, "Script exited with code N\nstderr:\n...\nstdout:\n...")` (only non-empty streams included) |
| `subprocess.TimeoutExpired` | `(False, f"Script timed out after {script_timeout}s: {path}")` |
| Any other exception | `(False, f"Script execution failed: {exc}")` |

## Script timeout precedence (`_get_script_timeout`, scheduler.py:622)

Resolution order:

1. Module-patched `_SCRIPT_TIMEOUT` (test/runtime override; only used if it diverges from `_DEFAULT_SCRIPT_TIMEOUT`)
2. `HERMES_CRON_SCRIPT_TIMEOUT` env var (positive int)
3. `cron.script_timeout_seconds` in the user config
4. `_DEFAULT_SCRIPT_TIMEOUT` (the module-level fallback)

Each step logs a warning and falls through on invalid values.

## Implications for Apprentice

- A no_agent cron job is the cheapest way for the Observer to do periodic polling (e.g. tail the session DB, snapshot metrics, run a watchdog) without budgeting tokens for it.
- The `~/.hermes/scripts/` sandbox is enforced by resolution, not by chroot — the Apprentice writes its scripts into that exact dir or `_run_job_script` blocks them.
- Script extension dictates interpreter. Apprentice helper scripts that aren't `.sh`/`.bash` are Python, period; there's no way to register a third interpreter without patching `_run_job_script`.
- `cron.script_timeout_seconds` (or `HERMES_CRON_SCRIPT_TIMEOUT`) needs to be tuned if any Apprentice training trigger needs >`_DEFAULT_SCRIPT_TIMEOUT` to run.
- For Apprentice pipelines that DO want the agent in the loop (Detector firing the Dataset Builder, e.g.), the same job can omit `no_agent` and supply a pre-run script — its stdout flows into the agent's prompt, and the `wakeAgent: false` gate can cancel a tick if the script decides nothing happened.
- The Observer/Detector should rely on `_process_job` always calling `mark_job_run` (even from the exception path at line 1690) — `last_status` is authoritative for whether to fire downstream pipeline steps.

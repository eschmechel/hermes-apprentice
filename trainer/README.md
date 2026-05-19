# Apprentice Trainer

Python package that fine-tunes [Qwen2.5-1.5B-Instruct](https://huggingface.co/Qwen/Qwen2.5-1.5B-Instruct) into a task specialist using [Unsloth](https://github.com/unslothai/unsloth) QLoRA (4-bit base + LoRA rank-16 adapters). The training set comes from `dataset-builder`'s versioned output:

```
~/apprentice/datasets/<pattern-id>/v<N>/{train,val,test}.jsonl.gz
```

Each line is a Hermes chat-template record: `{"messages": [{"role": "system", "content": "…"}, {"role": "user", "content": "…"}, {"role": "assistant", "content": "…"}]}`.

## Hardware

Training requires an NVIDIA GPU. Tested targets:

| Target | VRAM | Profile (trainer-02..03) |
|---|---|---|
| RTX 4060 Mobile / Max-Q (laptop) | 6–8 GB | tight batch, gradient accumulation |
| RTX 2080 Ti (desktop, 11 GB) | 11 GB | standard QLoRA settings |
| RunPod A100 80 GB | 80 GB | large batch, no grad accum needed |

CPU-only hosts can still run `--check-only` (dataset + config validation) and `--help`.

## Install

Pick the install line matching your CUDA toolkit. Unsloth ships pre-built wheels via PyPI extras; the extra name encodes CUDA + torch version. Newer combos work for both 30/40-series and A100.

```bash
# CUDA 12.1 + torch 2.4 (works for 4060, 2080 Ti, A100 on most modern drivers)
uv pip install -e .
uv pip install "unsloth[cu121-torch240] @ git+https://github.com/unslothai/unsloth.git"
```

If your CUDA version differs, browse [Unsloth's install matrix](https://github.com/unslothai/unsloth#-installation-instructions) and substitute the extra (e.g. `cu118-torch230`, `cu124-torch250`).

## Run

```bash
apprentice-train \
    --dataset-dir ~/apprentice/datasets/<pattern-id>/v1 \
    --output-dir  ~/apprentice/checkpoints/<pattern-id>/v1 \
    --max-steps   60 \
    --batch-size  2 \
    --grad-accum  4
```

Output:

```
~/apprentice/checkpoints/<pattern-id>/v1/
├── checkpoints/                  # transformers Trainer intermediate state (save_strategy=no, kept empty)
└── lora-adapter/                 # final LoRA weights + tokenizer config
    ├── adapter_config.json
    ├── adapter_model.safetensors
    ├── tokenizer.json
    └── …
```

The LoRA adapter is what trainer-04 (`model_exporter.py`) merges with the base weights to produce a deployable fp16 model.

## CPU smoke test

To verify the script parses args + reads the dataset on a pure-CPU host (no Unsloth, no GPU):

```bash
apprentice-train --check-only \
    --dataset-dir ~/apprentice/datasets/<pattern-id>/v1 \
    --output-dir  /tmp/ignored
```

This loads `train.jsonl.gz` and `val.jsonl.gz`, validates each row has a `messages` field, and exits. Used by CI and the dataset-builder regression check.

## Custom profiles for your own hardware

The three profiles shipped under `trainer/profiles/` cover the targets we test on. If you're training on different hardware, write your own YAML — any key matching an `apprentice-train` long-option `dest` becomes a default:

```yaml
# ~/.config/apprentice-trainer/my-3090.yaml — example
base_model: "unsloth/Qwen2.5-1.5B-Instruct-bnb-4bit"
load_in_4bit: true
max_seq_len: 4096
lora_rank: 16

batch_size: 4
grad_accum: 4
learning_rate: 0.0002
warmup_steps: 5
max_steps: 100
seed: 3407
```

Three ways to apply it:

```bash
# 1. Explicit per-run.
apprentice-train --profile ~/.config/apprentice-trainer/my-3090.yaml ...

# 2. Sticky default for this shell.
export APPRENTICE_TRAINER_PROFILE=~/.config/apprentice-trainer/my-3090.yaml
apprentice-train ...

# 3. Sticky default for your account — drop the export into ~/.bashrc / ~/.zshrc.
```

Unknown keys are reported as a `WARNING` in the JSON log and ignored — safe to add `comment:` or `# vim: ft=yaml` headers. CLI flags always win over the profile, so `--batch-size 2` on the command line overrides the profile's `batch_size: 4`.

## Defaults

| Flag | Default | Notes |
|---|---|---|
| `--base-model` | `unsloth/Qwen2.5-1.5B-Instruct-bnb-4bit` | Unsloth-prequantized; loads ~5× faster than fresh quantizing the FP16 base |
| `--max-seq-len` | 2048 | Plenty for chat-template rows; reduce to 1024 on 4060 if OOM |
| `--lora-rank` | 16 | Required by trainer-01 acceptance |
| `--max-steps` | 60 | Per-pattern target — small enough to test, large enough to actually adapt |
| `--batch-size` | 2 | Per-device; multiply by `--grad-accum` for effective batch |
| `--grad-accum` | 4 | Effective batch 8 |
| `--learning-rate` | 2e-4 | Standard QLoRA Unsloth default |
| `--seed` | 3407 | Unsloth's lucky number; deterministic shuffling |

These are tuned for the 11 GB 2080 Ti. Profile YAMLs (trainer-02 / trainer-03) override per device.

## Logging

JSON lines to stderr, one per event:

```json
{"time":"2026-05-19T…","level":"INFO","msg":"trainer starting","component":"apprentice_trainer",
 "dataset_dir":"…","cuda_device":"NVIDIA GeForce RTX 4060 Laptop GPU","cuda_capability":"8.9"}
```

Matches the observer / detector / dataset-builder style for downstream log aggregation.

#!/usr/bin/env bash
# Apprentice demo — end-to-end from session log to serving a specialist.
# Runs offline, no GPU required (all subprocesses mocked or CPU-only).
# Prerequisites: Go 1.22+, Python 3.12+, uv (optional).
set -euo pipefail

DEMO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
APPRENTICE_ROOT="/tmp/apprentice-demo-$$"
TIMESTAMP="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

export APPRENTICE_ROOT
export APPRENTICE_PROXY_URL="http://localhost:8083"
export APPRENTICE_OBSERVER_URL="http://localhost:8080"
export APPRENTICE_BASE_MODEL="unsloth/Qwen2.5-1.5B-Instruct-bnb-4bit"

echo "============================================"
echo " Apprentice v0.2 Demo"
echo " Timestamp: $TIMESTAMP"
echo " Root:      $APPRENTICE_ROOT"
echo "============================================"
echo ""

# ── Step 0: Init ──────────────────────────────────────────────────────────────
init() {
  rm -rf "$APPRENTICE_ROOT"
  mkdir -p "$APPRENTICE_ROOT"/{proxy,datasets,registry,decisions,outbox,keys,patterns,candidates,cost,canary,alias,skills}
  echo "[0/8] Directory structure created."
}

# ── Step 1: Session log ────────────────────────────────────────────────────────
write_session_log() {
  local DB="$APPRENTICE_ROOT/hermes-state.db"
  sqlite3 "$DB" '
    CREATE TABLE conversations (
      id TEXT PRIMARY KEY, created_at TEXT, model TEXT,
      messages TEXT, total_tokens INTEGER
    );
    INSERT INTO conversations VALUES
    ("s1", "'"$TIMESTAMP"'", "claude-sonnet-4-20250514",
     '"'"'[{"role":"user","content":"Extract the SKU and quantity from this order email: Hi, I need 3 units of AX-7 and 2 units of BX-12. Thanks!"},{"role":"assistant","content":"Here are the items:\n- SKU: AX-7, Quantity: 3\n- SKU: BX-12, Quantity: 2"}]'"'"', 100),
    ("s2", "'"$TIMESTAMP"'", "claude-sonnet-4-20250514",
     '"'"'[{"role":"user","content":"Parse this invoice: Widget W-99 x5, Gadget G-44 x1, Cable C-77 x20"},{"role":"assistant","content":"| SKU | Quantity |\n|-----|----------|\n| W-99 | 5 |\n| G-44 | 1 |\n| C-77 | 20 |"}]'"'"', 120),
    ("s3", "'"$TIMESTAMP"'", "claude-sonnet-4-20250514",
     '"'"'[{"role":"user","content":"Inventory check: ALPHA-1 qty 12, BETA-2 qty 7"},{"role":"assistant","content":"SKU: ALPHA-1 → Quantity: 12\nSKU: BETA-2  → Quantity: 7"}]'"'"', 80);
  '

  # Observer: read SQLite → JSON pairs
  cat > "$APPRENTICE_ROOT/observer-pairs.jsonl" <<'EOO'
{"id":"p1","session_id":"s1","input":"Extract the SKU and quantity from this order email: Hi, I need 3 units of AX-7 and 2 units of BX-12. Thanks!","output":"Here are the items:\n- SKU: AX-7, Quantity: 3\n- SKU: BX-12, Quantity: 2"}
{"id":"p2","session_id":"s2","input":"Parse this invoice: Widget W-99 x5, Gadget G-44 x1, Cable C-77 x20","output":"| SKU | Quantity |\n|-----|----------|\n| W-99 | 5 |\n| G-44 | 1 |\n| C-77 | 20 |"}
{"id":"p3","session_id":"s3","input":"Inventory check: ALPHA-1 qty 12, BETA-2 qty 7","output":"SKU: ALPHA-1 → Quantity: 12\nSKU: BETA-2  → Quantity: 7"}
EOO

  echo "[1/8] 3 session pairs written to $APPRENTICE_ROOT/observer-pairs.jsonl."
}

# ── Step 2: Detect pattern ──────────────────────────────────────────────────────
detect_pattern() {
  local PATTERN_ID="sku-extraction"
  local DESCRIPTION="Extract SKU and quantity pairs from unstructured text."

  cat > "$APPRENTICE_ROOT/patterns/$PATTERN_ID/manifest.json" <<EOP
{
  "id": "$PATTERN_ID",
  "description": "$DESCRIPTION",
  "centroid": [0.0],
  "sample_count": 3,
  "detected_at": "$TIMESTAMP",
  "status": "pending"
}
EOP

  echo "[2/8] Pattern '$PATTERN_ID' detected ($DESCRIPTION)."
  echo "       Manual approval needed: reply 'train gc-$(echo -n "$PATTERN_ID" | sha256sum | head -c 8)'"
}

# ── Step 3: Build dataset ───────────────────────────────────────────────────────
build_dataset() {
  local PATTERN_ID="sku-extraction"
  local DS_DIR="$APPRENTICE_ROOT/datasets/$PATTERN_ID/v1"
  mkdir -p "$DS_DIR"

  # Write train/val/test JSONL.gz
  for split in train val test; do
    cat > "/tmp/$split.jsonl" <<EOL
{"messages":[{"role":"system","content":"Extract SKU and quantity pairs from the input. Output format: SKU: <id>, Quantity: <n> per line."},{"role":"user","content":"Order: 5x AX-7, 2x BX-12"},{"role":"assistant","content":"SKU: AX-7, Quantity: 5\nSKU: BX-12, Quantity: 2"}]}
EOL
    gzip -c "/tmp/$split.jsonl" > "$DS_DIR/$split.jsonl.gz"
  done

  cat > "$DS_DIR/manifest.json" <<EOM
{"version":1,"pattern_id":"$PATTERN_ID","original_count":3,"augmented_count":0,"train_count":2,"val_count":1,"test_count":1,"total_count":4,"created_at":"$TIMESTAMP"}
EOM

  echo "[3/8] Dataset built: 4 records (2 train / 1 val / 1 test) at $DS_DIR."
}

# ── Step 4: Train (simulated) ──────────────────────────────────────────────────
train() {
  local PATTERN_ID="sku-extraction"
  local CKPT="$APPRENTICE_ROOT/checkpoints/$PATTERN_ID/v1"
  mkdir -p "$CKPT/lora-adapter"

  # Write training manifest
  cat > "$CKPT/training_manifest.json" <<EOM
{
  "schema_version":1,
  "pattern_id":"$PATTERN_ID",
  "base_model":"unsloth/Qwen2.5-1.5B-Instruct-bnb-4bit",
  "training_duration_seconds":900,
  "exit_code":0,
  "created_at":"$TIMESTAMP"
}
EOM

  echo "[4/8] Training complete (simulated, 900s on 2080 Ti)."
  echo "       Checkpoint: $CKPT"
}

# ── Step 5: Merge ───────────────────────────────────────────────────────────────
merge_model() {
  local PATTERN_ID="sku-extraction"
  local MERGED="$APPRENTICE_ROOT/merged/$PATTERN_ID/v1"
  mkdir -p "$MERGED"

  cp "$APPRENTICE_ROOT/checkpoints/$PATTERN_ID/v1/training_manifest.json" "$MERGED/"

  echo "[5/8] Model merged to fp16 at $MERGED."
}

# ── Step 6: Validate ──────────────────────────────────────────────────────────────
validate() {
  local PATTERN_ID="sku-extraction"
  local MERGED="$APPRENTICE_ROOT/merged/$PATTERN_ID/v1"
  local REG_DIR="$APPRENTICE_ROOT/registry/$PATTERN_ID/v1"
  mkdir -p "$REG_DIR"

  # Simulated promotion gate pass
  cat > "$REG_DIR/registry_manifest.json" <<EOM
{
  "schema_version":1,
  "pattern_id":"$PATTERN_ID",
  "version":1,
  "promoted_at":"$TIMESTAMP",
  "model_dir":"$REG_DIR",
  "scores":{"exact_match":0.92,"f1":0.94},
  "base_model":"unsloth/Qwen2.5-1.5B-Instruct-bnb-4bit"
}
EOM

  mkdir -p "$REG_DIR/latest"
  cp "$REG_DIR/registry_manifest.json" "$REG_DIR/latest/"

  echo "[6/8] Validation passed: exact_match=0.92, F1=0.94 (gate: 10pp margin OK)."
  echo "       Promoted to $REG_DIR"
}

# ── Step 7: Register pattern in proxy ────────────────────────────────────────────
register_pattern() {
  local PATTERN_ID="sku-extraction"
  local PROXY_STATE="$APPRENTICE_ROOT/proxy"
  mkdir -p "$PROXY_STATE"

  cat > "$PROXY_STATE/patterns.json" <<EOP
[{"id":"$PATTERN_ID","description":"Extract SKU and quantity pairs","centroid":[],"tenant_id":""}]
EOP

  echo "[7/8] Pattern registered in proxy state at $PROXY_STATE/patterns.json."
}

# ── Step 8: Summary ──────────────────────────────────────────────────────────────
summary() {
  echo ""
  echo "==============================================="
  echo " Demo Complete!"
  echo "==============================================="
  echo ""
  echo "Dashboard URL:        http://localhost:8083/dashboard"
  echo "Proxy metrics:        http://localhost:8083/metrics"
  echo "Registry:             http://localhost:8082/registry/sku-extraction/latest"
  echo "Canary state:         http://localhost:8083/canary/state"
  echo ""
  echo "To serve the model (requires GPU):"
  echo "  apprentice-serve --model-dir $APPRENTICE_ROOT/registry/sku-extraction/v1 --port 8000 &"
  echo ""
  echo "To run the proxy:"
  echo "  cd $DEMO_ROOT/proxy && go run . serve \\"
  echo "    --listen :8083 --state-dir $APPRENTICE_ROOT/proxy \\"
  echo "    --serve-url http://localhost:8000 --residency-url http://localhost:8001 \\"
  echo "    --upstream-url https://openrouter.ai/api/v1 \\"
  echo "    --canary-state-dir $APPRENTICE_ROOT/canary \\"
  echo "    --shadow-rate 0"
  echo ""
  echo "To test:"
  echo "  curl -s http://localhost:8083/v1/chat/completions \\"
  echo "    -H 'Content-Type: application/json' \\"
  echo "    -d '{\"model\":\"openai/gpt-4o-mini\",\"messages\":[{\"role\":\"user\",\"content\":\"Order: 3x AX-7, 1x BX-12\"}],\"max_tokens\":50}'"
  echo ""
  echo "State written to: $APPRENTICE_ROOT"
  echo "Timestamp: $TIMESTAMP"
}

# ── Run ─────────────────────────────────────────────────────────────────────────
init
write_session_log
detect_pattern
build_dataset
train
merge_model
validate
register_pattern
summary

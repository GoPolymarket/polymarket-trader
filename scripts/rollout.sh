#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  ./scripts/rollout.sh <phase> [config]

Phases:
  paper       paper account with real paper fills (dry_run=false)
  shadow      live connectivity + strategy signals, but no order placement
  live-small  conservative live with capped size/risk
  live        full live (uses config values)

Examples:
  ./scripts/rollout.sh paper
  ./scripts/rollout.sh shadow
  ./scripts/rollout.sh live-small config.yaml
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" || "${1:-}" == "" ]]; then
  usage
  exit 0
fi

phase="$1"
config_path="${2:-config.yaml}"

echo "[rollout] phase=${phase} config=${config_path}"
exec go run ./cmd/trader -config "${config_path}" -phase "${phase}"

#!/usr/bin/env bash
set -euo pipefail

role="${1:-}"
mode="${2:-plan}"
company="${3:-unknown-company}"
service="${4:-$(basename "$PWD")}"

if [[ -z "$role" ]]; then
  echo "usage: scripts/ci-agent-role.sh <role> [mode] [company] [service]"
  exit 2
fi

artifacts_dir="${ARTIFACTS_DIR:-artifacts/agent-team}"
mkdir -p "$artifacts_dir"
summary_file="$artifacts_dir/${role}-summary.md"

ts_utc="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"

{
  echo "# Agent Role Report"
  echo
  echo "- role: $role"
  echo "- mode: $mode"
  echo "- company: $company"
  echo "- service: $service"
  echo "- timestamp_utc: $ts_utc"
  echo
} > "$summary_file"

run_and_log() {
  local title="$1"
  shift
  local logfile="$artifacts_dir/${role}-${title}.log"
  echo "## $title" >> "$summary_file"
  echo '```bash' >> "$summary_file"
  printf '%q ' "$@" >> "$summary_file"
  echo >> "$summary_file"
  echo '```' >> "$summary_file"
  "$@" > "$logfile" 2>&1
  echo "- output: \`$logfile\`" >> "$summary_file"
  echo >> "$summary_file"
}

case "$role" in
  product)
    go_files="$(find . -type f -name "*.go" | wc -l | tr -d ' ')"
    pkg_count="$(go list ./... | wc -l | tr -d ' ')"
    {
      echo "## scope-snapshot"
      echo "- go_files: $go_files"
      echo "- packages: $pkg_count"
      echo "- recommendation: split next batch by package boundaries and dependency risks."
      echo
    } >> "$summary_file"
    ;;

  architect)
    run_and_log module-inventory go list ./...
    ;;

  developer)
    run_and_log compile-gate go test ./... -run '^$' -count=1
    ;;

  qa)
    if [[ "$mode" == "execute" && "${AGENT_QA_FULL_TEST:-false}" == "true" ]]; then
      run_and_log qa-tests go test ./... -count=1
    else
      run_and_log qa-compile-gate go test ./... -run '^$' -count=1
      {
        echo "## qa-note"
        echo "- full tests skipped (set AGENT_QA_FULL_TEST=true in execute mode to run full suite)."
        echo
      } >> "$summary_file"
    fi
    ;;

  sre)
    {
      echo "## runtime-artifacts"
      if [[ -f Dockerfile ]]; then
        echo "- Dockerfile: present"
      else
        echo "- Dockerfile: missing"
      fi
      if [[ -f docker-compose.yml ]]; then
        echo "- docker-compose.yml: present"
      else
        echo "- docker-compose.yml: missing"
      fi
      echo
    } >> "$summary_file"
    ;;

  security)
    run_and_log module-list go list -m all
    findings_file="$artifacts_dir/security-potential-secrets.txt"
    if command -v rg >/dev/null 2>&1; then
      rg -n --glob '*.go' --glob '*.yaml' --glob '*.yml' '(?i)(api[_-]?secret|private[_-]?key|passphrase)\s*[:=]\s*"[^"]+"' . > "$findings_file" || true
    else
      grep -RniE --include='*.go' --include='*.yaml' --include='*.yml' '(api[_-]?secret|private[_-]?key|passphrase)[[:space:]]*[:=][[:space:]]*"[^"]+"' . > "$findings_file" || true
    fi
    {
      echo "## secret-scan"
      echo "- findings: \`$findings_file\`"
      echo "- note: manual review required for any non-empty findings file."
      echo
    } >> "$summary_file"
    ;;

  *)
    echo "unsupported role: $role" | tee -a "$summary_file"
    exit 2
    ;;
esac

{
  echo "## status"
  echo "- result: pass"
} >> "$summary_file"

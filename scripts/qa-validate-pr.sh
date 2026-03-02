#!/usr/bin/env bash
set -euo pipefail

pr_number="${1:-}"
auto_merge="${2:-${QA_AUTO_MERGE:-true}}"
repo="${GITHUB_REPOSITORY:-}"
artifacts_dir="${ARTIFACTS_DIR:-artifacts/qa}"

if [[ -z "$pr_number" ]]; then
  echo "usage: scripts/qa-validate-pr.sh <pr-number> [auto-merge:true|false]"
  exit 2
fi

if [[ -z "$repo" ]]; then
  echo "GITHUB_REPOSITORY is required"
  exit 2
fi

if [[ -z "${GH_TOKEN:-}" ]]; then
  echo "GH_TOKEN is required"
  exit 2
fi

mkdir -p "$artifacts_dir"
summary_file="$artifacts_dir/qa-summary.md"

run_cmd() {
  local title="$1"
  local cmd="$2"
  local logfile="$artifacts_dir/${title}.log"

  {
    echo "## ${title}"
    echo '```bash'
    echo "$cmd"
    echo '```'
  } >> "$summary_file"

  if bash -lc "$cmd" >"$logfile" 2>&1; then
    echo "- result: pass" >> "$summary_file"
    echo "- log: \`${logfile}\`" >> "$summary_file"
    echo >> "$summary_file"
    return 0
  fi

  echo "- result: fail" >> "$summary_file"
  echo "- log: \`${logfile}\`" >> "$summary_file"
  echo >> "$summary_file"
  return 1
}

sanitize_cmd() {
  local input="$1"
  if [[ "$input" =~ ^go[[:space:]]+test[[:space:]]+ ]]; then
    echo "$input"
    return
  fi
  if [[ "$input" =~ ^make[[:space:]]+test ]]; then
    echo "$input"
    return
  fi
  if [[ "$input" =~ ^make[[:space:]]+qa ]]; then
    echo "$input"
    return
  fi
  echo ""
}

pr_json="$(gh pr view "$pr_number" --repo "$repo" --json title,body,url,state,isDraft,baseRefName,headRefName,number)"

mapfile -t pr_ctx < <(PR_JSON="$pr_json" python3 - <<"PY"
import json
import os
import re

obj = json.loads(os.environ["PR_JSON"])
body = obj.get("body") or ""
issue = ""
patterns = [
    r"(?i)(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\s+#(\d+)",
    r"#(\d+)",
]
for pattern in patterns:
    m = re.search(pattern, body)
    if m:
        issue = m.group(1)
        break

print(obj.get("title", ""))
print(obj.get("url", ""))
print(str(obj.get("isDraft", False)).lower())
print(obj.get("state", ""))
print(issue)
print(obj.get("baseRefName", ""))
print(obj.get("headRefName", ""))
PY
)

pr_title="${pr_ctx[0]:-}"
pr_url="${pr_ctx[1]:-}"
is_draft="${pr_ctx[2]:-false}"
pr_state="${pr_ctx[3]:-}"
issue_number="${pr_ctx[4]:-}"
base_ref="${pr_ctx[5]:-main}"
head_ref="${pr_ctx[6]:-}"

if [[ "$pr_state" != "OPEN" ]]; then
  echo "PR #$pr_number is not open"
  exit 1
fi

if [[ -z "$issue_number" ]]; then
  echo "PR #$pr_number is missing linked issue (e.g., Closes #123)" >&2
  exit 1
fi

issue_json="$(gh issue view "$issue_number" --repo "$repo" --json title,body,url,number)"
mapfile -t issue_ctx < <(ISSUE_JSON="$issue_json" python3 - <<"PY"
import json
import os
import re

obj = json.loads(os.environ["ISSUE_JSON"])
body = obj.get("body") or ""

def field(name):
    pattern = re.compile(rf"^###\s+{re.escape(name)}\s*$", re.I | re.M)
    m = pattern.search(body)
    if not m:
        return ""
    start = m.end()
    rest = body[start:]
    nxt = re.search(r"^###\s+", rest, re.M)
    chunk = rest[:nxt.start()] if nxt else rest
    chunk = chunk.strip()
    if chunk == "_No response_":
        return ""
    return chunk

print(obj.get("title", ""))
print(obj.get("url", ""))
print(field("Targeted QA Command (optional)"))
print(field("Regression Command (optional)"))
PY
)

issue_title="${issue_ctx[0]:-}"
issue_url="${issue_ctx[1]:-}"
raw_targeted_cmd="${issue_ctx[2]:-}"
raw_regression_cmd="${issue_ctx[3]:-}"

default_targeted="go test ./... -run '^$' -count=1"
default_regression="go test ./... -count=1"

if [[ -d pkg ]]; then
  default_targeted="go test ./pkg/... -run '^$' -count=1"
fi

targeted_cmd="$(sanitize_cmd "$raw_targeted_cmd")"
regression_cmd="$(sanitize_cmd "$raw_regression_cmd")"

if [[ -z "$targeted_cmd" ]]; then targeted_cmd="$default_targeted"; fi
if [[ -z "$regression_cmd" ]]; then regression_cmd="$default_regression"; fi

{
  echo "# QA Validation Report"
  echo
  echo "- pr: #${pr_number}"
  echo "- pr_title: ${pr_title}"
  echo "- pr_url: ${pr_url}"
  echo "- base_branch: ${base_ref}"
  echo "- head_branch: ${head_ref}"
  echo "- linked_issue: #${issue_number}"
  echo "- issue_title: ${issue_title}"
  echo "- issue_url: ${issue_url}"
  echo "- auto_merge: ${auto_merge}"
  echo "- generated_at_utc: $(date -u +"%Y-%m-%dT%H:%M:%SZ")"
  echo
} > "$summary_file"

status="pass"
run_cmd "targeted-validation" "$targeted_cmd" || status="fail"
run_cmd "regression" "$regression_cmd" || status="fail"

if [[ "$status" != "pass" ]]; then
  echo "## status" >> "$summary_file"
  echo "- result: fail" >> "$summary_file"
  gh pr edit "$pr_number" --repo "$repo" --add-label qa-failed --remove-label qa-passed >/dev/null || true
  gh pr comment "$pr_number" --repo "$repo" --body "QA failed for PR #$pr_number. Please see workflow artifacts for logs." >/dev/null || true
  exit 1
fi

echo "## status" >> "$summary_file"
echo "- result: pass" >> "$summary_file"

if [[ "$is_draft" == "true" ]]; then
  gh pr ready "$pr_number" --repo "$repo" >/dev/null || true
fi

gh pr edit "$pr_number" --repo "$repo" --add-label qa-passed --remove-label qa-failed >/dev/null || true
gh pr comment "$pr_number" --repo "$repo" --body "QA passed for PR #$pr_number (targeted + regression)." >/dev/null || true

echo "pr_number=$pr_number" > "$artifacts_dir/context.env"
echo "issue_number=$issue_number" >> "$artifacts_dir/context.env"
echo "auto_merge=$auto_merge" >> "$artifacts_dir/context.env"

cat "$artifacts_dir/context.env"

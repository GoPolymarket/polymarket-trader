#!/usr/bin/env bash
set -euo pipefail

issue_number="${1:-}"
base_branch="${2:-main}"
repo="${GITHUB_REPOSITORY:-}"
artifacts_dir="${ARTIFACTS_DIR:-artifacts/dev}"

if [[ -z "$issue_number" ]]; then
  echo "usage: scripts/dev-bot-implement.sh <issue-number> [base-branch]"
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

ensure_label() {
  local name="$1"
  local color="$2"
  local desc="$3"
  gh label create "$name" --repo "$repo" --color "$color" --description "$desc" >/dev/null 2>&1 || true
}

ensure_label "dev-bot" "0E8A16" "Created by dev automation"
ensure_label "ready-for-qa" "FBCA04" "Ready for QA validation"

issue_json="$(gh issue view "$issue_number" --repo "$repo" --json title,body,state,url,labels,number)"

mapfile -t parsed < <(ISSUE_JSON="$issue_json" python3 - <<"PY"
import json
import os

obj = json.loads(os.environ["ISSUE_JSON"])
labels = {x["name"] for x in obj.get("labels", [])}
print(obj.get("title", ""))
print(obj.get("body", ""))
print(obj.get("state", ""))
print(obj.get("url", ""))
print("1" if "ready-for-dev" in labels else "0")
PY
)

issue_title="${parsed[0]:-}"
issue_body="${parsed[1]:-}"
issue_state="${parsed[2]:-}"
issue_url="${parsed[3]:-}"
ready_for_dev="${parsed[4]:-0}"

if [[ "$issue_state" != "OPEN" ]]; then
  echo "issue #$issue_number is not open"
  exit 1
fi

if [[ "$ready_for_dev" != "1" ]]; then
  echo "issue #$issue_number is not labeled ready-for-dev"
  exit 1
fi

branch="bot/issue-${issue_number}"

# Keep bot changes deterministic: always start from latest base branch.
git fetch origin "$base_branch" --depth=1
git checkout -B "$branch" "origin/$base_branch"

workdir=".agent/workitems/issue-${issue_number}"
mkdir -p "$workdir"
report_file="$workdir/developer-output.md"

cat > "$report_file" <<EOF2
# Developer Bot Output

- issue: #${issue_number}
- source: ${issue_url}
- generated_at_utc: $(date -u +"%Y-%m-%dT%H:%M:%SZ")

## Requirement Snapshot

${issue_title}

## Implementation

- Prepared branch \`${branch}\`
- Captured requirement context
EOF2

patch_file="$(mktemp)"

ISSUE_BODY="$issue_body" python3 - "$patch_file" <<"PY"
import os
import re
import sys

body = os.environ.get("ISSUE_BODY", "")
match = re.search(r"```diff\n(.*?)\n```", body, re.S | re.I)
if match:
    patch = match.group(1).strip()
    with open(sys.argv[1], "w", encoding="utf-8") as f:
        if patch:
            f.write(patch + "\n")
else:
    with open(sys.argv[1], "w", encoding="utf-8") as f:
        f.write("")
PY

applied_patch="false"
if [[ -s "$patch_file" ]]; then
  if git apply "$patch_file" >/dev/null 2>&1; then
    applied_patch="true"
  elif git apply --reject --whitespace=fix "$patch_file" >/dev/null 2>&1; then
    applied_patch="true"
  fi
fi

if [[ "$applied_patch" == "true" ]]; then
  {
    echo "- Applied patch from issue body"
  } >> "$report_file"
else
  {
    echo "- No valid diff patch found in issue body"
    echo "- Generated planning artifact for manual/agent code implementation"
  } >> "$report_file"
fi

# Ensure there is always a concrete output for traceability.
printf "%s\n" "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" > "$workdir/heartbeat.txt"

git add -A
if git diff --cached --quiet; then
  echo "No staged changes generated" >&2
  exit 1
fi

git commit -m "feat(dev-bot): prepare delivery for issue #${issue_number}" >/dev/null
git push --set-upstream origin "$branch" --force

pr_title="feat: ${issue_title} (#${issue_number})"
pr_body=$(cat <<EOF2
Automated developer delivery for issue #${issue_number}.

Closes #${issue_number}

## Dev Output
- Branch: \`${branch}\`
- Artifacts: \`.agent/workitems/issue-${issue_number}\`
- Status: Ready for QA validation and regression

Source issue: ${issue_url}
EOF2
)

existing_pr="$(gh pr list --repo "$repo" --head "$branch" --state open --json number --jq '.[0].number // empty')"
if [[ -n "$existing_pr" ]]; then
  gh pr edit "$existing_pr" --repo "$repo" --title "$pr_title" --body "$pr_body" --add-label dev-bot --add-label ready-for-qa >/dev/null
  pr_number="$existing_pr"
else
  gh pr create --repo "$repo" --base "$base_branch" --head "$branch" --title "$pr_title" --body "$pr_body" --draft --label dev-bot --label ready-for-qa >/dev/null
  pr_number="$(gh pr list --repo "$repo" --head "$branch" --state open --json number --jq '.[0].number // empty')"
fi

if [[ -z "$pr_number" ]]; then
  echo "failed to create or locate PR for $branch" >&2
  exit 1
fi

gh issue comment "$issue_number" --repo "$repo" --body "Dev bot created/updated PR #${pr_number} from \`${branch}\`." >/dev/null

echo "issue_number=${issue_number}" > "$artifacts_dir/context.env"
echo "branch=${branch}" >> "$artifacts_dir/context.env"
echo "pr_number=${pr_number}" >> "$artifacts_dir/context.env"
echo "issue_url=${issue_url}" >> "$artifacts_dir/context.env"

cat "$artifacts_dir/context.env"

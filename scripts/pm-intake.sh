#!/usr/bin/env bash
set -euo pipefail

issue_number="${1:-}"
repo="${GITHUB_REPOSITORY:-}"

if [[ -z "$issue_number" ]]; then
  echo "usage: scripts/pm-intake.sh <issue-number>"
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

ensure_label() {
  local name="$1"
  local color="$2"
  local desc="$3"
  gh label create "$name" --repo "$repo" --color "$color" --description "$desc" >/dev/null 2>&1 || true
}

ensure_label "pm-requirement" "1F6FEB" "PM requirement intake"
ensure_label "needs-pm-update" "B60205" "PM issue missing required sections"
ensure_label "pm-approved" "0E8A16" "PM issue passed intake"
ensure_label "ready-for-dev" "5319E7" "Ready for developer automation"

issue_json="$(gh issue view "$issue_number" --repo "$repo" --json title,body,state,url,labels,number)"

mapfile -t parsed < <(ISSUE_JSON="$issue_json" python3 - <<"PY"
import json
import os
import re

obj = json.loads(os.environ["ISSUE_JSON"])
body = obj.get("body") or ""
labels = {x["name"] for x in obj.get("labels", [])}
required = [
    "Business Context",
    "Objective",
    "Scope",
    "Acceptance Criteria",
]
missing = []
for item in required:
    pattern = re.compile(rf"^###\s+{re.escape(item)}\s*$", re.I | re.M)
    if not pattern.search(body):
        missing.append(item)

print(obj.get("title", ""))
print(obj.get("url", ""))
print(obj.get("state", ""))
print(",".join(missing))
print("1" if "pm-requirement" in labels else "0")
PY
)

title="${parsed[0]:-}"
issue_url="${parsed[1]:-}"
state="${parsed[2]:-}"
missing_csv="${parsed[3]:-}"
has_pm_label="${parsed[4]:-0}"

if [[ "$state" != "OPEN" ]]; then
  echo "issue #$issue_number is not open"
  exit 1
fi

if [[ "$has_pm_label" != "1" ]]; then
  gh issue edit "$issue_number" --repo "$repo" --add-label pm-requirement >/dev/null
fi

if [[ -n "$missing_csv" ]]; then
  gh issue edit "$issue_number" --repo "$repo" --add-label needs-pm-update >/dev/null || true
  gh issue edit "$issue_number" --repo "$repo" --remove-label ready-for-dev --remove-label pm-approved >/dev/null || true

  IFS="," read -r -a missing_items <<< "$missing_csv"
  msg="PM intake failed for #${issue_number}. Missing sections:"
  for item in "${missing_items[@]}"; do
    msg+=$'\n- '
    msg+="$item"
  done
  msg+=$'\n\nPlease complete the issue template and rerun PM intake.'

  gh issue comment "$issue_number" --repo "$repo" --body "$msg" >/dev/null
  echo "$msg"
  exit 1
fi

gh issue edit "$issue_number" --repo "$repo" --add-label pm-approved --add-label ready-for-dev >/dev/null
gh issue edit "$issue_number" --repo "$repo" --remove-label needs-pm-update >/dev/null || true

gh issue comment "$issue_number" --repo "$repo" --body "PM intake passed for #$issue_number: \"$title\". Marked as **ready-for-dev**." >/dev/null

echo "PM intake passed: $issue_url"

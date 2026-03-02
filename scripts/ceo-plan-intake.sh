#!/usr/bin/env bash
set -euo pipefail

issue_number="${1:-}"
repo="${GITHUB_REPOSITORY:-}"

if [[ -z "$issue_number" ]]; then
  echo "usage: scripts/ceo-plan-intake.sh <issue-number>"
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

ensure_label "ceo-plan" "5319E7" "CEO strategic plan"
ensure_label "needs-ceo-update" "B60205" "CEO plan missing required sections"
ensure_label "ceo-approved" "0E8A16" "CEO plan passed intake"
ensure_label "ready-for-pm-breakdown" "FBCA04" "Ready for PM manager decomposition"

issue_json="$(gh issue view "$issue_number" --repo "$repo" --json title,body,state,url,labels,number)"

mapfile -t parsed < <(ISSUE_JSON="$issue_json" python3 - <<"PY"
import json
import os
import re

obj = json.loads(os.environ["ISSUE_JSON"])
body = obj.get("body") or ""
labels = {x["name"] for x in obj.get("labels", [])}
required = [
    "Vision",
    "North Star Metric",
    "90-Day Objectives",
    "Strategic Initiatives",
    "In Scope",
    "Out of Scope",
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
print("1" if "ceo-plan" in labels else "0")
PY
)

title="${parsed[0]:-}"
issue_url="${parsed[1]:-}"
state="${parsed[2]:-}"
missing_csv="${parsed[3]:-}"
has_label="${parsed[4]:-0}"

if [[ "$state" != "OPEN" ]]; then
  echo "issue #$issue_number is not open"
  exit 1
fi

if [[ "$has_label" != "1" ]]; then
  gh issue edit "$issue_number" --repo "$repo" --add-label ceo-plan >/dev/null
fi

if [[ -n "$missing_csv" ]]; then
  gh issue edit "$issue_number" --repo "$repo" --add-label needs-ceo-update >/dev/null || true
  gh issue edit "$issue_number" --repo "$repo" --remove-label ceo-approved --remove-label ready-for-pm-breakdown >/dev/null || true

  IFS="," read -r -a missing_items <<< "$missing_csv"
  msg="CEO intake failed for #${issue_number}. Missing sections:"
  for item in "${missing_items[@]}"; do
    msg+=$'\n- '
    msg+="$item"
  done
  msg+=$'\n\nPlease complete the CEO project plan template and rerun intake.'

  gh issue comment "$issue_number" --repo "$repo" --body "$msg" >/dev/null
  echo "$msg"
  exit 1
fi

gh issue edit "$issue_number" --repo "$repo" --add-label ceo-approved --add-label ready-for-pm-breakdown >/dev/null
gh issue edit "$issue_number" --repo "$repo" --remove-label needs-ceo-update >/dev/null || true

if [[ -n "${CEO_PROJECT_NAME:-}" ]]; then
  gh issue edit "$issue_number" --repo "$repo" --add-project "$CEO_PROJECT_NAME" >/dev/null || true
fi

gh issue comment "$issue_number" --repo "$repo" --body "CEO plan intake passed for #$issue_number: \"$title\". Marked as **ready-for-pm-breakdown**." >/dev/null

echo "CEO intake passed: $issue_url"

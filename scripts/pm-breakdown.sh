#!/usr/bin/env bash
set -euo pipefail

ceo_issue_number="${1:-}"
repo="${GITHUB_REPOSITORY:-}"
max_items="${PM_BREAKDOWN_MAX_ITEMS:-3}"
artifacts_dir="${ARTIFACTS_DIR:-artifacts/pm-breakdown}"

if [[ -z "$ceo_issue_number" ]]; then
  echo "usage: scripts/pm-breakdown.sh <ceo-issue-number>"
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

if ! [[ "$max_items" =~ ^[0-9]+$ ]]; then
  max_items="3"
fi
if (( max_items < 1 )); then max_items=1; fi
if (( max_items > 8 )); then max_items=8; fi

mkdir -p "$artifacts_dir"
tmp_root="$(mktemp -d)"
trap 'rm -rf "$tmp_root"' EXIT

ensure_label() {
  local name="$1"
  local color="$2"
  local desc="$3"
  gh label create "$name" --repo "$repo" --color "$color" --description "$desc" >/dev/null 2>&1 || true
}

ensure_label "pm-requirement" "1F6FEB" "PM requirement intake"
ensure_label "pm-breakdown-done" "0E8A16" "PM manager decomposition finished"
ensure_label "ready-for-pm-breakdown" "FBCA04" "Ready for PM manager decomposition"
ensure_label "needs-pm-review" "B60205" "PM review required"

ceo_json="$(gh issue view "$ceo_issue_number" --repo "$repo" --json title,body,state,url,labels,number)"

mapfile -t ceo_ctx < <(CEO_JSON="$ceo_json" python3 - <<"PY"
import json
import os

obj = json.loads(os.environ["CEO_JSON"])
labels = {x["name"] for x in obj.get("labels", [])}
print(obj.get("title", ""))
print(obj.get("state", ""))
print(obj.get("url", ""))
print("1" if "ready-for-pm-breakdown" in labels else "0")
print("1" if "pm-breakdown-done" in labels else "0")
print(obj.get("body") or "")
PY
)

ceo_title="${ceo_ctx[0]:-}"
ceo_state="${ceo_ctx[1]:-}"
ceo_url="${ceo_ctx[2]:-}"
ready_label="${ceo_ctx[3]:-0}"
already_done="${ceo_ctx[4]:-0}"
ceo_body="${ceo_ctx[5]:-}"

if [[ "$ceo_state" != "OPEN" ]]; then
  echo "CEO issue #$ceo_issue_number is not open"
  exit 1
fi

if [[ "$ready_label" != "1" ]]; then
  echo "CEO issue #$ceo_issue_number is not labeled ready-for-pm-breakdown"
  exit 1
fi

if [[ "$already_done" == "1" ]]; then
  echo "PM breakdown already completed for #$ceo_issue_number"
  exit 0
fi

ceo_body_file="$tmp_root/ceo-body.md"
printf "%s" "$ceo_body" > "$ceo_body_file"

existing_count="$(gh issue list --repo "$repo" --search "\"Parent Plan: #$ceo_issue_number\" in:body" --state open --json number --jq 'length')"
if [[ "$existing_count" != "0" ]]; then
  gh issue edit "$ceo_issue_number" --repo "$repo" --add-label pm-breakdown-done --remove-label ready-for-pm-breakdown >/dev/null || true
  gh issue comment "$ceo_issue_number" --repo "$repo" --body "PM breakdown skipped: found $existing_count existing child issues linked to this plan." >/dev/null || true
  echo "existing child issues found: $existing_count"
  exit 0
fi

generate_ai_json() {
  local out_json="$1"

  if [[ -z "${OPENAI_API_KEY:-}" ]]; then
    return 1
  fi

  local endpoint="${OPENAI_BASE_URL:-https://api.openai.com/v1}"
  endpoint="${endpoint%/}/responses"
  local model="${OPENAI_MODEL:-gpt-5-mini}"
  local prompt_file="$tmp_root/prompt.txt"
  local req_file="$tmp_root/req.json"
  local resp_file="$tmp_root/resp.json"

  {
    echo "Repository: $repo"
    echo "CEO Plan Issue: #$ceo_issue_number"
    echo "CEO Title: $ceo_title"
    echo "max_items: $max_items"
    echo
    echo "CEO plan body:"
    cat "$ceo_body_file"
    echo
    cat <<'RULES'
Return ONLY JSON array with 1..max_items objects.
Each object must contain keys:
- title
- business_context
- objective
- scope
- acceptance_criteria
- implementation_notes
- targeted_qa_command
- regression_command
Rules:
- title starts with "[PM]"
- acceptance_criteria should be bullet checklist text
- output JSON only, no markdown
RULES
  } > "$prompt_file"

  python3 - "$model" "$prompt_file" "$req_file" <<"PY"
import json
import sys

model = sys.argv[1]
prompt = open(sys.argv[2], "r", encoding="utf-8").read()
req = {
    "model": model,
    "input": prompt,
    "max_output_tokens": 5000,
}
with open(sys.argv[3], "w", encoding="utf-8") as f:
    json.dump(req, f)
PY

  local code
  code="$(curl -sS -o "$resp_file" -w "%{http_code}" "$endpoint" \
    -H "Authorization: Bearer $OPENAI_API_KEY" \
    -H "Content-Type: application/json" \
    -d @"$req_file")"

  if [[ "$code" -ge 400 ]]; then
    return 1
  fi

  python3 - "$resp_file" "$out_json" "$max_items" <<"PY"
import json
import re
import sys

resp_path, out_path, max_items = sys.argv[1], sys.argv[2], int(sys.argv[3])
obj = json.load(open(resp_path, "r", encoding="utf-8"))
texts = []

def walk(x):
    if isinstance(x, dict):
        for k, v in x.items():
            if k == "text" and isinstance(v, str):
                texts.append(v)
            else:
                walk(v)
    elif isinstance(x, list):
        for item in x:
            walk(item)

walk(obj)
raw = "\n".join(t for t in texts if t).strip()
if not raw and isinstance(obj, dict):
    raw = obj.get("output_text", "") or ""
raw = raw.strip()
if not raw:
    raise SystemExit(1)

m = re.search(r"```(?:json)?\n(.*?)\n```", raw, re.S | re.I)
if m:
    raw = m.group(1).strip()

data = json.loads(raw)
if not isinstance(data, list) or not data:
    raise SystemExit(1)

required = [
    "title",
    "business_context",
    "objective",
    "scope",
    "acceptance_criteria",
    "implementation_notes",
    "targeted_qa_command",
    "regression_command",
]
out = []
for item in data[:max_items]:
    if not isinstance(item, dict):
        continue
    clean = {}
    ok = True
    for k in required:
        v = item.get(k, "")
        if not isinstance(v, str):
            v = str(v)
        v = v.strip()
        if k in {"title", "business_context", "objective", "scope", "acceptance_criteria"} and not v:
            ok = False
            break
        clean[k] = v
    if ok:
        out.append(clean)

if not out:
    raise SystemExit(1)

with open(out_path, "w", encoding="utf-8") as f:
    json.dump(out, f, ensure_ascii=False)
PY
}

fallback_json() {
  local out_json="$1"
  python3 - "$ceo_title" "$ceo_body_file" "$out_json" "$max_items" <<"PY"
import json
import sys

ceo_title = sys.argv[1].strip()
body = open(sys.argv[2], "r", encoding="utf-8").read()
out_path = sys.argv[3]
max_items = int(sys.argv[4])

bullets = []
for line in body.splitlines():
    s = line.strip()
    if s.startswith("- ") or s.startswith("* "):
        bullets.append(s[2:].strip())

if not bullets:
    bullets = [
        "Foundation work and architecture alignment",
        "Core feature delivery and integration",
        "Validation, launch readiness, and operations handoff",
    ]

items = []
for idx in range(max_items):
    focus = bullets[idx] if idx < len(bullets) else f"Workstream {idx+1}"
    items.append(
        {
            "title": f"[PM] {ceo_title} - Requirement {idx+1}",
            "business_context": f"Derived from CEO plan focus: {focus}",
            "objective": f"Deliver measurable progress for: {focus}",
            "scope": f"In scope: {focus}\\nOut of scope: unrelated initiatives",
            "acceptance_criteria": "- [ ] Feature behavior matches requirement\\n- [ ] Tests updated and passing\\n- [ ] Docs/ops notes updated if needed",
            "implementation_notes": "Align with existing architecture and service boundaries.",
            "targeted_qa_command": "go test ./... -run '^$' -count=1",
            "regression_command": "go test ./... -count=1",
        }
    )

with open(out_path, "w", encoding="utf-8") as f:
    json.dump(items, f, ensure_ascii=False)
PY
}

req_json_file="$tmp_root/requirements.json"
source="fallback"
if generate_ai_json "$req_json_file"; then
  source="ai"
else
  fallback_json "$req_json_file"
fi

python3 - "$req_json_file" "$tmp_root/items" "$ceo_issue_number" <<"PY"
import json
import os
import sys

data = json.load(open(sys.argv[1], "r", encoding="utf-8"))
out_dir = sys.argv[2]
parent = sys.argv[3]
os.makedirs(out_dir, exist_ok=True)

for i, item in enumerate(data, 1):
    title = item["title"].strip()
    if not title.startswith("[PM]"):
        title = "[PM] " + title

    body = f"""### Business Context
{item['business_context']}

### Objective
{item['objective']}

### Scope
{item['scope']}

### Acceptance Criteria
{item['acceptance_criteria']}

### Implementation Notes (for Dev)
{item.get('implementation_notes', '')}

### Targeted QA Command (optional)
{item.get('targeted_qa_command', '')}

### Regression Command (optional)
{item.get('regression_command', '')}

### Parent Plan
Parent Plan: #{parent}
"""

    with open(os.path.join(out_dir, f"{i}.title"), "w", encoding="utf-8") as f:
        f.write(title)
    with open(os.path.join(out_dir, f"{i}.body"), "w", encoding="utf-8") as f:
        f.write(body)
PY

summary_file="$artifacts_dir/summary.md"
{
  echo "# PM Breakdown Report"
  echo
  echo "- ceo_issue: #$ceo_issue_number"
  echo "- ceo_title: $ceo_title"
  echo "- ceo_url: $ceo_url"
  echo "- generation_source: $source"
  echo "- max_items: $max_items"
  echo
  echo "## Created Requirement Issues"
} > "$summary_file"

created=0
while IFS= read -r title_file; do
  body_file="${title_file%.title}.body"
  title="$(cat "$title_file")"
  body="$(cat "$body_file")"

  issue_url="$(gh issue create --repo "$repo" --title "$title" --body "$body" --label pm-requirement)"
  issue_num="$(echo "$issue_url" | sed -E 's#.*/issues/([0-9]+)$#\1#')"

  {
    echo "- #$issue_num $title"
    echo "  - $issue_url"
  } >> "$summary_file"
  created=$((created + 1))
done < <(find "$tmp_root/items" -type f -name '*.title' | sort)

if (( created == 0 )); then
  gh issue edit "$ceo_issue_number" --repo "$repo" --add-label needs-pm-review >/dev/null || true
  gh issue comment "$ceo_issue_number" --repo "$repo" --body "PM breakdown produced no child requirement issues. Manual PM review required." >/dev/null || true
  exit 1
fi

gh issue edit "$ceo_issue_number" --repo "$repo" --add-label pm-breakdown-done --remove-label ready-for-pm-breakdown >/dev/null || true
gh issue comment "$ceo_issue_number" --repo "$repo" --body "PM manager breakdown complete: created $created requirement issues from this CEO plan." >/dev/null

echo "created_count=$created" > "$artifacts_dir/context.env"
echo "source=$source" >> "$artifacts_dir/context.env"
echo "ceo_issue=$ceo_issue_number" >> "$artifacts_dir/context.env"

cat "$artifacts_dir/context.env"

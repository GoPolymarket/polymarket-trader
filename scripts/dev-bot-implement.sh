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
tmp_root="$(mktemp -d)"
trap 'rm -rf "$tmp_root"' EXIT
workdir="$tmp_root/workitems"
mkdir -p "$workdir"

ensure_label() {
  local name="$1"
  local color="$2"
  local desc="$3"
  gh label create "$name" --repo "$repo" --color "$color" --description "$desc" >/dev/null 2>&1 || true
}

extract_issue_patch() {
  local source_file="$1"
  local out_patch="$2"
  python3 - "$source_file" "$out_patch" <<'PY'
import re
import sys

body = open(sys.argv[1], "r", encoding="utf-8").read()
match = re.search(r"```diff\n(.*?)\n```", body, re.S | re.I)
patch = match.group(1).strip() if match else ""
with open(sys.argv[2], "w", encoding="utf-8") as f:
    if patch:
        f.write(patch)
        if not patch.endswith("\n"):
            f.write("\n")
PY
}

generate_ai_patch() {
  local out_patch="$1"
  if [[ -z "${OPENAI_API_KEY:-}" ]]; then
    return 1
  fi

  local endpoint
  endpoint="${OPENAI_BASE_URL:-https://api.openai.com/v1}"
  endpoint="${endpoint%/}/responses"
  local model="${OPENAI_MODEL:-gpt-5-mini}"

  local file_list="$tmp_root/file-list.txt"
  local go_mod_excerpt="$tmp_root/go-mod.txt"
  local readme_excerpt="$tmp_root/readme.txt"
  local prompt_file="$tmp_root/prompt.txt"
  local req_file="$tmp_root/request.json"
  local resp_file="$tmp_root/response.json"

  git ls-files | sed -n '1,400p' > "$file_list"
  if [[ -f go.mod ]]; then
    sed -n '1,200p' go.mod > "$go_mod_excerpt"
  else
    : > "$go_mod_excerpt"
  fi
  if [[ -f README.md ]]; then
    sed -n '1,220p' README.md > "$readme_excerpt"
  else
    : > "$readme_excerpt"
  fi

  {
    echo "Repository: $repo"
    echo "Issue #$issue_number"
    echo "Issue URL: $issue_url"
    echo
    echo "Issue title:"
    echo "$issue_title"
    echo
    echo "Issue body:"
    cat "$issue_body_file"
    echo
    echo "Top tracked files:"
    cat "$file_list"
    echo
    echo "go.mod excerpt:"
    cat "$go_mod_excerpt"
    echo
    echo "README excerpt:"
    cat "$readme_excerpt"
    echo
    cat <<'RULES'
Task:
- Produce a minimal, correct code change for this issue.
- Return ONLY a unified diff patch (git apply format).
- Do NOT return markdown, explanations, or fenced code blocks.
- If no safe change can be made, return exactly NO_CHANGES.
RULES
  } > "$prompt_file"

  python3 - "$model" "$prompt_file" "$req_file" <<'PY'
import json
import sys

model = sys.argv[1]
prompt = open(sys.argv[2], "r", encoding="utf-8").read()
out = {
    "model": model,
    "input": prompt,
    "max_output_tokens": 5000,
}
with open(sys.argv[3], "w", encoding="utf-8") as f:
    json.dump(out, f)
PY

  local http_code
  http_code="$(curl -sS -o "$resp_file" -w "%{http_code}" "$endpoint" \
    -H "Authorization: Bearer $OPENAI_API_KEY" \
    -H "Content-Type: application/json" \
    -d @"$req_file")"

  if [[ "$http_code" -ge 400 ]]; then
    return 1
  fi

  python3 - "$resp_file" "$out_patch" <<'PY'
import json
import re
import sys

obj = json.load(open(sys.argv[1], "r", encoding="utf-8"))
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

if not raw:
    raw = obj.get("output_text", "") if isinstance(obj, dict) else ""

if not raw and isinstance(obj, dict) and "choices" in obj:
    try:
        content = obj["choices"][0]["message"]["content"]
        if isinstance(content, str):
            raw = content
        elif isinstance(content, list):
            chunks = []
            for item in content:
                if isinstance(item, dict):
                    chunks.append(item.get("text", ""))
                else:
                    chunks.append(str(item))
            raw = "\n".join(chunks)
    except Exception:
        raw = ""

raw = (raw or "").strip()
if not raw or raw.upper().startswith("NO_CHANGES"):
    open(sys.argv[2], "w", encoding="utf-8").write("")
    sys.exit(0)

m = re.search(r"```(?:diff)?\n(.*?)\n```", raw, re.S | re.I)
if m:
    raw = m.group(1).strip()

if "--- " in raw and "+++ " in raw:
    raw = raw[raw.find("--- "):]

if raw and not raw.endswith("\n"):
    raw += "\n"

open(sys.argv[2], "w", encoding="utf-8").write(raw)
PY

  [[ -s "$out_patch" ]]
}

ensure_label "dev-bot" "0E8A16" "Created by dev automation"
ensure_label "ready-for-qa" "FBCA04" "Ready for QA validation"
ensure_label "needs-dev-input" "B60205" "Issue needs more detail for dev automation"

issue_json="$(gh issue view "$issue_number" --repo "$repo" --json title,body,state,url,labels,number)"
issue_body_file="$tmp_root/issue-body.md"

issue_title="$(ISSUE_JSON="$issue_json" python3 - <<'PY'
import json
import os
print(json.loads(os.environ["ISSUE_JSON"]).get("title", ""))
PY
)"

issue_state="$(ISSUE_JSON="$issue_json" python3 - <<'PY'
import json
import os
print(json.loads(os.environ["ISSUE_JSON"]).get("state", ""))
PY
)"

issue_url="$(ISSUE_JSON="$issue_json" python3 - <<'PY'
import json
import os
print(json.loads(os.environ["ISSUE_JSON"]).get("url", ""))
PY
)"

ready_for_dev="$(ISSUE_JSON="$issue_json" python3 - <<'PY'
import json
import os
obj = json.loads(os.environ["ISSUE_JSON"])
labels = {x.get("name", "") for x in obj.get("labels", [])}
print("1" if "ready-for-dev" in labels else "0")
PY
)"

ISSUE_JSON="$issue_json" ISSUE_BODY_FILE="$issue_body_file" python3 - <<'PY'
import json
import os
obj = json.loads(os.environ["ISSUE_JSON"])
with open(os.environ["ISSUE_BODY_FILE"], "w", encoding="utf-8") as f:
    f.write(obj.get("body") or "")
PY

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

report_file="$workdir/developer-output.md"
compile_log="$workdir/compile.log"
patch_file="$tmp_root/generated.patch"
patch_source="none"

cat > "$report_file" <<EOF2
# Developer Bot Output

- issue: #${issue_number}
- source: ${issue_url}
- generated_at_utc: $(date -u +"%Y-%m-%dT%H:%M:%SZ")

## Requirement Snapshot

${issue_title}
EOF2

extract_issue_patch "$issue_body_file" "$patch_file"
if [[ -s "$patch_file" ]]; then
  patch_source="issue-diff"
fi

if [[ "$patch_source" == "none" ]]; then
  if generate_ai_patch "$patch_file"; then
    patch_source="ai-coder"
  fi
fi

applied_patch="false"
if [[ -s "$patch_file" ]]; then
  if git apply --whitespace=fix "$patch_file" >/dev/null 2>&1; then
    applied_patch="true"
  elif git apply --3way "$patch_file" >/dev/null 2>&1; then
    applied_patch="true"
  fi
fi

if [[ "$applied_patch" != "true" ]]; then
  {
    echo
    echo "## Implementation"
    echo "- result: failed"
    echo "- patch_source: ${patch_source}"
    echo "- reason: no applicable patch generated from issue diff or AI"
  } >> "$report_file"

  gh issue edit "$issue_number" --repo "$repo" --add-label needs-dev-input >/dev/null || true
  gh issue comment "$issue_number" --repo "$repo" --body "Dev bot could not generate a valid code patch for #$issue_number. Please refine implementation notes or provide an explicit diff patch." >/dev/null || true

  out_dir="$artifacts_dir/issue-${issue_number}"
  mkdir -p "$out_dir"
  cp "$report_file" "$out_dir/developer-output.md"
  if [[ -s "$patch_file" ]]; then cp "$patch_file" "$out_dir/generated.patch"; fi
  exit 1
fi

if ! go test ./... -run '^$' -count=1 >"$compile_log" 2>&1; then
  {
    echo
    echo "## Implementation"
    echo "- result: failed"
    echo "- patch_source: ${patch_source}"
    echo "- reason: compile gate failed"
    echo "- compile_log: $(basename "$compile_log")"
  } >> "$report_file"

  gh issue edit "$issue_number" --repo "$repo" --add-label needs-dev-input >/dev/null || true
  gh issue comment "$issue_number" --repo "$repo" --body "Dev bot generated code for #$issue_number but compile gate failed. Please review workflow artifacts for logs." >/dev/null || true

  out_dir="$artifacts_dir/issue-${issue_number}"
  mkdir -p "$out_dir"
  cp "$report_file" "$out_dir/developer-output.md"
  cp "$compile_log" "$out_dir/compile.log"
  cp "$patch_file" "$out_dir/generated.patch"
  exit 1
fi

git add -A
if git diff --cached --quiet; then
  gh issue edit "$issue_number" --repo "$repo" --add-label needs-dev-input >/dev/null || true
  gh issue comment "$issue_number" --repo "$repo" --body "Dev bot produced no tracked file changes for #$issue_number. Please refine requirements." >/dev/null || true
  exit 1
fi

git commit -m "feat(dev-bot): implement issue #${issue_number}" >/dev/null
git push --set-upstream origin "$branch" --force

pr_title="feat: ${issue_title} (#${issue_number})"
pr_body=$(cat <<EOF2
Automated developer delivery for issue #${issue_number}.

Closes #${issue_number}

## Dev Output
- Branch: \`${branch}\`
- Patch source: ${patch_source}
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

gh issue edit "$issue_number" --repo "$repo" --remove-label needs-dev-input >/dev/null || true
gh issue comment "$issue_number" --repo "$repo" --body "Dev bot created/updated PR #${pr_number} from \`${branch}\` (source: ${patch_source})." >/dev/null

{
  echo
  echo "## Implementation"
  echo "- result: success"
  echo "- patch_source: ${patch_source}"
  echo "- pr_number: #${pr_number}"
} >> "$report_file"

out_dir="$artifacts_dir/issue-${issue_number}"
mkdir -p "$out_dir"
cp "$report_file" "$out_dir/developer-output.md"
cp "$patch_file" "$out_dir/generated.patch"
cp "$compile_log" "$out_dir/compile.log"

echo "issue_number=${issue_number}" > "$artifacts_dir/context.env"
echo "branch=${branch}" >> "$artifacts_dir/context.env"
echo "pr_number=${pr_number}" >> "$artifacts_dir/context.env"
echo "issue_url=${issue_url}" >> "$artifacts_dir/context.env"
echo "patch_source=${patch_source}" >> "$artifacts_dir/context.env"

cat "$artifacts_dir/context.env"

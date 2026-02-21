#!/usr/bin/env bash
# Apply GitHub rulesets from JSON files.
# Usage: .github/rulesets/apply.sh
#
# Creates or updates rulesets to match the local JSON definitions.
# Requires: gh CLI (authenticated)

set -euo pipefail

REPO=$(gh repo view --json nameWithOwner -q .nameWithOwner)
DIR="$(cd "$(dirname "$0")" && pwd)"

for file in "$DIR"/*.json; do
  [ -f "$file" ] || continue
  name=$(jq -r .name "$file")

  # Check if ruleset already exists
  existing_id=$(gh api "repos/$REPO/rulesets" --jq ".[] | select(.name == \"$name\") | .id" 2>/dev/null || true)

  if [ -n "$existing_id" ]; then
    echo "Updating ruleset '$name' (id: $existing_id)..."
    gh api "repos/$REPO/rulesets/$existing_id" -X PUT --input "$file"
  else
    echo "Creating ruleset '$name'..."
    gh api "repos/$REPO/rulesets" -X POST --input "$file"
  fi

  echo "Done: $name"
done

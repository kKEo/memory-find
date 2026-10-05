#!/usr/bin/env bash
# docs-check: keep the user and operator guides (docs/guide/) honest.
#
#  1. Every relative link in either guide resolves to a file or directory
#     inside docs/guide/ (links leaving the guides would break on Pages; use
#     an absolute GitHub URL for anything else in the repository).
#  2. Every MEMO_* environment variable read by the code appears in the
#     operator guide's environment reference.
#  3. Every top-level command in `usage()` appears in the commands reference.
#  4. Every memo_* metric registered in internal/ appears in the metric
#     catalogue.
#
# Checks 2-4 are skipped with a warning while their reference page is still
# a draft stub (contains the line "> Draft."), so the site can be built and
# published while the operator guide is being written.
#
# Portable: bash, grep -E, sed, find; no GNU-only flags. Run from anywhere.
set -euo pipefail
cd "$(dirname "$0")/.."

GUIDE=docs/guide
OP=$GUIDE/operator
fail=0
err()  { echo "docs-check: $*" >&2; fail=1; }
warn() { echo "docs-check: warning: $*" >&2; }
is_draft() { grep -qx '> Draft\.' "$1"; }

# Non-test Go sources.
go_sources() { find internal cmd -name '*.go' ! -name '*_test.go' -print0; }

# 1. Relative links.
guide_root=$(cd "$GUIDE" && pwd -P)
broken=$(mktemp)
trap 'rm -f "$broken"' EXIT
while IFS= read -r -d '' md; do
  dir=$(dirname "$md")
  # Markdown links ](target), skipping URLs, mailto and same-page anchors.
  { grep -oE '\]\([^)[:space:]]+\)' "$md" || true; } | sed -E 's/^\]\(//; s/\)$//' |
  while IFS= read -r target; do
    case "$target" in http://*|https://*|mailto:*|\#*) continue ;; esac
    path=${target%%#*}
    [ -z "$path" ] && continue
    if [ ! -e "$dir/$path" ]; then
      echo "docs-check: broken link in $md: $target" >&2; echo x
      continue
    fi
    real=$(cd "$(dirname "$dir/$path")" && pwd -P)
    case "$real/" in "$guide_root"/*) ;; *) echo "docs-check: link leaves docs/guide in $md: $target (use a GitHub URL)" >&2; echo x ;; esac
  done
done < <(find "$GUIDE" -name '*.md' -print0) > "$broken"
[ -s "$broken" ] && fail=1

# 2. Environment variables.
env_ref=$OP/config/environment.md
if is_draft "$env_ref"; then
  warn "$env_ref is a draft; skipping the environment-variable check"
else
  for v in $(go_sources | xargs -0 grep -hoE '"MEMO_[A-Z_]+"' | tr -d '"' | sort -u); do
    grep -q "\`$v\`" "$env_ref" || err "$v is read by the code but missing from $env_ref"
  done
fi

# 3. Commands from usage().
cmd_ref=$OP/config/commands.md
if is_draft "$cmd_ref"; then
  warn "$cmd_ref is a draft; skipping the command check"
else
  cmds=$(sed -n '/^func usage/,/^}/p' internal/cli/cli.go | grep -oE '^  memo-mcp \[?[a-z][a-z-]*' | sed -E 's/^  memo-mcp \[?//' | sort -u)
  [ -n "$cmds" ] || err "could not extract commands from usage() in internal/cli/cli.go"
  for c in $cmds; do
    grep -qE "memo-mcp $c( |\`|$)" "$cmd_ref" || err "command '$c' is in usage() but missing from $cmd_ref"
  done
fi

# 4. Metric names.
cat_ref=$OP/monitoring/catalogue.md
if is_draft "$cat_ref"; then
  warn "$cat_ref is a draft; skipping the metric catalogue check"
else
  for m in $(go_sources | xargs -0 grep -hoE '"memo_(build|mcp|search|graph|store|kb|embed|ui)_[a-z0-9_]+"' | tr -d '"' | sort -u); do
    grep -q "\`$m\`" "$cat_ref" || err "metric $m is registered but missing from $cat_ref"
  done
fi

if [ "$fail" -ne 0 ]; then
  echo "docs-check: FAILED" >&2
  exit 1
fi
echo "docs-check: ok"

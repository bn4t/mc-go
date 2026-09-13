#!/usr/bin/env bash
set -euo pipefail

AUTH_FILE="${XDG_DATA_HOME:-$HOME/.local/share}/opencode/auth.json"

OPENCODE_GO_API_KEY="$(
  jq -r '."opencode-go".key // empty' "$AUTH_FILE"
)"

if [[ -z "$OPENCODE_GO_API_KEY" ]]; then
  echo "No OpenCode Go key found in $AUTH_FILE"
  echo "Check with: opencode auth list"
  exit 1
fi

export OPENCODE_GO_API_KEY
export OPENCODE_SESSION="${OPENCODE_SESSION:-$(uuidgen)}"

# Keep this launcher MCP-free for OpenCode Go schema compatibility without
# changing the user's normal Codex configuration.
# OpenCode Go calls DeepSeek V4.1 Flash `deepseek-flash`.
exec codex \
  -c 'mcp_servers={}' \
  -c 'features.plugins=false' \
  -c 'features.apps=false' \
  -c 'model_provider="opencode_go"' \
  -c 'model="deepseek-flash"' \
  -c 'model_reasoning_effort="max"' \
  -c 'model_providers.opencode_go={
    name="OpenCode Go",
    base_url="https://opencode.ai/zen/go/v1",
    env_key="OPENCODE_GO_API_KEY",
    wire_api="responses",
    requires_openai_auth=false,
    request_max_retries=20,
    stream_max_retries=20,
    env_http_headers={ "x-opencode-session"="OPENCODE_SESSION" }
  }' \
  --approve-for-me \
  "$@"

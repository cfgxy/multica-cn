#!/bin/bash
# minimal MCP streamable-http client for guxy-resource-pool
set -eu
URL="http://192.168.2.130:8772/mcp"
HDR=$(python3 -c "
import json
cfg = json.load(open('/home/guxy/.zcode/cli/config.json'))
h = cfg['mcp']['servers']['guxy-resource-pool'].get('headers', {})
for k, v in h.items(): print('-H', json.dumps(f'{k}: {v}'))")
SIDFILE=/home/guxy/multica_workspaces/ruyigongfan-018667473993/ruyi-669-50a21f7758da/worktree/.mcp_sid
call() {
  local method=$1 params=$2 sid=""
  [ -f "$SIDFILE" ] && sid=$(cat "$SIDFILE")
  local extra=()
  [ -n "$sid" ] && extra+=(-H "mcp-session-id: $sid")
  local resp
  resp=$(curl -sS -D /tmp/mcp_hdr -X POST "$URL" $HDR -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' "${extra[@]}" -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"$method\",\"params\":$params}")
  local newsid=$(grep -i '^mcp-session-id:' /tmp/mcp_hdr | tr -d '\r' | awk '{print $2}')
  [ -n "$newsid" ] && echo "$newsid" > "$SIDFILE"
  # strip SSE wrapper if present
  if [[ "$resp" == data:* ]] || [[ "$resp" == *"data: {"* ]]; then
    echo "$resp" | grep '^data:' | sed 's/^data: //' | tail -1
  else
    echo "$resp"
  fi
}
case "${1:-}" in
  init) call "initialize" '{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"guxy-agent","version":"1.0"}}' | head -c 300 ;;
  tools) call "tools/list" '{}' | python3 -c "import json,sys; d=json.load(sys.stdin); print(' '.join(t['name'] for t in d['result']['tools']))" ;;
  call) shift; TOOL=$1; ARGS=$2; call "tools/call" "{\"name\":\"$TOOL\",\"arguments\":$ARGS}" ;;
  *) echo "usage: mcp.sh init|tools|call TOOL ARGS_JSON" ;;
esac

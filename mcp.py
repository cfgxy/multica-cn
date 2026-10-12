#!/usr/bin/env python3
import json, sys, urllib.request, pathlib
URL = "http://192.168.2.130:8772/mcp"
CFG = json.load(open('/home/guxy/.zcode/cli/config.json'))
HEADERS = dict(CFG['mcp']['servers']['guxy-resource-pool'].get('headers', {}))
SID = pathlib.Path('/home/guxy/multica_workspaces/ruyigongfan-018667473993/ruyi-669-50a21f7758da/worktree/.mcp_sid')

def call(method, params, rid=1):
    headers = dict(HEADERS)
    headers['Content-Type'] = 'application/json'
    headers['Accept'] = 'application/json, text/event-stream'
    if SID.exists():
        headers['mcp-session-id'] = SID.read_text().strip()
    req = urllib.request.Request(URL, json.dumps({"jsonrpc": "2.0", "id": rid, "method": method, "params": params}).encode(), headers)
    with urllib.request.urlopen(req, timeout=120) as r:
        newsid = r.headers.get('mcp-session-id')
        if newsid:
            SID.write_text(newsid)
        body = r.read().decode()
    if body.startswith('event:') or '\ndata:' in body or body.startswith('data:'):
        for line in body.splitlines():
            if line.startswith('data:'):
                body = line[5:].strip()
                break
    return json.loads(body)

if __name__ == '__main__':
    cmd = sys.argv[1]
    if cmd == 'init':
        print(json.dumps(call('initialize', {"protocolVersion": "2024-11-05", "capabilities": {}, "clientInfo": {"name": "guxy-agent", "version": "1.0"}}))[:200])
        call('notifications/initialized', {})
    elif cmd == 'tools':
        d = call('tools/list', {})
        print(' '.join(t['name'] for t in d['result']['tools']))
    elif cmd == 'call':
        tool = sys.argv[2]
        args = json.loads(sys.argv[3]) if len(sys.argv) > 3 else {}
        d = call('tools/call', {"name": tool, "arguments": args}, rid=2)
        print(json.dumps(d, ensure_ascii=False))

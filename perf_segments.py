import sys, time, json
WORK = '/home/guxy/multica_workspaces/ruyigongfan-018667473993/ruyi-669-50a21f7758da/worktree'
sys.path.insert(0, WORK)
from mcp import call

SEGS = [("native",1),("native",2),("native",3),
        ("kern",1),("kern",2),("kern",3),
        ("kernxfs",1),("kernxfs",2),("kernxfs",3),
        ("userns",1),("userns",2),("userns",3)]
results = []
for i, (arm, rep) in enumerate(SEGS):
    first = "rm -f /home/agent/workspace/exp4/perf/results.csv; " if i == 0 else ""
    cmd = f'{first}cd /home/agent/workspace/exp4 && ARMS={arm} REPS_LIST={rep} bash run_perf.sh > seg_{arm}{rep}.log 2>&1; echo SEG_EXIT=$?'
    r = call('tools/call', {"name": "exec", "arguments": {"lease_id": "ls-000106", "command": cmd, "timeout_seconds": 600, "wait": False}})
    eid = json.loads(r['result']['content'][0]['text'])['execution_id']
    # poll
    deadline = time.time() + 560
    state = 'running'
    while time.time() < deadline:
        time.sleep(12)
        s = call('tools/call', {"name": "get_exec_status", "arguments": {"lease_id": "ls-000106", "execution_id": eid}})
        st = json.loads(s['result']['content'][0]['text'])
        state = st['state']
        if state != 'running':
            break
    results.append((arm, rep, eid, state))
    print(f"SEG {arm} rep{rep}: {state}", flush=True)
    if state != 'succeeded':
        print(f"SEG {arm} rep{rep} FAILED ({state}), stopping", flush=True)
        break
print("ALL_DONE", results, flush=True)

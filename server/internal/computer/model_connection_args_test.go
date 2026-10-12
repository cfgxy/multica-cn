package computer

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestModelConnectionPinsSavedNativeSelector(t *testing.T) {
	for _, kind := range []string{"claude", "codex", "pi", "omp", "opencode", "openclaw"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			executable := filepath.Join(home, "fake-cli")
			selector := "provider/model-a"
			if kind == "openclaw" {
				if err := os.Mkdir(filepath.Join(home, ".openclaw"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(home, ".openclaw/openclaw.json"), []byte(`{"models":{"providers":{"fixture":{}}},"agents":{"defaults":{"model":{"primary":"wrong"}}}}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			script := `#!/usr/bin/env python3
import json,os,sys
kind=` + pythonLiteral(kind) + `
if kind=='openclaw':
    assert sys.argv[sys.argv.index('--agent')+1]=='multica-config-test'
    with open(os.environ['OPENCLAW_CONFIG_PATH']) as f:
        config=json.load(f)
    assert config['agents']['list'][0]['model']=='provider/model-a'
    assert config['models']['providers']=={'fixture':{}}
    print(json.dumps({'payloads':[{'text':'MULTICA_CONNECTION_OK'}]}))
else:
    assert sys.argv[sys.argv.index('--model')+1]=='provider/model-a'
    if kind=='claude':
        print(json.dumps({'type':'result','is_error':False,'result':'MULTICA_CONNECTION_OK'}))
    elif kind=='codex':
        print(json.dumps({'type':'item.completed','item':{'type':'agent_message','text':'MULTICA_CONNECTION_OK'}}))
        print(json.dumps({'type':'turn.completed'}))
    elif kind=='opencode':
        print(json.dumps({'type':'text','part':{'text':'MULTICA_CONNECTION_OK'}}))
        print(json.dumps({'type':'step_finish'}))
    else:
        print(json.dumps({'type':'message_end','message':{'role':'assistant','content':[{'text':'MULTICA_CONNECTION_OK'}],'stopReason':'stop'}}))
`
			if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			if strings.Count(modelConnectionScript, "    main()\n") != 1 {
				t.Fatal("unsafe test injection")
			}
			injected := strings.Replace(modelConnectionScript, "    main()\n", "    main("+pythonLiteral(home)+", "+pythonLiteral(executable)+")\n", 1)
			command := exec.Command("python3", "-c", injected)
			request, _ := json.Marshal(map[string]string{"kind": kind, "model_selector": selector, "model": "model-a"})
			command.Stdin = strings.NewReader(string(request))
			raw, err := command.Output()
			if err != nil {
				t.Fatal(err)
			}
			var result ModelConnectionTest
			if json.Unmarshal(raw, &result) != nil || result.Status != "succeeded" {
				t.Fatalf("model selection assertion failed for %s", kind)
			}
		})
	}
}

func TestModelConnectionDrainsReplyWithoutWaitingForBackgroundChild(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "fake-codex")
	script := `#!/usr/bin/env python3
import json,subprocess,sys
subprocess.Popen([sys.executable,'-c','import time; time.sleep(20)'])
for _ in range(1000):
    print(json.dumps({'type':'progress','text':'fixture'}))
print(json.dumps({'type':'item.completed','item':{'type':'agent_message','text':'MULTICA_CONNECTION_OK'}}))
print(json.dumps({'type':'turn.completed'}))
`
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if strings.Count(modelConnectionScript, "    main()\n") != 1 {
		t.Fatal("unsafe test injection")
	}
	injected := strings.Replace(modelConnectionScript, "    main()\n", "    main("+pythonLiteral(root)+", "+pythonLiteral(executable)+")\n", 1)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "python3", "-c", injected)
	command.Stdin = strings.NewReader(`{"kind":"codex","model_selector":"model-a"}`)
	raw, err := command.Output()
	if err != nil {
		t.Fatalf("test waited for background child: %v", err)
	}
	var result ModelConnectionTest
	if json.Unmarshal(raw, &result) != nil || result.Status != "succeeded" {
		t.Fatal("exited before draining the buffered reply")
	}
}

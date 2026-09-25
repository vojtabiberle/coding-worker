package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/vojtabiberle/coding-worker/internal/testutil"
	"github.com/vojtabiberle/coding-worker/runner"
)

func TestPiRoutingCorrectionAndSavedProfile(t *testing.T) {
	a, root := setup(t)
	a.Engine = nil
	testutil.Write(t, a.ConfigPath, "default_profile='pi'\n[profiles.pi]\nengine='pi'\nmodel='fake/model'\nmax_steps=3\n")
	bin := t.TempDir()
	script := `#!/usr/bin/env python3
import sys,os,json,pathlib
if '--version' in sys.argv:
 print('0.78.1',file=sys.stderr);sys.exit(0)
prompt=sys.stdin.read()
state=pathlib.Path(os.environ['PI_CODING_AGENT_DIR']).parent
count=state/'calls'
n=int(count.read_text())+1 if count.exists() else 1
count.write_text(str(n))
session=state/'sessions'/'session.jsonl'
session.write_text('{"id":"session"}\n')
assert ('--session' in sys.argv)==(n>1)
report={'answer':'Inspected','findings':[]}
if n==1:
 report['findings']=[{'kind':'fact','text':'Read source','evidence':[{'file':'existing.txt','line':1,'quote':'WRONG QUOTE'}]}]
if n==2:
 assert 'failed validation' in prompt
for record in [{'type':'session','id':'session'},{'type':'worker_ready'},{'type':'agent_start'},{'type':'message_end','message':{'role':'assistant','stopReason':'stop','content':[{'type':'text','text':json.dumps(report)}]}},{'type':'agent_end'}]:
 print(json.dumps(record))
`
	executable := filepath.Join(bin, "pi")
	testutil.Write(t, executable, script)
	os.Chmod(executable, 0700)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	ctx := context.Background()
	v, e := a.Explore(ctx, ExploreRequest{CWD: root, Question: "Inspect"})
	if e != nil || v.State != "completed" {
		t.Fatal(v, e)
	}
	saved, e := a.Store.Get(v.RunID)
	if e != nil || saved.BackendVersion != runner.PiVersion || saved.Iterations[0].CitationAttempt == nil {
		t.Fatal(saved, e)
	}
	// Even after global settings change, continuation uses the original Pi profile.
	testutil.Write(t, a.ConfigPath, "default_profile='other'\n[profiles.other]\nengine='opencode'\nmodel='other/model'\nagent='build'\nmax_steps=4\n")
	next, e := a.Explore(ctx, ExploreRequest{RunID: v.RunID, Question: "Follow up"})
	if e != nil || next.State != "completed" {
		t.Fatal(next, e)
	}
	again, e := a.Store.Get(v.RunID)
	if e != nil || again.Session != saved.Session || again.Config.Profile.Engine != "pi" || len(again.Iterations) != 2 {
		t.Fatal(again, e)
	}
}

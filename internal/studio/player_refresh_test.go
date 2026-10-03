package studio

import (
	"os/exec"
	"strings"
	"testing"
)

func TestPlayerStructuralRefreshCoalescesBehindPoll(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	player, err := templates.ReadFile("templates/player.html")
	if err != nil {
		t.Fatal(err)
	}
	source := string(player)
	start := strings.Index(source, "  async function refreshPresentation(")
	end := strings.Index(source[start:], "  setInterval(")
	script := `
const assert=require('node:assert/strict');
let refreshingPresentation=false,structuralRefreshPending=false,selfMutations=0,autosaveInFlight=false,presentationETag='';
let dirty=false,drawerOpen=false,fetches=0,autosaves=0,release,outboxReads=0,unsent=false;
const slides=[{}],slideDirty=()=>dirty,scheduleAutosave=()=>autosaves++,interactionSurfaceOpen=()=>drawerOpen;
const frame={contains:()=>false},document={activeElement:{}},window={VSTD:{}},location={pathname:'/d/demo/'};
const outbox=async()=>{outboxReads++;return unsent?[{}]:[]};
const fetch=async()=>{fetches++;if(fetches===1)await new Promise(resolve=>release=resolve);return {status:304};};
` + source[start:start+end] + `
(async()=>{
 const first=refreshPresentation();
 while(!release)await Promise.resolve();
 drawerOpen=true;
 await refreshPresentation(true);
 assert.equal(fetches,1,'in-flight refresh should be coalesced');
 release();await first;
 for(let i=0;i<12;i++)await Promise.resolve();
 assert.equal(fetches,2,'saved move must fetch again after the earlier poll');
 await refreshPresentation();assert.equal(fetches,2,'ordinary polling must respect the companion drawer');
 dirty=true;await refreshPresentation(true);assert.equal(fetches,2);assert.equal(autosaves,1);
 dirty=false;await refreshPresentation();assert.equal(fetches,3,'saved move may refresh once dirty slides are saved');
 unsent=true;const reads=outboxReads;await refreshPresentation(true);
 for(let i=0;i<12;i++)await Promise.resolve();
 assert.equal(fetches,3);assert.equal(outboxReads,reads+1,'unsent edits must not cause a refresh spin loop');
})().catch(error=>{console.error(error);process.exitCode=1});
`
	if output, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", output, err)
	}
}

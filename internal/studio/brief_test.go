package studio

import (
	"os/exec"
	"testing"
)

func TestDeckBriefBackgroundSaveAndClear(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	source, err := templates.ReadFile("templates/brief.js")
	if err != nil {
		t.Fatal(err)
	}
	script := `global.window=global;` + string(source) + `
(async()=>{
 const assert=require('node:assert/strict');
 let server={generation:'first',revision:0,markdown:'',updated_at:''},finish,posts=0,resets=0;const requests=[];
 const controller=VSTDDeckBrief.create({deck:'demo',delay:100000,reset:()=>resets++,fetch:async(path,options={})=>{
   requests.push([path,options]);
   if(options.method==='DELETE'){server={generation:'second',revision:0,markdown:'',updated_at:''};return {ok:true,json:async()=>({...server})};}
   if(options.method==='POST'){posts++;return new Promise(r=>finish=()=>{server={...server,revision:1,markdown:'# Acme pilot'};r({ok:true,json:async()=>({...server})});});}
   return {ok:true,json:async()=>({...server})};
 }});
 await controller.refresh();controller.capture('Presenter','Prepare for Acme');
 const save=controller.flush();await new Promise(r=>setImmediate(r));assert.equal(posts,1);
 await controller.clear();finish();await save;
 assert.equal(controller.markdown(),'','an old in-flight save must not restore cleared context');assert.equal(resets,1);
 assert.equal(requests[1][1].keepalive,true);
 let generation='old';
 const other=VSTDDeckBrief.create({deck:'other',reset:()=>resets++,fetch:async()=>({ok:true,json:async()=>({generation,revision:0,markdown:'',updated_at:''})})});
 await other.refresh();generation='new';await other.refresh();assert.equal(resets,2,'another tab clear resets live context');
})().catch(error=>{console.error(error);process.exitCode=1;});`
	if output, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
}

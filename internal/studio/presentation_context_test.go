package studio

import (
	"os/exec"
	"testing"
)

func TestPresentationContextTracksSelectionAndCompanions(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	source, err := templates.ReadFile("templates/presentation-context.js")
	if err != nil {
		t.Fatal(err)
	}
	script := `global.window=global;` + string(source) + `
(async()=>{
 const assert=require('node:assert/strict');
 const slide=(id,title,flags=[])=>({id:'decorative-'+id,dataset:{vstd:id},innerText:title,querySelector:()=>({textContent:title}),hasAttribute:x=>flags.includes(x)});
 const a=slide('a','Baseline'),park=slide('park','Unused',['data-parked']),b=slide('b','Governance',['data-hidden']);
 let slides=[a,park,b],current=a;const pending=[];
 const ctx=VSTDPresentationContext.create({deck:'demo',current:()=>current,slides:()=>slides,fetch:(url)=>new Promise(resolve=>pending.push({url,resolve}))});
 const answer=(i,id,companion)=>pending[i].resolve({ok:true,json:async()=>({current:{id,companion},pages:[{id:'a',summary:'Workforce baseline'},{id:'b',summary:'Accountability and decision rights'}]})});
 const old=ctx.refresh();current=b;const fresh=ctx.refresh();
 assert.match(pending[1].url,/slide=b/);assert.equal(ctx.snapshot().current.page,2);assert.equal(ctx.snapshot().current.companion,null);
 answer(1,'b','## Talk track\nFull PRIVATE narrative '+ 'x'.repeat(20000));await fresh;
 answer(0,'a','STALE WRONG COMPANION');await old;
 let state=ctx.snapshot();assert.equal(state.current.id,'b');assert.equal(state.current.page,2);assert.equal(state.pages[1].page,0);assert.equal(state.pages[2].summary,'Accountability and decision rights');
 assert.match(state.current.companion,/Full PRIVATE narrative/);assert(!state.current.companion.includes('STALE'));
 const prompt=ctx.prompt(6000);assert(prompt.length<=6000);assert.match(prompt,/decision rights/);assert.match(prompt,/companion_truncated/);
 assert(new TextEncoder().encode(ctx.frontend()).length<500);assert.match(ctx.frontend(),/"page":2/);
 slides=[b,a,park];assert.equal(ctx.snapshot().current.page,1,'page numbering follows live player ordering');
 current=a;state=ctx.snapshot();assert.equal(state.current.companion,null,'never reuse another slide companion');
 const read=ctx.read();answer(2,'a','Fresh saved companion');state=await read;assert.equal(state.current.page,2);assert.equal(state.current.companion,'Fresh saved companion');
 const failing=ctx.refresh();pending[3].resolve({ok:false,status:503});await failing;
 assert.equal(ctx.snapshot().current.companion,null);assert.match(ctx.snapshot().error,/503/);
 const retry=ctx.read();answer(4,'a','Recovered companion');assert.equal((await retry).current.companion,'Recovered companion');
 // Very large decks must not produce an invalid Live offer or silently lose pages.
 slides=Array.from({length:1000},(_,i)=>slide('page'+i,'Long title '+i+'x'.repeat(160)));current=slides[0];
 const huge=ctx.prompt(6000);assert(huge.length<=6000);assert.match(huge,/toc_requires_tool/);
})().catch(error=>{console.error(error);process.exit(1);});`
	if output, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
}

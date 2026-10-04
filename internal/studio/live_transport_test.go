package studio

import (
	"os/exec"
	"testing"
)

func TestLiveTransportDelegationAndGracefulClose(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	source, err := templates.ReadFile("templates/live.js")
	if err != nil {
		t.Fatal(err)
	}
	script := `global.window=global;` + string(source) + `
 const assert=require('node:assert/strict');
 const sent=[];const dc={readyState:'open',send:x=>sent.push(JSON.parse(x))};
 const adapter=VSTDLive.adapter(dc);
 adapter.send({type:'session.update',session:{instructions:'current slide',tools:[]}});
 assert.equal(sent.length,0);
 adapter.event({type:'session.started'});
 assert.equal(sent[0].session.delegation.responses.instructions,'current slide');
 adapter.event({type:'response.event',event:{type:'response.created'}});
 const call={type:'response.event',event:{type:'response.output_item.done',item:{type:'function_call',name:'next_slide',call_id:'call_1',arguments:'{}'}}};
 assert.equal(adapter.event(call).type,'response.function_call_arguments.done');assert.equal(adapter.event(call),null);
 adapter.send({type:'conversation.item.create',item:{type:'function_call_output',call_id:'call_1',output:'advanced'}});
 assert.equal(sent.at(-1).type,'response.item.create');
 adapter.send({type:'response.create'});assert.notEqual(sent.at(-1).type,'response.create');
 adapter.event({type:'response.event',event:{type:'response.completed',response:{}}});assert.equal(sent.at(-1).type,'response.create');
 let finalized=false;window.__vendRealtimeSession=async()=>{finalized=true;};
 const closing=adapter.close();assert.equal(sent.at(-1).type,'session.close');assert.equal(finalized,false);
 adapter.event({type:'session.closed'});closing.then(closed=>{assert.equal(closed,true);assert.equal(finalized,true);});
 `
	if output, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
}

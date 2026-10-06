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
 adapter.send({type:'response.create'});assert.equal(sent.at(-1).type,'response.create','tool results must continue immediately without waiting for response.completed');
 const count=sent.length;adapter.event({type:'response.event',event:{type:'response.completed',response:{}}});assert.equal(sent.length,count,'completion must not launch a queued duplicate');
 let finalized=false;window.__vendRealtimeSession=async()=>{finalized=true;};
 const closing=adapter.close();assert.equal(sent.at(-1).type,'session.close');assert.equal(finalized,false);
 adapter.event({type:'session.closed'});closing.then(closed=>{assert.equal(closed,true);assert.equal(finalized,true);});
 `
	if output, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
}

func TestLiveTransportAuthenticatedRelay(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	source, err := templates.ReadFile("templates/live.js")
	if err != nil {
		t.Fatal(err)
	}
	script := `global.window=global;` + string(source) + `
(async()=>{
 const assert=require('node:assert/strict');
 const requests=[],sent=[];let delivered=false,finishContext;
 const dc=new EventTarget();dc.readyState='open';dc.send=x=>sent.push(JSON.parse(x));
 global.fetch=async(path,options)=>{
   requests.push([path,options]);
   if(path==='/api/live/context')await new Promise(resolve=>finishContext=resolve);
   return {ok:true,json:async()=>({events:delivered?[]:(delivered=true,[{type:'response.event',event:{type:'response.output_item.done',item:{type:'function_call',name:'next_slide',call_id:'call_relay',arguments:'{}'}}}])})};
 };
 const adapter=VSTDLive.adapter(dc,true);let calls=0;
 dc.addEventListener('message',event=>{const translated=adapter.event(JSON.parse(event.data));if(translated?.type==='response.function_call_arguments.done')calls++;});
 adapter.event({type:'session.started'});
 adapter.send({type:'session.update',session:{instructions:'current slide',tools:[]}});
 await new Promise(r=>setTimeout(r,30));
 assert.equal(calls,1);assert(requests.some(([path])=>path==='/api/live/context'));
 assert(!sent.some(event=>event.type==='session.update'));
 adapter.event({type:'response.event',event:{type:'response.created'}});
 adapter.send({type:'conversation.item.create',item:{type:'function_call_output',call_id:'call_relay',output:'advanced'}});
 adapter.send({type:'response.create'});
 assert.deepEqual(sent.slice(-2).map(event=>event.type),['response.item.create','response.create'],'pending slide context must not block a completed action result');
 finishContext();
 adapter.event({type:'session.closed'});await adapter.close();
})().catch(err=>{console.error(err);process.exit(1);});`
	if output, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
}

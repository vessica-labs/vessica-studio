package studio

import (
	"os/exec"
	"testing"
)

func TestHostedApplicationBridgeChecksOriginAndActiveFrame(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	source, err := templates.ReadFile("templates/hosted-apps.js")
	if err != nil {
		t.Fatal(err)
	}
	script := `const assert=require('node:assert/strict');global.window=global;const events={},sent=[];let currentFrame;
 global.document={hidden:false,addEventListener:(n,f)=>events[n]=f,dispatchEvent:()=>{}};
 global.addEventListener=(n,f)=>events[n]=f;global.setInterval=()=>{};global.location={href:'https://session.editor.test/'};
 global.VSTDPresenterControl=()=>true;global.fetch=async()=>({ok:true,json:async()=>({appId:'demo',origin:'https://demo.test',expiresAt:Date.now()+300000,token:'private-capability'})});
 const frame={dataset:{vstdHostedApp:'demo'},title:'Demo',getAttribute:()=> 'https://demo.test/',contentWindow:{postMessage:(m,o)=>sent.push({m,o})}};
 currentFrame=frame;global.VSTDP={slideEl:()=>({querySelector:()=>currentFrame})};
 ` + string(source) + `
 (async()=>{
  events.message({source:frame.contentWindow,origin:'https://evil.test',data:{type:'vstd:hosted-app:ready',appId:'demo'}});assert.equal(sent.length,0);
  events.message({source:frame.contentWindow,origin:'https://demo.test',data:{type:'vstd:hosted-app:ready',appId:'demo'}});await new Promise(setImmediate);
  assert.equal(sent[0].o,'https://demo.test');assert.equal(sent[0].m.token,'private-capability');const channel=sent[0].m.channel;
  const message={type:'vstd:hosted-app:state',appId:'demo',channel,state:{view:{share:25}}};
  events.message({source:{},origin:'https://demo.test',data:message});assert.equal(VSTDHostedApps.context().state,null);
  events.message({source:frame.contentWindow,origin:'https://demo.test',data:{...message,channel:'wrong'}});assert.equal(VSTDHostedApps.context().state,null);
  events.message({source:frame.contentWindow,origin:'https://demo.test',data:message});assert.equal(VSTDHostedApps.context().state.view.share,25);assert(!JSON.stringify(VSTDHostedApps.context()).includes('private-capability'));
  const result=VSTDHostedApps.run({action:'status'});await new Promise(setImmediate);const command=sent.at(-1).m;
  events.message({source:frame.contentWindow,origin:'https://demo.test',data:{type:'vstd:hosted-app:result',appId:'demo',channel,id:command.id,ok:true,result:{confirmed:true}}});assert.deepEqual(await result,{confirmed:true});
  const pending=VSTDHostedApps.run({action:'status'});await new Promise(setImmediate);currentFrame=null;events['vstd:slide']();await assert.rejects(pending,/slide changed/);assert.equal(sent.at(-1).m.token,undefined);
  await assert.rejects(VSTDHostedApps.run({action:'run'}),/No hosted application/);
 })().catch(e=>{console.error(e);process.exit(1)});`
	if output, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
}

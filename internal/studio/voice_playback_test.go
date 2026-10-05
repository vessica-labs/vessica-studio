package studio

import (
	"os/exec"
	"strings"
	"testing"
)

func TestVoicePlaybackWakeAndRecovery(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	source, err := templates.ReadFile("templates/voice.js")
	if err != nil {
		t.Fatal(err)
	}
	script := `global.window=global;` + string(source) + `
(async()=>{
 const assert=require('node:assert/strict');
 let rejectPlayback=false,plays=0,pauses=0;const states=[],conversations=[];
 const audio={muted:false,paused:true,srcObject:null,play(){plays++;if(rejectPlayback)return Promise.reject(new Error('NotAllowedError'));this.paused=false;return Promise.resolve();},pause(){pauses++;this.paused=true;}};
 const voice=VSTDVoice.create(audio,{state:(...s)=>states.push(s),conversation:t=>conversations.push(t)});
 const flush=async()=>{await Promise.resolve();await Promise.resolve();};
 assert.equal(audio.muted,true);assert.equal(audio.autoplay,true);
 voice.attach({id:'remote'});await flush();assert.equal(audio.srcObject.id,'remote');assert.equal(audio.muted,true);
 voice.input('Continue the presentation.');assert.equal(voice.talking(),false);
 voice.input(' Vessication');assert.equal(voice.talking(),false);
 voice.input(' Ves');voice.input('sica, help me.');await flush();
 assert.equal(voice.talking(),true);assert.equal(audio.muted,false);assert.equal(conversations.at(-1),true);
 assert(!states.some(s=>s[0]==='speaking'),'wake word must not claim speech playback');
 voice.input('Stop the simulation.');assert.equal(voice.talking(),true);
 voice.input(' Be qui');voice.input('et.');assert.equal(voice.talking(),false);assert.equal(audio.muted,true);
 voice.input('Continue.');assert.equal(voice.talking(),false,'old wake word must not reopen conversation');
 voice.input(' Vessica, that is all.');assert.equal(voice.talking(),false);
 rejectPlayback=true;voice.input(' Vessica, answer me.');await flush();
 assert.equal(voice.talking(),true);assert.equal(voice.blocked(),true);assert.match(states.at(-1)[1],/click.*sound/i);
 rejectPlayback=false;voice.retry();await flush();assert.equal(voice.blocked(),false);assert.equal(audio.paused,false);
 assert.equal(states.at(-1)[0],'listening');
 voice.end();assert.equal(audio.muted,true);assert.equal(voice.talking(),false);
 let settle;audio.play=()=>new Promise(resolve=>settle=resolve);
 voice.begin();voice.end();settle();await flush();assert.equal(audio.muted,true);assert.equal(voice.talking(),false);
 voice.close();assert.equal(audio.srcObject,null);assert.equal(audio.paused,true);assert.equal(pauses,1);
 const n=plays;voice.input('Vessica');voice.retry();assert.equal(plays,n);assert.equal(audio.muted,true);
})().catch(err=>{console.error(err);process.exit(1);});`
	if output, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
}

func TestPlayerLiveVoiceWithoutBackendWakeTool(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	player, err := templates.ReadFile("templates/player.html")
	if err != nil {
		t.Fatal(err)
	}
	source := string(player)
	start := strings.Index(source, "/* ================= Vessica realtime layer")
	end := strings.Index(source, "/* ============ Vessica demo tools client")
	if start < 0 || end <= start {
		t.Fatal("voice layer missing")
	}
	voice, err := templates.ReadFile("templates/voice.js")
	if err != nil {
		t.Fatal(err)
	}
	live, err := templates.ReadFile("templates/live.js")
	if err != nil {
		t.Fatal(err)
	}
	script := `
const assert=require('node:assert/strict');global.window=global;
global.location={protocol:'https:'};const listeners={},clicks={},audio=[],pcs=[],sent=[],toasts=[];let presenter=true;
const label={textContent:''},status={style:{},className:'',classList:{add(){}},addEventListener:(name,fn)=>clicks[name]=fn};
const slide={querySelector:()=>({textContent:'Demo'}),querySelectorAll:()=>[],hasAttribute:()=>false};
global.document={getElementById:id=>id==='vstatus'?status:label,addEventListener:(name,fn)=>listeners[name]=fn,querySelectorAll:()=>[slide]};
global.VSTD={deck:'demo',title:'Demo'};global.VSTDPresenterControl=()=>presenter;
global.VSTDP={slideEl:()=>slide,cur:()=>0,count:1,toast:t=>toasts.push(t)};
Object.defineProperty(global,'navigator',{value:{mediaDevices:{getUserMedia:async()=>({getTracks:()=>[{stop(){}}]})}},configurable:true});
let blocked=false;
global.Audio=class {constructor(){audio.push(this);}play(){return blocked?Promise.reject(new Error('NotAllowedError')):Promise.resolve();}pause(){this.paused=true;}};
global.RTCPeerConnection=class {constructor(){pcs.push(this);this.iceGatheringState='complete';}addTrack(){}createDataChannel(){return this.dc=new EventTarget();}async createOffer(){return {sdp:'offer'};}async setLocalDescription(offer){this.localDescription=offer;}async setRemoteDescription(){this.ontrack({streams:[{id:'remote'}]});}close(){}};
let liveMode=true;
global.fetch=async(path)=>({ok:true,text:async()=>'answer',json:async()=>path==='/api/realtime/token'?(liveMode?{protocol:'live'}:{value:'test-secret'}):path==='/api/live/session'?{session:{id:'live_1'},transport:{sdp:'answer'}}:{events:[]}});
` + string(voice) + string(live) + source[start:end] + `
(async()=>{
 const tick=()=>new Promise(resolve=>setImmediate(resolve));
 const emit=m=>pcs.at(-1).dc.onmessage({data:JSON.stringify(m)});
 listeners['vstd:conversation-toggle']();await tick();assert.equal(pcs.length,0,'conversation shortcut must not start a microphone session');assert.match(toasts.at(-1),/press V/i);
 listeners['vstd:vtoggle']();await tick();const first=audio.at(-1),dc=pcs.at(-1).dc;
 dc.readyState='open';dc.send=s=>sent.push(JSON.parse(s));dc.onopen();emit({type:'session.started'});
 assert.equal(first.muted,true);
 listeners['vstd:conversation-toggle']();await tick();assert.equal(first.muted,false);assert.equal(global.__vlive(),true);
 await global.__vpres.run('end_conversation',{});assert.equal(first.muted,false,'late sleep tool cannot undo button wake');
 assert(sent.some(m=>m.type==='session.thinking.append'&&m.content.includes('[PRESENTER VOICE CONTROL] wake')),'Live must receive explicit wake intent');
 emit({type:'session.input_transcript.delta',delta:"That's all."});assert.equal(first.muted,true,'spoken sleep after button wake');
 listeners['vstd:conversation-toggle']();assert.equal(first.muted,false);
 listeners['vstd:conversation-toggle']();assert.equal(first.muted,true);
 await global.__vpres.run('begin_conversation',{});assert.equal(first.muted,true,'late wake tool cannot undo button sleep');
 assert(sent.some(m=>m.type==='session.thinking.append'&&m.content.includes('[PRESENTER VOICE CONTROL] sleep')));
 presenter=false;listeners['vstd:conversation-toggle']();assert.equal(first.muted,true);presenter=true;
 emit({type:'session.output_transcript.delta',delta:'Vessica is speaking.'});assert.equal(first.muted,true,'model output cannot open speaker');
 blocked=true;emit({type:'session.input_transcript.delta',delta:'Ves'});emit({type:'session.input_transcript.delta',delta:'sica, hello.'});await tick();
 assert.equal(first.muted,false);assert.match(label.textContent,/audio blocked/);assert(!label.textContent.includes('speaking'));
 blocked=false;clicks.click();await tick();assert(global.__vlive(),'audio recovery must not stop the voice session');assert.match(label.textContent,/in conversation/);
 listeners['vstd:conversation-toggle']();assert.equal(first.muted,true,'button sleep after spoken wake');
 emit({type:'session.input_transcript.delta',delta:'Vessica'});assert.equal(first.muted,false,'spoken wake after button sleep');
 emit({type:'session.input_transcript.delta',delta:'Stop the simulation.'});assert.equal(first.muted,false);
 emit({type:'session.input_transcript.delta',delta:'Be quiet.'});assert.equal(first.muted,true);
 global.__vendRealtimeSession=async()=>{};emit({type:'session.closed'});await tick();assert.equal(first.paused,true);assert.equal(first.srcObject,null);
 assert.equal(global.__vlive(),false);
 // Local Realtime retains its tool-controlled wake path and a fresh muted speaker.
 liveMode=false;
 listeners['vstd:vtoggle']();await tick();const second=audio.at(-1),dc2=pcs.at(-1).dc;
 dc2.readyState='open';dc2.send=s=>sent.push(JSON.parse(s));dc2.onopen();
 assert.notEqual(second,first);assert.equal(second.muted,true);
 listeners['vstd:conversation-toggle']();assert.equal(second.muted,false);assert(sent.some(m=>m.type==='conversation.item.create'&&m.item.type==='message'&&m.item.content[0].text.includes('[PRESENTER VOICE CONTROL] wake')));
 listeners['vstd:conversation-toggle']();assert.equal(second.muted,true);
 emit({type:'session.input_transcript.delta',delta:'Vessica'});assert.equal(second.muted,true);
 await global.__vpres.run('begin_conversation',{});await tick();assert.equal(second.muted,false);
 await global.__vpres.run('end_conversation',{});assert.equal(second.muted,true);
 global.__vstopVessica();await tick();assert.equal(second.paused,true);
})().catch(err=>{console.error(err);process.exit(1);});`
	if output, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
}

// Exercise the actual player handler, including its authorization and text-entry guards.
func TestPlayerVoiceKeyboardShortcuts(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	player, err := templates.ReadFile("templates/player.html")
	if err != nil {
		t.Fatal(err)
	}
	source := string(player)
	start := strings.Index(source, "  function keyTargetIsTextEntry(e)")
	if start < 0 {
		t.Fatal("player keyboard handler missing")
	}
	end := strings.Index(source[start:], "  // ---------- Edit mode ----------")
	if end < 0 {
		t.Fatal("player keyboard handler missing")
	}
	script := `
 const assert=require('node:assert/strict');global.window=global;
 let keyboard,presenter=true,editingText=false,editMode=false,selEl=null;
 const events=[];global.document={activeElement:null,dispatchEvent:e=>events.push(e.type)};
 global.addEventListener=(name,fn)=>keyboard=fn;global.VSTDPresenterControl=()=>presenter;
 ` + source[start:start+end] + `
 const key=(key,extra={})=>keyboard({key,preventDefault(){},...extra});
 key('v');key('V');assert.deepEqual(events.splice(0),['vstd:vtoggle','vstd:vtoggle'],'Caps Lock preserves session toggle');
 key('V',{shiftKey:true});assert.deepEqual(events.splice(0),['vstd:conversation-toggle']);
 key('v',{shiftKey:true});assert.deepEqual(events.splice(0),['vstd:conversation-toggle']);
 for(const modifier of ['ctrlKey','metaKey','altKey','repeat']){key('V',{shiftKey:true,[modifier]:true});key('v',{[modifier]:true});}
 assert.equal(events.length,0,'modified shortcuts and key repeat must not toggle voice');
 const entry={nodeType:1,matches:()=>true};key('V',{shiftKey:true,target:entry});
 document.activeElement=entry;key('V',{shiftKey:true});document.activeElement=null;
 presenter=false;key('V',{shiftKey:true});presenter=true;
 window.__vaudience=true;key('V',{shiftKey:true});window.__vaudience=false;
 editingText=true;key('V',{shiftKey:true});assert.equal(events.length,0,'text entry and audience cannot toggle voice');
 `
	if output, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
}

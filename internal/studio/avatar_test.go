package studio

import (
	"os/exec"
	"testing"
)

func TestAvatarWakeSleepAndFallback(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	source, err := templates.ReadFile("templates/avatar.js")
	if err != nil {
		t.Fatal(err)
	}
	script := `global.window=global;global.crypto=require('node:crypto').webcrypto;` + string(source) + `
(async()=>{
 const assert=require('node:assert/strict'),tick=()=>new Promise(r=>setImmediate(r));
 const actions=[],routes=[],sockets=[],rooms=[],surfaces=[];let onFrame,stops=0,captureStops=0,failures=0,resolveStart;
 class Socket {constructor(){sockets.push(this);this.readyState=1;this.bufferedAmount=0;this.sent=[];}send(x){this.sent.push(JSON.parse(x));}close(){this.closed=true;}}
 class Room {constructor(){rooms.push(this);this.events={};this.canPlaybackAudio=true;}on(name,fn){this.events[name]=fn;}async connect(){}async startAudio(){}disconnect(){this.closed=true;}}
 const sdk={Room,RoomEvent:{TrackSubscribed:'track',Disconnected:'disconnect',AudioPlaybackStatusChanged:'audio'},Track:{Kind:{Video:'video',Audio:'audio'}}};
 const make=()=>VSTDAvatar.create({route:x=>routes.push(x),unavailable:()=>failures++},{
  load:async()=>sdk,WebSocket:Socket,
  capture:async(_,fn)=>{onFrame=fn;return()=>captureStops++;},
  surface:()=>{const s={video:{play:async()=>{}},audio:{append(){}},show(x){this.visible=x;},close(){this.closed=true;}};surfaces.push(s);return s;},
  request:async(action,id)=>{actions.push([action,id]);if(action==='stop'){stops++;return {};}
   if(action==='start')return new Promise(r=>resolveStart=()=>r({ws_url:'wss://media',livekit_url:'wss://room',livekit_client_token:'scoped'}));return {};}
 });
 const avatar=make();avatar.attach({});await tick();assert.equal(actions.length,0,'listening must not allocate paid sessions');
 avatar.begin();avatar.begin();await tick();assert.equal(actions.filter(x=>x[0]==='start').length,1,'duplicate wake must not allocate twice');
 avatar.end();await tick();assert.equal(stops,1);assert.equal(captureStops,1);
 resolveStart();await tick();assert.equal(sockets.length,0,'late startup must never revive sleep');assert.equal(stops,2,'late provider allocation is stopped again');
 avatar.begin();await tick();resolveStart();await tick();
 const s=sockets.at(-1),room=rooms.at(-1);
 s.onmessage({data:JSON.stringify({type:'session.state_updated',state:'connected'})});
 const a={};room.events.track({kind:'video',attach(){}});room.events.track({kind:'audio',attach:()=>a});
 const loud=new Int16Array(480).fill(5000),quiet=new Int16Array(480);
 onFrame(loud);assert(!routes.includes(true),'do not switch midway through direct voice');assert.equal(s.sent.length,0);
 for(let i=0;i<15;i++)onFrame(quiet);
 assert.equal(routes.at(-1),true);assert.equal(a.muted,false);assert.equal(surfaces.at(-1).visible,true);
 assert.equal(s.sent.length,0,'idle silence must not open an utterance');
 for(let i=0;i<30;i++)onFrame(loud);
 assert.equal(s.sent.at(-1).type,'agent.speak');assert.equal(Buffer.from(s.sent.at(-1).audio,'base64').length,28800);
 const utterance=s.sent.at(-1).event_id;assert(utterance);
 for(let i=0;i<50;i++)onFrame(loud);
 assert.equal(Buffer.from(s.sent.at(-1).audio,'base64').length,48000);assert.equal(s.sent.at(-1).event_id,utterance);
 onFrame(loud);for(let i=0;i<15;i++)onFrame(quiet);
 assert.equal(s.sent.at(-1).type,'agent.speak_end');assert.equal(s.sent.at(-1).event_id,utterance);
 assert.equal(s.sent.at(-2).event_id,utterance,'trailing audio belongs to the same utterance');
 const idle=s.sent.length;for(let i=0;i<20;i++)onFrame(quiet);assert.equal(s.sent.length,idle);
 for(let i=0;i<30;i++)onFrame(loud);assert.notEqual(s.sent.at(-1).event_id,utterance);
 onFrame(loud);avatar.interrupt();assert.equal(s.sent.at(-1).type,'agent.interrupt');
 const interrupted=s.sent.length;for(let i=0;i<40;i++)onFrame(loud);assert.equal(s.sent.length,interrupted,'discard old response after interruption');
 for(let i=0;i<15;i++)onFrame(quiet);for(let i=0;i<30;i++)onFrame(loud);assert.equal(s.sent.at(-1).type,'agent.speak','resume after an audio pause');
 room.events.disconnect();await tick();assert.equal(routes.at(-1),false);assert.equal(failures,1);assert.equal(room.closed,true);assert.equal(s.closed,true);assert.equal(surfaces.at(-1).closed,true);
 const sent=s.sent.length;onFrame(loud);assert.equal(s.sent.length,sent,'no hidden audio after session disposal');
 avatar.end();avatar.close();
 const before=actions.filter(x=>x[0]==='start').length;let failed=false;
 const unavailable=VSTDAvatar.create({route(){},unavailable(){failed=true;}},{load:async()=>{throw new Error('offline');},request:async()=>({})});
 unavailable.attach({});unavailable.begin();await tick();assert.equal(failed,true);assert.equal(actions.filter(x=>x[0]==='start').length,before,'failed SDK cannot allocate paid session');unavailable.close();
 const pixels=new Uint8ClampedArray([0,255,0,255,190,140,110,255,0,0,0,255]);VSTDAvatar.keyPixels(pixels);
 assert.equal(pixels[3],0);assert.equal(pixels[7],255);assert.equal(pixels[11],255);
})().catch(e=>{console.error(e);process.exit(1);});`
	if output, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
}

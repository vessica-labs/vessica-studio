package studio

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/chromium"
)

func TestSimulationControlBridge(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	player, err := templates.ReadFile("templates/player.html")
	if err != nil {
		t.Fatal(err)
	}
	source := string(player)
	start := strings.Index(source, "/* ================= Simulation control bridge")
	if start < 0 {
		t.Fatal("simulation bridge missing")
	}
	end := strings.Index(source[start:], "/* ================= End simulation control bridge")
	if end < 0 {
		t.Fatal("simulation bridge end missing")
	}
	script := `
const assert=require('node:assert/strict');
const listeners={},ticks=new Map();let timer=0,presenter=true,active=true,framePresent=true,now=1000;
Date.now=()=>now;
const attrs={'data-vstd-simulation':JSON.stringify({name:'Paro',checkpoints:['Dzong','Touchdown'],speeds:[0.5,1,2,4,8],cameras:['director','cockpit']}),'data-vstd-simulation-src':'/site/paro/','data-vstd-simulation-hosts':'localhost,127.0.0.1',srcdoc:'launcher'};
const commands=[];
const frame={getAttribute:k=>attrs[k]||null,hasAttribute:k=>k in attrs,setAttribute:(k,v)=>attrs[k]=v,removeAttribute:k=>delete attrs[k],contentWindow:{postMessage:(data,origin)=>commands.push({data,origin})}};
const slide={querySelector:()=>framePresent?frame:null,classList:{contains:k=>k==='active'&&active}};
global.window={location:{href:'http://localhost:4400/d/demo/',hostname:'localhost'},VSTDPresenterControl:()=>presenter,VSTDP:{slideEl:()=>active?slide:{}}};
global.document={hidden:false};global.addEventListener=(k,fn)=>listeners[k]=fn;
global.setInterval=(fn)=>{ticks.set(++timer,fn);return timer};global.clearInterval=id=>ticks.delete(id);
` + source[start:start+end] + `
(async()=>{
 const run=window.VSTDSimulation.run;
 const reply=(request,extra={},source=frame.contentWindow,origin='http://localhost:4400')=>listeners.message({source,origin,data:{type:'vstd:simulation:result',id:request.id,ok:true,state:{playing:true,speed:2},...extra}});
 assert.equal(window.VSTDSimulation.context().name,'Paro');
 presenter=false;await assert.rejects(run({action:'start'}),/presenter/);presenter=true;
 framePresent=false;await assert.rejects(run({action:'start'}),/No controllable/);framePresent=true;
 await assert.rejects(run({action:'checkpoint',checkpoint:'made up'}),/checkpoint/);
 await assert.rejects(run({action:'speed',speed:3}),/speed/);
 await assert.rejects(run({action:'camera',camera:'unknown'}),/camera/);
 await assert.rejects(run({action:'eval',code:'evil()'}),/action/);
 assert.equal(commands.length,0);
 assert.equal((await run({action:'status'})).ready,false);
 await assert.rejects(run({action:'stop'}),/Start/);
 window.location.hostname='cloud.example';await assert.rejects(run({action:'start'}),/host/);window.location.hostname='localhost';
 const startRun=run({action:'start'});assert.equal(attrs.src,'http://localhost:4400/site/paro/');assert.equal(attrs.srcdoc,undefined);
 const req=commands.at(-1).data;assert.equal(req.action,'start');assert.equal(commands.at(-1).origin,'http://localhost:4400');
 reply(req,{},{});reply(req,{},frame.contentWindow,'https://other.example');assert.equal(ticks.size,1);
 reply(req);assert.equal((await startRun).playing,true);assert.equal(ticks.size,0);
 const pause=run({action:'stop'});reply(commands.at(-1).data,{state:{playing:false}});assert.equal((await pause).playing,false);
 const jump=run({action:'checkpoint',checkpoint:'Dzong'});assert.equal(commands.at(-1).data.checkpoint,'Dzong');reply(commands.at(-1).data,{state:{checkpoint:'Dzong',time:634.7}});assert.equal((await jump).checkpoint,'Dzong');
 const speed=run({action:'speed',speed:4});reply(commands.at(-1).data,{state:{speed:4}});assert.equal((await speed).speed,4);
 const camera=run({action:'camera',camera:'cockpit'});reply(commands.at(-1).data,{state:{camera:'cockpit'}});assert.equal((await camera).camera,'cockpit');
 const failed=run({action:'status'});reply(commands.at(-1).data,{ok:false,error:'Loading failed'});await assert.rejects(failed,/Loading failed/);
 const left=run({action:'start'});active=false;[...ticks.values()][0]();await assert.rejects(left,/active slide/);active=true;
 const lostAuth=run({action:'start'});presenter=false;[...ticks.values()][0]();await assert.rejects(lostAuth,/presenter/);presenter=true;
 const timeout=run({action:'status'});now+=40001;[...ticks.values()][0]();await assert.rejects(timeout,/40 seconds/);
 const accepted=run({action:'status'}),acceptedReq=commands.at(-1).data,n=commands.length;
 listeners.message({source:frame.contentWindow,origin:'http://localhost:4400',data:{type:'vstd:simulation:accepted',id:acceptedReq.id}});
 [...ticks.values()][0]();assert.equal(commands.length,n);reply(acceptedReq);await accepted;
 attrs.srcdoc='launcher';frame.contentWindow.location={href:'http://localhost:4400/site/paro/'};
 const manual=run({action:'stop'});assert.equal(attrs.srcdoc,'launcher','manual launch was reloaded');reply(commands.at(-1).data,{state:{playing:false}});await manual;
 delete frame.contentWindow.location;
 attrs['data-vstd-simulation-src']='https://untrusted.example/app';attrs.srcdoc='launcher';await assert.rejects(run({action:'start'}),/same-origin/);
 assert.equal(ticks.size,0);
 attrs['data-vstd-bundle']='flight';delete attrs['data-vstd-simulation-hosts'];let launchedBundle=false;
 window.VSTDBundles={frame:()=>launchedBundle?frame:null,launch:async()=>{launchedBundle=true}};
 assert.equal((await run({action:'status'})).ready,false);
 const opaque=run({action:'start'});await Promise.resolve();const opaqueReq=commands.at(-1).data;
 assert.equal(commands.at(-1).origin,'*');reply(opaqueReq);assert.equal(ticks.size,1);reply(opaqueReq,{},frame.contentWindow,'null');await opaque;
})().catch(error=>{console.error(error);process.exitCode=1});
`
	if output, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", output, err)
	}
}

func TestVessicaSimulationToolDispatch(t *testing.T) {
	browser := chromium.Find("")
	if browser == "" {
		t.Skip("Chrome/Chromium unavailable")
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "studio.yaml"), "theme_default: default\n")
	writeFile(t, filepath.Join(root, "themes/default/theme.css"), ".slide{position:relative;width:1280px;height:720px}")
	writeFile(t, filepath.Join(root, "decks/demo/deck.yaml"), "title: Demo\ntheme: default\n")
	writeFile(t, filepath.Join(root, "decks/demo/slides/0010-sim.html"), `<section class="slide"><iframe data-vstd-simulation='{"name":"Test flight","checkpoints":["Dzong"],"speeds":[1,2,4],"cameras":["director","cockpit"]}' data-vstd-simulation-src="/sim" srcdoc="Launch"></iframe></section>`)
	writeFile(t, filepath.Join(root, "decks/demo/slides/0020-end.html"), `<section class="slide"><h1>End</h1></section>`)
	app := `<script>
const state={playing:true,checkpoint:'Dzong',speed:2,camera:'director'};
addEventListener('message',e=>{const r=e.data;if(r.type!=='vstd:simulation:command'||e.source!==parent)return;
if(r.action==='stop')state.playing=false;if(r.action==='start')state.playing=true;
if(r.action==='checkpoint')state.checkpoint=r.checkpoint;if(r.action==='speed')state.speed=r.speed;if(r.action==='camera')state.camera=r.camera;
parent.postMessage({type:'vstd:simulation:result',id:r.id,ok:true,state},e.origin);});
</script>`
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	entry, err := zw.CreateHeader(&zip.FileHeader{Name: "index.html", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = entry.Write([]byte(app)); err != nil {
		t.Fatal(err)
	}
	if err = zw.Close(); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archive.Bytes())
	manifest, _ := json.Marshal(map[string]any{"version": 1, "bundles": []any{map[string]any{"id": "test-flight", "hash": hex.EncodeToString(digest[:]), "bytes": archive.Len(), "expandedBytes": len(app), "fileCount": 1, "entrypoint": "index.html"}}})
	writeFile(t, filepath.Join(root, "library/manifest.json"), string(manifest))
	writeFile(t, filepath.Join(root, "decks/demo/slides/0020-end.html"), `<section class="slide"><div data-vstd-bundle="test-flight" data-vstd-simulation='{"name":"Bundled flight","checkpoints":["Dzong"],"speeds":[1,2,4],"cameras":["director","cockpit"]}'><button data-bundle-launch>Start</button><span data-bundle-status></span></div></section>`)
	writeFile(t, filepath.Join(root, "decks/demo/slides/0030-end.html"), `<section class="slide"><h1>End</h1></section>`)
	st, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	page, err := st.Build("demo")
	if err != nil {
		t.Fatal(err)
	}
	player, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/sim" {
			_, _ = w.Write([]byte(app))
			return
		}
		if r.URL.Path == "/assets/bundle/test-flight" {
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(archive.Bytes())
			return
		}
		if r.URL.Path == "/" {
			_, _ = w.Write(player)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	raw, err := chromium.Evaluate(ctx, browser, srv.URL+"/", `(async()=>{
if(document.readyState!=='complete'||!window.__vpres)return '';
window.__vme={presenter:true,editable:true};window.__vaudience=false;
const run=args=>window.__vpres.run('control_simulation',args).then(JSON.parse);
const a=await run({action:'start'}),b=await run({action:'stop'}),c=await run({action:'checkpoint',checkpoint:'Dzong'}),d=await run({action:'speed',speed:4}),e=await run({action:'camera',camera:'cockpit'});
if(!a.playing||b.playing||c.checkpoint!=='Dzong'||d.speed!==4||e.camera!=='cockpit')throw Error('Incorrect dispatch result');
window.__vaudience=true;let denied=false;try{await window.__vpres.run('control_simulation',{action:'start'})}catch(error){denied=/presenter/.test(error.message)}
if(!denied)throw Error('Audience was able to control simulation');window.__vaudience=false;
window.VSTDP.step(1);
const bundled=await run({action:'start'});if(!bundled.playing)throw Error('Bundled start failed');
const frame=document.querySelector('.slide.active [data-vstd-bundle] iframe');if(frame.getAttribute('sandbox')!=='allow-scripts')throw Error('Bundle sandbox weakened');
const pause=await run({action:'stop'});if(pause.playing)throw Error('Bundled pause failed');
const angle=await run({action:'camera',camera:'cockpit'});if(angle.camera!=='cockpit')throw Error('Bundled camera failed');
window.VSTDP.step(1);const unavailable=await window.__vpres.run('control_simulation',{action:'start'});if(!unavailable.startsWith('FAILED:'))throw Error('Non-simulator slide accepted command');
return 'passed';})()`)
	if err != nil {
		t.Fatal(err)
	}
	if raw != "passed" {
		t.Fatalf("unexpected tool result %q", raw)
	}
}

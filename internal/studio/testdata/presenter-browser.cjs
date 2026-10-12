// Real shared player with deterministic HTTP/media ports. No provider or hardware claim.
const {chromium}=require(process.env.VSTD_PLAYWRIGHT_MODULE||'@playwright/test');
const fs=require('node:fs'),path=require('node:path'),http=require('node:http'),assert=require('node:assert/strict');
const read=name=>fs.readFileSync(path.join(__dirname,'../templates',name),'utf8');
let html=read('player.html').replace('/*VSTD:META*/null/*:VSTD*/',JSON.stringify({deck:'demo',title:'AV test'})).replace('<!--VSTD:SLIDES-->','<section class="slide" data-vstd="0010-first"><h1>First</h1></section><section class="slide" data-vstd="0020-second"><h1>Second</h1></section>').replace('<!--VSTD:THEME-->','<style>.slide{width:1280px;height:720px;background:#fff;padding:80px;font:32px Arial}</style>').replace('/*VSTD:AVATAR*/',read('avatar.js')).replace('/*VSTD:PRESENTER*/',read('presenter.js')).replace('/*VSTD:LIVE*/',['live.js','voice.js','presentation-context.js'].map(read).join('\n')).replace('/*VSTD:BUNDLES*/',['bundle-download.js','bundles.js'].map(read).join('\n'));
let confirmed,command=null,revoked=false,writes=0,receipt=null;
const server=http.createServer(async(req,res)=>{
 res.setHeader('Content-Type','application/json');
 if(req.url==='/api/presenter/poll'){
  let data='';for await(const c of req)data+=c;const input=JSON.parse(data);confirmed=input.state;if(input.receipt)receipt=input.receipt;
  res.statusCode=revoked?401:200;res.end(JSON.stringify({command}));command=null;return;
 }
 if(req.url==='/api/me'){res.end(JSON.stringify({presenter:true,editable:false,remote_control:true,mode:'present',screen_name:'AV screen FIXTURE',revision_id:'fixed-revision',show_expires_at:new Date(Date.now()+3600000).toISOString(),capabilities:{}}));return;}
 if(req.url==='/api/realtime/token'){res.end(JSON.stringify({client_secret:{value:'fixture'}}));return;}
 if(req.url.startsWith('/v1/')){res.setHeader('Content-Type','text/plain');res.end('fixture-sdp');return;}
 if(req.url.startsWith('/api/deck/')){if(req.method!=='GET'&&!req.url.endsWith('/presenting'))writes++;res.end(JSON.stringify({slides:[],current:{page:1,title:'First',companion:'test'}}));return;}
 if(req.url.startsWith('/api/')){res.end('{}');return;}
 if(req.url==='/events'){res.setHeader('Content-Type','text/event-stream');res.end();return;}
 res.setHeader('Content-Type','text/html');res.end(html);
});
(async()=>{await new Promise(r=>server.listen(0,'127.0.0.1',r));const browser=await chromium.launch({headless:true});try{
 const page=await browser.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.addInitScript(()=>{
  class Peer{constructor(){this.iceConnectionState='connected';}createDataChannel(){this.dc={readyState:'open',send(){},close(){}};return this.dc;}async createOffer(){return {type:'offer',sdp:'fixture'};}async setLocalDescription(x){this.localDescription=x;}async setRemoteDescription(){setTimeout(()=>this.dc.onopen?.(),0);}close(){}}
  window.RTCPeerConnection=Peer;Object.defineProperty(navigator,'mediaDevices',{value:{getUserMedia:async()=>({getTracks:()=>[],getAudioTracks:()=>[{readyState:'live'}]}),enumerateDevices:async()=>[]}});Peer.prototype.addTrack=()=>{};
 });
 await page.goto('http://127.0.0.1:'+server.address().port+'/?mode=presentation');await page.getByRole('button',{name:'Enable show',exact:true}).waitFor();
 await page.evaluate(()=>{window.VSTD.realtime={base:location.origin+'/v1',model:'fixture'};});
 await page.getByRole('button',{name:'Enable show',exact:true}).click();await page.waitForFunction(()=>window.VSTDShowControl.state().voice==='asleep');
 await page.keyboard.press('PageDown');await page.waitForFunction(()=>window.VSTDP.cur()===1);
 await new Promise(r=>setTimeout(r,1200));assert.equal(confirmed.slide,2,'HID clicker position reaches confirmed state');
 command={id:'wake-test',sequence:1,action:'wake'};await page.waitForFunction(()=>window.VSTDShowControl.state().voice==='awake');
 command={id:'sleep-test',sequence:2,action:'sleep'};await page.waitForFunction(()=>window.VSTDShowControl.state().voice==='asleep');
 command={id:'avatar-test',sequence:3,action:'avatar-on'};await page.waitForFunction(()=>window.VSTDShowControl.state().avatar===true);await new Promise(r=>setTimeout(r,1200));assert.equal(receipt.id,'avatar-test');assert.equal(receipt.ok,true);
 const denied=await page.evaluate(()=>window.__vpres.run('edit_text',{find:'Second',replace:'HACKED'}));assert.match(denied,/Read-only/);assert.equal((await page.locator('#frame').textContent()).trim(),'FirstSecond');
 assert.equal(await page.locator('#editbtn').isVisible(),false);assert.equal(writes,0);revoked=true;await page.waitForFunction(()=>document.getElementById('frame').children.length===0);assert.equal(await page.evaluate(()=>window.VSTDShowControl.state().voice),'off');assert.deepEqual(errors,[]);console.log(JSON.stringify({passed:true,hid:true,acknowledged:true,readonly:true,revocation:true,provider:'mock',microphone:'mock'}));
 }finally{await browser.close();server.close();}})().catch(e=>{console.error(e);server.close();process.exitCode=1;});

// Full player HUD acceptance, local HTTP fixtures only; no microphone/provider calls.
const {chromium}=require(process.env.VSTD_PLAYWRIGHT_MODULE||'@playwright/test');
const fs=require('node:fs'),path=require('node:path'),http=require('node:http'),assert=require('node:assert/strict');
const read=name=>fs.readFileSync(path.join(__dirname,'../templates',name),'utf8');
let html=read('player.html').replace('/*VSTD:META*/null/*:VSTD*/',JSON.stringify({deck:'demo',title:'Avatar modes'})).replace('<!--VSTD:SLIDES-->','<section class="slide" data-vstd="0010-demo"><h1>Vessica avatar modes</h1><p>V: listen · Shift+V: wake/sleep</p></section>').replace('<!--VSTD:THEME-->','<style>.slide{width:1280px;height:720px;background:#f8f4ea;padding:80px;color:#173c39;font:32px Arial}</style>').replace('/*VSTD:AVATAR*/',read('avatar.js')).replace('/*VSTD:LIVE*/',['live.js','voice.js','presentation-context.js'].map(read).join('\n')).replace('/*VSTD:BUNDLES*/',['bundle-download.js','bundles.js'].map(read).join('\n'));
let allocations=0;
const server=http.createServer((req,res)=>{
 if(req.url.startsWith('/api/')){if(req.url.startsWith('/api/avatar/'))allocations++;res.setHeader('Content-Type','application/json');res.end(JSON.stringify(req.url==='/api/me'?{presenter:true,editable:false,mode:'present',capabilities:{}}:{}));}
 else if(req.url==='/events'){res.writeHead(200,{'Content-Type':'text/event-stream'});res.end();}
 else{res.setHeader('Content-Type','text/html');res.end(html);}
});
(async()=>{await new Promise(r=>server.listen(0,'127.0.0.1',r));const browser=await chromium.launch({headless:true});try{
 const page=await browser.newPage({viewport:{width:1440,height:900}});const errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.goto('http://127.0.0.1:'+server.address().port+'/?mode=presentation');
 const toggle=page.getByRole('switch');await toggle.waitFor({state:'visible'});assert.equal(await toggle.getAttribute('aria-checked'),'false');
 await toggle.click();assert.equal(await toggle.getAttribute('aria-checked'),'true');assert.equal(await toggle.textContent(),'Avatar on');
 await page.reload();await toggle.waitFor({state:'visible'});assert.equal(await toggle.getAttribute('aria-checked'),'true','selection survives page session');
 await toggle.click();assert.equal(await toggle.getAttribute('aria-checked'),'false');await page.reload();await toggle.waitFor({state:'visible'});assert.equal(await toggle.getAttribute('aria-checked'),'false');
 await page.mouse.move(720,850);await page.waitForFunction(()=>Number(getComputedStyle(document.getElementById('hud')).opacity)>.95);await page.screenshot({path:'/tmp/vessica-avatar-mode-hud.png'});assert.equal(allocations,0,'switch alone never starts a paid renderer');assert.deepEqual(errors,[]);
 console.log(JSON.stringify({passed:true,persistence:true,allocations,screenshot:'/tmp/vessica-avatar-mode-hud.png'}));
 }finally{await browser.close();server.close();}})().catch(e=>{console.error(e);server.close();process.exitCode=1;});

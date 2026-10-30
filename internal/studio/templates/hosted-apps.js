/* Hosted applications remain on their own origins. Only this bridge owns capabilities. */
(()=>{
 const pending=new Map(),sessions=new WeakMap();let serial=0;
 const current=()=>window.VSTDP?.slideEl();
 const frame=()=>current()?.querySelector('iframe[data-vstd-hosted-app]');
 const active=f=>f&&frame()===f&&!document.hidden;
 function origin(f){const u=new URL(f.getAttribute('src'),location.href);if(u.protocol!=='https:')throw Error('Hosted application requires HTTPS');return u.origin;}
 function entry(f){let s=sessions.get(f);if(!s){s={channel:crypto.randomUUID(),state:null,received:0,expires:0,renewing:false};sessions.set(f,s);}return s;}
 function send(f,value){f.contentWindow.postMessage({appId:f.dataset.vstdHostedApp,channel:entry(f).channel,...value},origin(f));}
 async function initialize(f){
  previous=f;const s=entry(f);if(s.renewing||!active(f))return;s.renewing=true;
  try{
   let capability=null;
   if(window.VSTDPresenterControl?.()){
    const r=await fetch('/api/hosted-apps/'+encodeURIComponent(f.dataset.vstdHostedApp)+'/capability',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}',signal:AbortSignal.timeout(8000)});
    if(r.ok){const data=await r.json();if(data.origin!==origin(f)||data.appId!==f.dataset.vstdHostedApp)throw Error('Hosted application origin mismatch');capability=data;}
   }
   if(!active(f))return;
   s.expires=capability?.expiresAt||0;send(f,{type:'vstd:hosted-app:init',...(capability?{token:capability.token}:{})});
  }catch(_){if(active(f))send(f,{type:'vstd:hosted-app:init'});s.expires=0;}
  finally{s.renewing=false;}
 }
 addEventListener('message',event=>{
  const f=frame(),m=event.data;if(!f||event.source!==f.contentWindow||!m||m.appId!==f.dataset.vstdHostedApp)return;
  try{if(event.origin!==origin(f))return;}catch(_){return;}
  const s=entry(f);
  if(m.type==='vstd:hosted-app:ready'){initialize(f);return;}
  if(m.channel!==s.channel||!active(f))return;
  if(m.type==='vstd:hosted-app:state'&&m.state&&JSON.stringify(m.state).length<20000){
   s.state=m.state;s.received=Date.now();document.dispatchEvent(new CustomEvent('vstd:hosted-app-state'));return;
  }
  if(m.type==='vstd:hosted-app:result'){
   const p=pending.get(m.id);if(!p||p.frame!==f)return;clearTimeout(p.timer);pending.delete(m.id);
   m.ok?p.resolve(m.result):p.reject(Error(String(m.error||'Application rejected command').slice(0,400)));
  }
 });
 let previous;
 document.addEventListener('vstd:slide',()=>{
  if(previous&&previous!==frame())send(previous,{type:'vstd:hosted-app:init'});
  for(const [id,p] of pending){clearTimeout(p.timer);p.reject(Error('Application slide changed'));pending.delete(id);}
  previous=frame();if(previous)initialize(previous);
 });
 setInterval(()=>{const f=frame();if(f&&active(f)&&window.VSTDPresenterControl?.()&&entry(f).expires<Date.now()+60000)initialize(f);},15000);
 window.VSTDHostedApps={
  context(){const f=frame();if(!f)return null;const s=entry(f);return {appId:f.dataset.vstdHostedApp,name:f.title,available:active(f),receivedAt:s.received?new Date(s.received).toISOString():null,stale:!s.received||Date.now()-s.received>15000,state:s.state};},
  async run(args){
   const f=frame();if(!active(f))throw Error('No hosted application on the active slide');
   if(!window.VSTDPresenterControl?.())throw Error('Presenter authorization required');
   const s=entry(f);if(args.action!=='status'&&s.expires<=Date.now())await initialize(f);
   if(!active(f))throw Error('Application slide changed');
   const id='hosted-'+(++serial)+'-'+crypto.randomUUID();
   return new Promise((resolve,reject)=>{const timer=setTimeout(()=>{pending.delete(id);reject(Error('Application did not confirm the command'));},12000);pending.set(id,{frame:f,resolve,reject,timer});send(f,{type:'vstd:hosted-app:command',id,args});});
  }
 };
})();

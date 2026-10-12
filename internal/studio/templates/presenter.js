/* Hosted AV transport. Local players never contact it unless /api/me opts in. */
(function(){
  let enabled=false,finished=false,receipt=null,last=0;
  const boot=crypto.randomUUID();
  const wait=setInterval(()=>{
    if(!window.__vme)return;
    clearInterval(wait);
    if(!window.__vme.remote_control)return;
    enabled=true;setup();void poll();const expires=Date.parse(window.__vme.show_expires_at);if(Number.isFinite(expires))setTimeout(end,Math.max(0,expires-Date.now()));
  },100);
  function end(){finished=true;window.VSTDShowControl.stop();const frame=document.getElementById('frame');if(frame)frame.replaceChildren();window.VSTDP.toast('Show access ended. Return to Studio for another show.');}
  function setup(){
    const panel=document.createElement('div');
    panel.id='showSetup';panel.style.cssText='position:fixed;z-index:99999;top:12px;left:12px;padding:16px;background:#172326;color:white;border-radius:12px;max-width:420px;font:16px system-ui';
    const name=document.createElement('strong');name.textContent=window.__vme.screen_name+' · revision '+window.__vme.revision_id;
    const instructions=document.createElement('p');instructions.textContent='Use the AV computer microphone and venue sound output. Enable show once, allow microphone access, then confirm this screen on your phone. Check voice and avatar in rehearsal.';
    const select=document.createElement('select');select.setAttribute('aria-label','Venue microphone');select.innerHTML='<option value="">System default microphone</option>';
    const enable=document.createElement('button');enable.textContent='Enable show';enable.style.cssText='padding:14px;margin:8px;font-size:18px';
    const status=document.createElement('p');status.setAttribute('role','status');
    enable.onclick=async()=>{
      // Fullscreen must be requested within this local gesture.
      document.documentElement.requestFullscreen?.().catch(()=>{});
      enable.disabled=true;
      try{await window.VSTDShowControl.enable(select.value);status.textContent='Show enabled. Confirm the venue sound output in your system settings.';
        const devices=await navigator.mediaDevices.enumerateDevices();
        for(const device of devices.filter(d=>d.kind==='audioinput')){
          const option=document.createElement('option');option.value=device.deviceId;option.textContent=device.label||'Microphone';select.append(option);
        }
      }catch(e){status.textContent=e.message;}finally{enable.disabled=false;}
    };
    select.onchange=()=>{window.VSTDShowControl.stop();status.textContent='Microphone changed. Tap Enable show again.';};
    const hide=document.createElement('button');hide.textContent='Hide setup';hide.onclick=()=>panel.remove();
    const finish=document.createElement('button');finish.textContent='End show';finish.onclick=()=>{void fetch('/api/presenter/end',{method:'POST'});end();};
    panel.append(name,instructions,select,enable,hide,finish,status);document.body.append(panel);
  }
  async function poll(){
    if(!enabled||finished)return;
    try{
      const slide=window.VSTDP?.slideEl();
      const state={...window.VSTDShowControl.state(),slide:window.VSTDP?.cur()+1,slideId:slide?.dataset.slideId||slide?.id||'',title:slide?.querySelector('h1,h2')?.textContent?.slice(0,200)||''};
      const r=await fetch('/api/presenter/poll',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({boot,state,receipt}),signal:AbortSignal.timeout(8000)});
      if(r.status===401||r.status===410||r.status===409){end();return;}
      if(!r.ok)throw Error('Disconnected');
      const result=await r.json();receipt=null;
      if(result.command&&result.command.sequence>last){
        last=result.command.sequence;
        try{await window.VSTDShowControl.command(result.command.action);receipt={id:result.command.id,ok:true};}
        catch(e){receipt={id:result.command.id,ok:false,error:e.message.slice(0,200)};}
      }
    }catch(_){/* Navigation and playback continue through transport loss. */}
    setTimeout(poll,1000);
  }
})();

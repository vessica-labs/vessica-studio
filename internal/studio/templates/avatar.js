/* Optional LiveAvatar overlay. The host owns credentials and paid session
   admission. Merely building/loading a deck never starts a renderer. */
(function(){
  const SDK='https://cdn.jsdelivr.net/npm/livekit-client@2.22.4/dist/livekit-client.esm.mjs';
  const WORKLET=`class VessicaCapture extends AudioWorkletProcessor {
    constructor(){super();this.buffer=new Int16Array(480);this.offset=0;}
    process(inputs){const a=inputs[0]?.[0];if(!a)return true;
      for(const x of a){this.buffer[this.offset++]=Math.round(Math.max(-1,Math.min(1,x))*32767);
        if(this.offset===480){this.port.postMessage(this.buffer.buffer,[this.buffer.buffer]);this.buffer=new Int16Array(480);this.offset=0;}}
      return true;}
  }registerProcessor('vessica-avatar-capture',VessicaCapture);`;
  function keyPixels(data){
    for(let i=0;i<data.length;i+=4){
      const r=data[i],g=data[i+1],b=data[i+2],max=Math.max(r,g,b),min=Math.min(r,g,b);
      // Green dominance preserves neutral/skin pixels, with a soft alpha edge.
      if(g>r*1.15&&g>b*1.15&&max-min>25){
        const alpha=Math.max(0,1-(g-Math.max(r,b))/Math.max(g,1)*5);
        data[i+3]=Math.round(255*alpha);
      }
    }
  }
  async function capture(stream,onFrame){
    const context=new AudioContext({sampleRate:24000,latencyHint:'interactive'});
    let source,node,url;
    const close=()=>{source?.disconnect();node?.disconnect();node&&(node.port.onmessage=null);void context.close();};
    try{
      if(context.sampleRate!==24000)throw new Error('Avatar requires 24 kHz audio');
      url=URL.createObjectURL(new Blob([WORKLET],{type:'text/javascript'}));
      await context.audioWorklet.addModule(url);await context.resume();
      node=new AudioWorkletNode(context,'vessica-avatar-capture');node.port.onmessage=e=>onFrame(new Int16Array(e.data));
      source=context.createMediaStreamSource(stream);source.connect(node);node.connect(context.destination);
      return close;
    }catch(e){close();throw e;}finally{if(url)URL.revokeObjectURL(url);}
  }
  function surface(){
    const canvas=document.createElement('canvas'),video=document.createElement('video'),audio=document.createElement('div');
    canvas.setAttribute('aria-label','Vessica');canvas.style.cssText='position:fixed;right:max(24px,env(safe-area-inset-right));bottom:76px;height:min(42vh,340px);max-width:30vw;object-fit:contain;z-index:190;pointer-events:none;background:transparent';
    canvas.hidden=true;video.muted=true;video.autoplay=true;video.playsInline=true;video.hidden=true;audio.hidden=true;
    document.body.append(canvas,video,audio);
    const ctx=canvas.getContext('2d',{alpha:true,willReadFrequently:true});let frame;
    function render(){
      if(video.readyState>=2){
        const scale=Math.min(1,480/video.videoHeight);canvas.width=Math.round(video.videoWidth*scale);canvas.height=Math.round(video.videoHeight*scale);
        ctx.drawImage(video,0,0,canvas.width,canvas.height);const pixels=ctx.getImageData(0,0,canvas.width,canvas.height);keyPixels(pixels.data);ctx.putImageData(pixels,0,0);
      }
      frame=requestAnimationFrame(render);
    }
    render();
    return {video,audio,show:on=>{canvas.hidden=!on;},close(){cancelAnimationFrame(frame);video.srcObject=null;canvas.remove();video.remove();audio.remove();}};
  }
  window.VSTDAvatar={keyPixels,
    create(callbacks,deps={}){
      const request=deps.request||((action,id)=>fetch('/api/avatar/'+action,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({id}),keepalive:action==='stop',signal:action==='stop'?undefined:AbortSignal.timeout(25000)}).then(async r=>{if(!r.ok)throw new Error('Avatar unavailable');return r.json();}));
      const load=deps.load||(()=>import(SDK));
      let awake=false,stream=null,current=null,generation=0;
      function send(s,event){if(s.ws?.readyState===1){if(s.ws.bufferedAmount>512*1024)throw new Error('Avatar upload fell behind');s.ws.send(JSON.stringify(event));}}
      async function dispose(s){
        if(!s||s.closed)return;s.closed=true;
        clearInterval(s.heartbeat);clearTimeout(s.timeout);s.capture?.();s.ws?.close();
        s.audios.forEach(a=>{a.pause?.();a.srcObject=null;});s.room?.disconnect();s.surface?.close();
        if(current===s){current=null;callbacks.route(false);}
        try{await request('stop',s.id);}catch(_){/* Host lease is the cleanup fallback. */}
      }
      function fail(s){if(current!==s||s.closed)return;void dispose(s);callbacks.unavailable?.();}
      function route(s,on){if(s.routed===on)return;s.routed=on;callbacks.route(on);s.surface.show(on);s.audios.forEach(a=>a.muted=!on);}
      function flush(s){
        if(!s.samples)return;
        const samples=new Int16Array(s.samples);let offset=0;
        for(const chunk of s.chunks){samples.set(chunk,offset);offset+=chunk.length;}
        const bytes=new Uint8Array(samples.buffer);let binary='';for(const x of bytes)binary+=String.fromCharCode(x);
        send(s,{type:'agent.speak',event_id:s.utterance,audio:btoa(binary)});
        s.chunks=[];s.samples=0;s.firstChunk=false;
      }
      function frame(s,samples){
        if(!awake||current!==s||s.closed)return;
        let energy=0;for(const x of samples)energy+=x*x;
        const silent=Math.sqrt(energy/samples.length)<260;s.silence=silent?s.silence+20:0;
        // Start the synchronized path only at a pause. A reply already audible
        // through ordinary voice finishes there instead of playing twice.
        if(s.mediaReady&&s.controlReady&&!s.routed&&s.silence>=300)route(s,true);
        if(s.routed)try{
          // Drop the remainder of interrupted WebRTC audio until its next pause.
          if(s.interrupted){if(s.silence>=300)s.interrupted=false;return;}
          if(!s.utterance){
            if(silent)return;
            s.utterance=crypto.randomUUID();s.chunks=[];s.samples=0;s.firstChunk=true;
          }
          s.chunks.push(samples.slice());s.samples+=samples.length;
          // Buffer 600 ms initially, then 1 second, matching provider guidance.
          if(s.samples>=(s.firstChunk?14400:24000))flush(s);
          if(s.silence>=300){flush(s);send(s,{type:'agent.speak_end',event_id:s.utterance});s.utterance=null;}
        }catch(_){fail(s);}
      }
      async function start(){
        if(!awake||!stream||current)return;
        const s={id:crypto.randomUUID(),generation,closed:false,audios:[],silence:0};current=s;
        try{
          // Load/capture before any paid provider session is allocated.
          const sdk=await load();if(s.closed||s.generation!==generation)return;
          s.capture=await (deps.capture||capture)(stream,samples=>frame(s,samples));
          if(s.closed){s.capture();return;}
          s.surface=(deps.surface||surface)();
          const info=await request('start',s.id);
          if(s.closed||s.generation!==generation){await request('stop',s.id);return;}
          s.timeout=setTimeout(()=>fail(s),15000);
          s.heartbeat=setInterval(()=>request('keepalive',s.id).catch(()=>fail(s)),15000);
          s.ws=new (deps.WebSocket||WebSocket)(info.ws_url);
          s.ws.onmessage=e=>{
            if(s.closed)return;
            let event;try{event=JSON.parse(e.data);}catch(_){return;}
            if(event.type==='session.state_updated'&&event.state==='connected'){s.controlReady=true;if(s.mediaReady&&s.controlReady)clearTimeout(s.timeout);}
            if(event.type==='error'||(event.type==='session.state_updated'&&event.state==='disconnected'))fail(s);
          };
          s.ws.onerror=()=>fail(s);s.ws.onclose=()=>fail(s);
          s.room=new sdk.Room({singlePeerConnection:false});
          let hasVideo=false,hasAudio=false;
          s.room.on(sdk.RoomEvent.TrackSubscribed,track=>{
            if(s.closed)return;
            if(track.kind===sdk.Track.Kind.Video){track.attach(s.surface.video);hasVideo=true;void s.surface.video.play().catch(()=>fail(s));}
            if(track.kind===sdk.Track.Kind.Audio){const a=track.attach();a.muted=true;s.audios.push(a);s.surface.audio.append(a);hasAudio=true;}
            s.mediaReady=hasVideo&&hasAudio;if(s.mediaReady&&s.controlReady)clearTimeout(s.timeout);
          });
          s.room.on(sdk.RoomEvent.Disconnected,()=>fail(s));
          s.room.on(sdk.RoomEvent.AudioPlaybackStatusChanged,()=>{if(!s.room.canPlaybackAudio)fail(s);});
          await s.room.connect(info.livekit_url,info.livekit_client_token);
          if(s.closed){s.room.disconnect();return;}
          await s.room.startAudio();
        }catch(_){fail(s);}
      }
      const unload=()=>{awake=false;generation++;void dispose(current);};
      window.addEventListener?.('pagehide',unload);
      return {
        attach(value){stream=value;if(awake)void start();},
        begin(){if(awake)return;awake=true;generation++;void start();},
        end(){awake=false;generation++;void dispose(current);},
        interrupt(){const s=current;if(s?.controlReady)try{
          s.utterance=null;s.chunks=[];s.samples=0;s.interrupted=true;s.silence=0;
          send(s,{type:'agent.interrupt',event_id:crypto.randomUUID()});
        }catch(_){fail(s);}},
        retry(){const s=current;if(s?.room)void s.room.startAudio().catch(()=>fail(s));},
        close(){this.end();stream=null;window.removeEventListener?.('pagehide',unload);}
      };
    }
  };
})();

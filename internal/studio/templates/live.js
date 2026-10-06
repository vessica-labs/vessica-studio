/* Cloud GPT-Live transport; local Realtime sessions retain their existing protocol. */
(function(){
  let finalizing=Promise.resolve();
  window.VSTDLive={
    async negotiate(pc,dc,config){
      await finalizing;
      if(pc.iceGatheringState!=='complete')await new Promise((resolve,reject)=>{
        const timeout=setTimeout(()=>{pc.removeEventListener('icegatheringstatechange',check);reject(new Error('ICE gathering timed out'));},10000);
        function check(){if(pc.iceGatheringState==='complete'){clearTimeout(timeout);pc.removeEventListener('icegatheringstatechange',check);resolve();}}
        pc.addEventListener('icegatheringstatechange',check);check();
      });
      const response=await fetch('/api/live/session',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({sdp:pc.localDescription.sdp,instructions:config.instructions,tools:config.tools,dictation:!!config.dictation}),signal:AbortSignal.timeout(45000)});
      const result=await response.json();
      if(!response.ok)throw new Error(result.message||'Live session could not start');
      if(!result.session?.id||!result.transport?.sdp)throw new Error('Invalid Live connection receipt');
      return {answer:result.transport.sdp,adapter:this.adapter(dc,true)};
    },
    adapter(dc,relay=false){
      let ready=false,closed=false,closing=false,finished;
      const queue=[],calls=new Set(),done=new Promise(r=>finished=r);
      let pollTimer,failures=0,configuration=Promise.resolve();
      const dispatch=event=>dc.dispatchEvent(new MessageEvent('message',{data:JSON.stringify(event)}));
      async function poll(){
        if(closing)return;
        try{
          const response=await fetch('/api/live/events',{signal:AbortSignal.timeout(5000)});
          if(!response.ok)throw new Error('Live event relay unavailable');
          const result=await response.json();failures=0;
          (result.events||[]).forEach(dispatch);
        }catch(_){if(++failures>=3){dispatch({type:'error',error:{message:'Live connection lost'}});raw({type:'session.close'});closing=true;return;}}
        if(!closing)pollTimer=setTimeout(poll,300);
      }
      if(relay)pollTimer=setTimeout(poll,0);
      function raw(event){if(dc.readyState==='open')dc.send(JSON.stringify(event));}
      function send(event){
        if(closing)return;
        if(!ready){queue.push(event);return;}
        if(event.type==='session.update'){
          const session=event.session;
          if(relay){configuration=configuration.then(async()=>{
            const response=await fetch('/api/live/context',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({sdp:'context',instructions:session.instructions,tools:session.tools}),signal:AbortSignal.timeout(5000)});
            if(!response.ok)throw new Error('Live context update failed');
          }).catch(()=>{dispatch({type:'error',error:{message:'Live context update failed'}});raw({type:'session.close'});});}
          else raw({type:'session.update',session:{delegation:{responses:{instructions:session.instructions,tools:session.tools,tool_choice:'auto',parallel_tool_calls:false}}}});return;
        }
        if(event.type==='conversation.item.create'){
          if(event.item.type==='function_call_output')raw({type:'response.item.create',item:event.item});
          else raw({type:'session.thinking.append',delegation_id:null,content:(event.item.content||[]).map(x=>x.text||'').join('\n').slice(0,1400)});
          return;
        }
        // Live manages delegation independently of speech. Return tool results
        // and continue immediately; a Realtime-style busy queue can deadlock
        // waiting for the very continuation it has held back. Context updates
        // are serialized separately and must not hold completed tool results.
        if(event.type==='response.create'){raw({type:'response.create'});return;}
        raw(event);
      }
      return {send,
        event(message){
          if(message.type==='session.started'){ready=true;queue.splice(0).forEach(send);return {type:'session.updated'};}
          if(message.type==='session.closed'){closed=true;finished();return {type:'live.closed'};}
          if(message.type==='response.event'){
            const event=message.event;
            if(event.type==='response.created')return {type:'response.created'};
            if(event.type==='response.output_item.done'&&event.item?.type==='function_call'&&!calls.has(event.item.call_id)){
              calls.add(event.item.call_id);return {...event.item,type:'response.function_call_arguments.done'};
            }
            if(event.type==='response.completed'||event.type==='response.failed'||event.type==='response.incomplete'){
              return {type:'response.done',response:{status:event.type==='response.completed'?'completed':'failed'}};
            }
            return null;
          }
          if(message.type==='session.input_transcript.delta')return {type:'conversation.item.input_audio_transcription.completed',transcript:message.delta};
          return message;
        },
        close(){
          clearTimeout(pollTimer);if(!closing){closing=true;if(ready&&!closed)raw({type:'session.close'});}
          finalizing=(async()=>{let timer;await Promise.race([done,new Promise(r=>timer=setTimeout(r,10000))]);clearTimeout(timer);await window.__vendRealtimeSession?.();return closed;})();return finalizing;
        }
      };
    }
  };
})();

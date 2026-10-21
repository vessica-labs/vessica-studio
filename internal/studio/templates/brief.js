/* Per-deck conversational memory. Only distilled Markdown is stored by the host. */
(function(){
  window.VSTDDeckBrief={
    create(options){
      const transport=options.fetch||fetch,base='/api/vessica/'+encodeURIComponent(options.deck)+'/brief';
      let state=null,pending='',timer=null,flight=null,epoch=0,status='Loading…',lastRole='';
      const changed=()=>options.changed?.(state,status);
      function apply(next){
        if(!next||typeof next.generation!=='string'||typeof next.markdown!=='string'||!Number.isInteger(next.revision))throw Error('Invalid brief');
        const reset=state&&state.generation!==next.generation;
        if(state&&!reset&&next.revision<state.revision)return;
        const updated=!state||reset||state.revision!==next.revision;state=next;
        if(reset){epoch++;pending='';lastRole='';clearTimeout(timer);options.reset?.();}
        if(updated)options.context?.(next.markdown);changed();
      }
      async function refresh(){
        const token=epoch;
        const r=await transport(base,{cache:'no-store'});if(!r.ok){status='Brief unavailable';changed();throw Error(status);}
        const next=await r.json();if(token===epoch){apply(next);if(!flight&&!pending)status='Saved';changed();}
        return state;
      }
      async function flush(conflicts=0){
        clearTimeout(timer);
        if(flight){await flight;if(pending)return flush();return;}
        if(!pending)return;
        if(!state)await refresh();
        const token=epoch,text=pending.slice(0,16000);pending=pending.slice(text.length);lastRole='';
        status='Saving…';changed();
        flight=(async()=>{
          try{
            const r=await transport(base,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({generation:state.generation,revision:state.revision,text}),keepalive:true});
            if(token!==epoch)return;
            if(r.status===409){await refresh();if(token===epoch){if(conflicts>=2)throw Error('Brief changed in another window');pending=text+'\n'+pending;conflicts++;}return;}
            if(!r.ok)throw Error('Brief could not be saved');
            const next=await r.json();if(token!==epoch)return;apply(next);status=pending?'Waiting to save…':'Saved';changed();
          }catch(error){if(token===epoch){pending=text+'\n'+pending;status='Not saved · '+error.message;changed();}throw error;}
        })();
        try{await flight;}finally{flight=null;}
        if(pending)return flush(conflicts);
      }
      return {
        refresh,flush,
        markdown:()=>state?.markdown||'',
        capture(role,text){
          if(options.readonly?.()||typeof text!=='string'||!text.trim())return;
          pending+=(role===lastRole?'':'\n'+role+': ')+text;lastRole=role;status='Waiting to save…';changed();clearTimeout(timer);
          // Batch a conversational exchange; saving never blocks voice output.
          timer=setTimeout(()=>flush().catch(()=>{}),options.delay??2500);
        },
        async clear(){
          if(options.readonly?.())throw Error('This presentation can only read the brief');
          epoch++;pending='';lastRole='';clearTimeout(timer);
          const r=await transport(base,{method:'DELETE'});if(!r.ok){status='Could not clear context';changed();throw Error(status);}
          apply(await r.json());status='Cleared';changed();
        },
        readonly:()=>!!options.readonly?.(),
        state:()=>state,
        status:()=>status
      };
    },
    drawer(brief){
      let dialog;
      function render(){
        if(!dialog)return;
        dialog.querySelector('pre').textContent=brief.markdown()||'No context saved yet. Talk with Vessica to prepare for this meeting.';
        dialog.querySelector('[role="status"]').textContent=brief.status();
      }
      return {render,async open(){
        if(!dialog){
          dialog=document.createElement('dialog');dialog.setAttribute('aria-label','Deck meeting brief');
          dialog.style.cssText='width:min(760px,90vw);max-height:80vh;padding:24px;border:1px solid #777;border-radius:12px;background:#17191e;color:#efeee7;';
          dialog.innerHTML='<h2 style="margin-top:0">Meeting brief</h2><p>Saved context for this deck. Vessica can reference everything here in conversation.</p><p role="status" aria-live="polite"></p><pre style="white-space:pre-wrap;overflow-wrap:anywhere;max-height:48vh;overflow:auto;font:15px/1.5 ui-monospace,monospace;user-select:text"></pre><div style="display:flex;gap:12px;justify-content:flex-end"><button type="button" data-clear>Clear context</button><button type="button" data-close>Close</button></div>';
          dialog.querySelector('[data-close]').onclick=()=>dialog.close();
          dialog.querySelector('[data-clear]').onclick=async()=>{
            const button=dialog.querySelector('[data-clear]');button.disabled=true;
            try{await brief.clear();}catch(_){}finally{button.disabled=false;render();}
          };
          document.body.appendChild(dialog);
        }
        render();dialog.querySelector('[data-clear]').hidden=brief.readonly();if(!dialog.open)dialog.showModal();
        try{await brief.refresh();}catch(_){dialog.querySelector('[role="status"]').textContent='Brief unavailable';}
        render();
      }};
    }
  };
})();

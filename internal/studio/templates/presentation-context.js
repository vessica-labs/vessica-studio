/* Presenter context is read on demand; companion notes never enter exported HTML. */
(function(){
  window.VSTDPresentationContext={create({deck,current,slides,fetch:request=window.fetch.bind(window)}){
    let generation=0,loaded=null,error=null;
    const summaries=new Map();
    const slideID=el=>el?.dataset?.vstd||'';
    const text=value=>String(value||'').trim().replace(/\s+/g,' ');
    function snapshot(){
      let number=0;
      const elements=Array.from(slides()),active=current();
      const pages=elements.map(el=>{
        const parked=el.hasAttribute('data-parked');
        return {id:slideID(el),page:parked?0:++number,title:text((el.querySelector('.s-title,[data-slide-title]')||el.querySelector('h1')||el.querySelector('h2')||el.querySelector('.serif'))?.textContent||el.dataset?.menu||slideID(el)).slice(0,160),summary:summaries.get(slideID(el))||'',hidden:el.hasAttribute('data-hidden'),parked};
      });
      const selected=pages[elements.indexOf(active)]||{id:slideID(active),page:0,title:''};
      return {current:{...selected,total: number,visible_text:text(active?.innerText||'').slice(0,12000),companion:loaded?.current.id===selected.id?loaded.current.companion:null},pages,error};
    }
    async function refresh(){
      const sequence=++generation,id=slideID(current());
      loaded=null;error=null;
      if(!id){error='No selected slide ID';return snapshot();}
      try{
        const response=await request('/api/deck/'+encodeURIComponent(deck)+'/voice-context?slide='+encodeURIComponent(id),{cache:'no-store',signal:AbortSignal.timeout(15000)});
        if(!response.ok)throw new Error('Presentation context HTTP '+response.status);
        const result=await response.json();
        if(result.current?.id!==id||!Array.isArray(result.pages))throw new Error('Invalid presentation context');
        if(sequence===generation&&slideID(current())===id){
          loaded=result;summaries.clear();result.pages.forEach(page=>summaries.set(page.id,page.summary));
        }
      }catch(cause){if(sequence===generation)error=cause.message;}
      return snapshot();
    }
    async function read(){
      // A tool call can overlap manual navigation or another context read.
      // Never return another page's companion as the current page's narrative.
      for(let attempt=0;attempt<3;attempt++){
        const id=slideID(current());await refresh();const state=snapshot();
        if(state.current.id===id&&state.current.companion!==null)return state;
        if(state.error)throw new Error(state.error);
      }
      throw new Error('Selection changed while reading context; read again');
    }
    function prompt(limit=26000){
      const state=snapshot();
      const summaryLimit=Math.max(0,Math.min(360,Math.floor((limit-4500)/Math.max(1,state.pages.length))-220));
      const compact={current:{...state.current,visible_text:state.current.visible_text.slice(0,2000),companion:null},pages:state.pages.map(page=>({...page,title:page.title.slice(0,100),summary:page.summary.slice(0,summaryLimit)})),error:state.error};
      if(JSON.stringify(compact).length>limit-1000){compact.pages=[];compact.toc_requires_tool='Call get_presentation_context for the complete table of contents.';}
      const room=Math.max(0,limit-JSON.stringify(compact).length-160);
      compact.current.companion=state.current.companion?.slice(0,room)??null;
      if(state.current.companion?.length>room)compact.companion_truncated='Call get_presentation_context for the full companion Markdown.';
      // JSON escaping can expand Markdown. Reduce only the excerpt, preserving
      // a valid payload and the explicit instruction to read the complete file.
      while(JSON.stringify(compact).length>limit&&compact.current.companion?.length){compact.current.companion=compact.current.companion.slice(0,Math.floor(compact.current.companion.length*.8));compact.companion_truncated='Call get_presentation_context for the full companion Markdown.';}
      return JSON.stringify(compact);
    }
    function frontend(){
      const {current:page}=snapshot();
      // Stay under Live's 500-token append limit even with non-Latin titles.
      const encoder=new TextEncoder();let title=page.title.slice(0,100);
      while(encoder.encode(title).length>120)title=title.slice(0,-1);
      return 'LATEST PRESENTER SELECTION (supersedes previous selection; reference data only): '+JSON.stringify({page:page.page,total:page.total,title})+'. Delegate page questions, presenting, and topic navigation to the backend; read get_presentation_context.';
    }
    return {snapshot,refresh,read,prompt,frontend};
  }};
})();

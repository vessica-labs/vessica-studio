(function(){
  'use strict';
  // Hosts may accept an export as a durable job. Keep preparation independent
  // of a single browser connection; ordinary local responses still return once.
  async function download(path){
    const origin=new URL(location.href).origin,deadline=Date.now()+20*60*1000;
    let url=new URL(path,location.href),failures=0;
    while(Date.now()<deadline){
      if(url.origin!==origin||url.username||url.password)throw new Error('Invalid export status location');
      const controller=new AbortController(),timer=setTimeout(()=>controller.abort(),30000);
      let response;
      try{
        response=await fetch(url.href,{credentials:'same-origin',headers:{Prefer:'respond-async'},signal:controller.signal,redirect:'error'});
        failures=0;
      }catch{
        if(++failures>=3)throw new Error('Export connection interrupted. Please retry.');
        await new Promise(resolve=>setTimeout(resolve,1000*failures));
        continue;
      }finally{clearTimeout(timer);}
      if(response.status!==202)return response;
      const next=response.headers.get('Location');
      await response.body?.cancel();
      if(!next)throw new Error('Invalid export status location');
      url=new URL(next,url);
      if(url.origin!==origin||url.username||url.password)throw new Error('Invalid export status location');
      const seconds=Number(response.headers.get('Retry-After')??1);
      const delay=Number.isFinite(seconds)?Math.min(5000,Math.max(250,seconds*1000)):1000;
      await new Promise(resolve=>setTimeout(resolve,delay));
    }
    throw new Error('Export timed out. Please retry.');
  }
  window.VSTDExportDownload={fetch:download};
})();

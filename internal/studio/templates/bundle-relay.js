// Trusted, engine-generated release shell. The authored deck remains in an
// opaque sandbox. The shell downloads only this release's declared bundles;
// it transfers bytes, never cookies, capability URLs or application credentials.
(()=>{
 const deck=document.getElementById('deck'),assets=/*VSTD:RELAY_ASSETS*/{},pending=new Map();
 // Preserve the authorized viewer selector and slide location when entering the deck.
 deck.src='./presentation.html'+location.search+location.hash;
 addEventListener('message',async event=>{
  if(event.source!==deck.contentWindow||event.origin!=='null')return;
  if(event.data?.type==='vstd-bundle-cancel'){pending.get(event.data.request)?.abort();return;}
  if(!['vstd-bundle-fetch','vstd-bundle-authorize'].includes(event.data?.type))return;
  const {id,request}=event.data,asset=Object.hasOwn(assets,id)?assets[id]:undefined;
  if(!asset||typeof request!=='string'||!/^[a-f0-9-]{36}$/.test(request)||pending.has(request)||pending.size>=2)return;
  const controller=new AbortController();pending.set(request,controller);
  try{
   if(event.data.type==='vstd-bundle-authorize'){
    await window.VSTDBundleDownload.authorize(asset.url,controller.signal);
    deck.contentWindow.postMessage({type:'vstd-bundle-authorized',request},'*');return;
   }
   const bytes=await window.VSTDBundleDownload.download(asset.url,asset,controller.signal,size=>deck.contentWindow.postMessage({type:'vstd-bundle-progress',request,size},'*'));
   deck.contentWindow.postMessage({type:'vstd-bundle-bytes',request,bytes},'*',[bytes.buffer]);
  }catch{deck.contentWindow.postMessage({type:'vstd-bundle-error',request},'*');}
  finally{pending.delete(request);}
 });
 addEventListener('pagehide',()=>{for(const controller of pending.values())controller.abort();});
})();

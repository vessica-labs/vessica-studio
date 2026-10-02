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
  if(event.data?.type!=='vstd-bundle-fetch')return;
  const {id,request}=event.data,asset=Object.hasOwn(assets,id)?assets[id]:undefined;
  if(!asset||typeof request!=='string'||!/^[a-f0-9-]{36}$/.test(request)||pending.has(request)||pending.size>=2)return;
  const controller=new AbortController();pending.set(request,controller);
  try{
   const response=await fetch(asset.url,{credentials:'include',signal:controller.signal});if(!response.ok)throw Error('Application download failed');
   const reader=response.body.getReader(),chunks=[];let size=0;
   for(;;){const r=await reader.read();if(r.done)break;size+=r.value.length;if(size>asset.bytes||size>128*1024*1024){await reader.cancel();throw Error('Application download exceeds manifest');}chunks.push(r.value);deck.contentWindow.postMessage({type:'vstd-bundle-progress',request,size},'*');}
   if(size!==asset.bytes)throw Error('Application download incomplete');
   const bytes=new Uint8Array(size);let offset=0;for(const b of chunks){bytes.set(b,offset);offset+=b.length;}
   deck.contentWindow.postMessage({type:'vstd-bundle-bytes',request,bytes},'*',[bytes.buffer]);
  }catch{deck.contentWindow.postMessage({type:'vstd-bundle-error',request},'*');}
  finally{pending.delete(request);}
 });
 addEventListener('pagehide',()=>{for(const controller of pending.values())controller.abort();});
})();

// Trusted deck/relay only. Cached bytes never grant access: a cache hit first
// checks the current URL with an uncached HEAD and still verifies its digest.
(()=>{
 const limit=128*1024*1024,cacheName='vstd-bundles-v1';
 async function authorize(url,signal){
  const response=await fetch(url,{method:'HEAD',credentials:'include',cache:'no-store',signal});
  if(!response.ok)throw Error('Application asset unavailable');
  return response;
 }
 async function read(response,asset,signal,progress,verify=true){
  if(!response.ok||!response.body||Number(response.headers.get('content-length')||0)>limit){await response.body?.cancel();throw Error('Application download failed');}
  const reader=response.body.getReader(),parts=[];let size=0,percent=-1;
  try{for(;;){if(signal?.aborted)throw new DOMException('Canceled','AbortError');const r=await reader.read();if(r.done)break;size+=r.value.length;if(size>asset.bytes||size>limit)throw Error('Application download exceeds manifest');parts.push(r.value);const next=Math.round(size/asset.bytes*100);if(next!==percent){percent=next;progress?.(size);}}}
  finally{await reader.cancel().catch(()=>{});}
  if(size!==asset.bytes)throw Error('Application download incomplete');
  const bytes=new Uint8Array(size);let offset=0;for(const part of parts){bytes.set(part,offset);offset+=part.length;}
  if(verify)await integrity(bytes,asset.hash);
  return bytes;
 }
 async function integrity(bytes,hash){
  const actual=Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256',bytes)),n=>n.toString(16).padStart(2,'0')).join('');
  if(actual!==hash)throw Error('Application integrity check failed');
 }
 async function parts(url,asset,signal,progress){
  const controller=new AbortController(),abort=()=>controller.abort();
  if(signal?.aborted)throw new DOMException('Canceled','AbortError');
  signal?.addEventListener('abort',abort,{once:true});
  const partSize=4*1024*1024,count=Math.ceil(asset.bytes/partSize),bytes=new Uint8Array(asset.bytes),received=new Array(count).fill(0);
  let next=0,total=0,percent=-1;
  try{
   await Promise.all(Array.from({length:Math.min(4,count)},async()=>{
    for(;;){
     const index=next++;if(index>=count)return;
     const address=new URL(url,location.href);address.searchParams.set('vstd_part',String(index));
     const response=await fetch(address.href,{credentials:'include',signal:controller.signal});
     if(response.status!==200||response.headers.get('x-vstd-bundle-part')!==String(index)){await response.body?.cancel();throw Error('Invalid application part');}
     const expected=Math.min(partSize,asset.bytes-index*partSize);
     const data=await read(response,{bytes:expected},controller.signal,size=>{total+=size-received[index];received[index]=size;const p=Math.round(total/asset.bytes*100);if(p!==percent){percent=p;progress?.(total);}},false);
     bytes.set(data,index*partSize);
    }
   }));
   await integrity(bytes,asset.hash);return bytes;
  }catch(error){controller.abort();throw error;}
  finally{signal?.removeEventListener('abort',abort);}
 }
 async function download(url,asset,signal,progress){
  if(!/^[a-f0-9]{64}$/.test(asset.hash)||!Number.isSafeInteger(asset.bytes)||asset.bytes<1||asset.bytes>limit)throw Error('Invalid application manifest');
  const key=new URL(url,location.href).href;let cache;
  try{cache=await caches.open(cacheName);}catch{/* Opaque origins and storage denial use ordinary delivery. */}
  if(cache){
   let cached;try{cached=await cache.match(key);}catch{}
   if(cached){
    await authorize(url,signal);
    try{return await read(cached,asset,signal,progress);}catch(error){
     if(signal?.aborted)throw error;
     await cache.delete(key).catch(()=>{});
    }
   }
  }
  // Cloud advertises fixed-size, independently cacheable parts. Four bounded
  // requests avoid a single large CDN miss; ordinary servers keep one GET.
  const head=asset.bytes>=8*1024*1024?await authorize(url,signal):null;
  const bytes=head?.headers.get('x-vstd-bundle-part-size')===String(4*1024*1024)
   ?await parts(url,asset,signal,progress)
   :await read(await fetch(url,{credentials:'include',signal}),asset,signal,progress);
  if(cache&&!signal?.aborted){
   try{
    // Bound persistent storage to one archive budget and four entries. URLs
    // retain their release/editor scope; no cross-scope cache lookup occurs.
    let used=asset.bytes,count=1;
    for(const request of (await cache.keys()).reverse()){
     const entry=await cache.match(request),size=Number(entry?.headers.get('content-length'));
     if(request.url===key||!Number.isSafeInteger(size)||size<1||used+size>limit||count>=4)await cache.delete(request);
     else{used+=size;count++;}
    }
    await cache.put(key,new Response(bytes,{headers:{'content-type':'application/zip','content-length':String(bytes.length)}}));
   }catch{/* Storage quotas must not prevent a validated launch. */}
  }
  return bytes;
 }
 window.VSTDBundleDownload={download,authorize};
})();

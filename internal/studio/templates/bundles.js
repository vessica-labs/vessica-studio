/* Interactive bundle v1: inline application code and relative data/image files.
   Archive bytes are fetched only on launch. No credential or delivery URL is
   passed into the opaque-origin application. Leaving a slide destroys it. */
document.addEventListener('DOMContentLoaded',()=>{
  const maxZip=128*1024*1024,maxExpanded=256*1024*1024;
  const safe=p=>p&&p.length<=512&&!/[\\:\x00?#]/.test(p)&&!p.startsWith('/')&&p.split('/').every(s=>s&&s!=='.'&&s!=='..'&&!s.startsWith('.')&&!/[ .]$/.test(s));
  async function unzip(bytes){
    const v=new DataView(bytes.buffer,bytes.byteOffset,bytes.byteLength);
    let end=-1;
    for(let i=bytes.length-22;i>=Math.max(0,bytes.length-65557);i--)if(v.getUint32(i,true)===0x06054b50){end=i;break;}
    if(end<0||v.getUint16(end+4,true)||v.getUint16(end+6,true))throw Error('Unsupported bundle archive');
    const count=v.getUint16(end+10,true),offset=v.getUint32(end+16,true);
    if(!count||count>512)throw Error('Bundle file limit exceeded');
    let cursor=offset,total=0;const files=[],seen=new Set(),decoder=new TextDecoder('utf-8',{fatal:true});
    for(let i=0;i<count;i++){
      if(cursor+46>bytes.length||v.getUint32(cursor,true)!==0x02014b50)throw Error('Invalid bundle index');
      const flags=v.getUint16(cursor+8,true),method=v.getUint16(cursor+10,true),packed=v.getUint32(cursor+20,true),size=v.getUint32(cursor+24,true),n=v.getUint16(cursor+28,true),extra=v.getUint16(cursor+30,true),comment=v.getUint16(cursor+32,true),local=v.getUint32(cursor+42,true);
      const name=decoder.decode(bytes.subarray(cursor+46,cursor+46+n));cursor+=46+n+extra+comment;
      if(name.endsWith('/'))continue;
      if(!safe(name)||seen.has(name.toLowerCase())||flags&1||![0,8].includes(method)||size>maxExpanded||(total+=size)>maxExpanded)throw Error('Unsafe bundle entry');
      seen.add(name.toLowerCase());
      if(local+30>bytes.length||v.getUint32(local,true)!==0x04034b50)throw Error('Invalid bundle data');
      const start=local+30+v.getUint16(local+26,true)+v.getUint16(local+28,true);
      if(start+packed>bytes.length)throw Error('Truncated bundle');
      let data=bytes.slice(start,start+packed);
      if(method===8){
        const reader=new Blob([data]).stream().pipeThrough(new DecompressionStream('deflate-raw')).getReader(),parts=[];let got=0;
        for(;;){const r=await reader.read();if(r.done)break;got+=r.value.length;if(got>size){await reader.cancel();throw Error('Bundle expansion exceeded');}parts.push(r.value);}
        data=new Uint8Array(got);let at=0;for(const p of parts){data.set(p,at);at+=p.length;}
      }
      if(data.length!==size)throw Error('Bundle entry size mismatch');files.push({name,data});
    }
    return {files,total};
  }
  // This bootstrap runs in the child, where Blob URLs inherit its opaque origin.
  function boot(id){
    addEventListener('message',function receive(event){
      if(event.source!==parent||event.data?.type!=='vstd-bundle-init'||event.data.id!==id)return;
      removeEventListener('message',receive);
      const {files,entrypoint}=event.data,urls=new Map(),decoder=new TextDecoder();
      window.VSTDSimulationHost={origin:event.origin};
      const mime=name=>({json:'application/json',jpg:'image/jpeg',jpeg:'image/jpeg',png:'image/png',svg:'image/svg+xml',webp:'image/webp',css:'text/css',woff:'font/woff',woff2:'font/woff2',txt:'text/plain'}[name.split('.').pop()]||'application/octet-stream');
      for(const f of files)urls.set(f.name,URL.createObjectURL(new Blob([f.data],{type:mime(f.name)})));
      const root=new URL(entrypoint,'https://bundle.invalid/');
      const resolve=value=>{try{const u=new URL(String(value),root);return u.origin===root.origin?urls.get(decodeURIComponent(u.pathname.slice(1))):undefined;}catch{return undefined;}};
      const nativeFetch=window.fetch.bind(window);
      window.fetch=(input,init)=>{const value=typeof input==='string'||input instanceof URL?input:input.url;const mapped=resolve(value);return mapped?nativeFetch(mapped,init):Promise.reject(Error('Resource is outside this bundle'));};
      const imageSrc=Object.getOwnPropertyDescriptor(HTMLImageElement.prototype,'src');
      Object.defineProperty(HTMLImageElement.prototype,'src',{...imageSrc,set(value){imageSrc.set.call(this,resolve(value)||value);}});
      const source=files.find(f=>f.name===entrypoint);if(!source)throw Error('Bundle entrypoint missing');
      const doc=new DOMParser().parseFromString(decoder.decode(source.data),'text/html');
      doc.querySelectorAll('[src],[href],[poster]').forEach(el=>{for(const attr of ['src','href','poster']){const value=el.getAttribute(attr);if(value&&resolve(value))el.setAttribute(attr,resolve(value));}});
      const css=value=>value.replace(/url\(\s*(['"]?)([^)'"\s]+)\1\s*\)/g,(all,q,p)=>resolve(p)?'url("'+resolve(p)+'")':all);
      doc.querySelectorAll('style').forEach(el=>el.textContent=css(el.textContent));doc.querySelectorAll('[style]').forEach(el=>el.setAttribute('style',css(el.getAttribute('style'))));
      // No application network, forms, navigation or nested frames. Data stays in memory.
      const policy=doc.createElement('meta');policy.httpEquiv='Content-Security-Policy';policy.content="default-src 'none'; script-src 'unsafe-inline' blob:; style-src 'unsafe-inline' blob:; img-src blob: data:; font-src blob: data:; media-src blob: data:; connect-src blob: data:; frame-src 'none'; worker-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'";doc.head.prepend(policy);
      document.open();document.write('<!doctype html>'+doc.documentElement.outerHTML);document.close();
    });
    parent.postMessage({type:'vstd-bundle-ready',id},'*');
  }
  const running=new Map(),prepared=new Map();let preparedBytes=0;
  function remember(key,value){
    if(value.total>maxExpanded)return;
    if(prepared.has(key)){preparedBytes-=prepared.get(key).total;prepared.delete(key);}
    while(prepared.size && (preparedBytes+value.total>maxExpanded||prepared.size>=4)){
      const oldest=prepared.keys().next().value;preparedBytes-=prepared.get(oldest).total;prepared.delete(oldest);
    }
    prepared.set(key,value);preparedBytes+=value.total;
  }
  function cleanup(host){const state=running.get(host);if(!state)return;state.controller.abort();state.frame?.remove();if(state.listener)removeEventListener('message',state.listener);running.delete(host);host.querySelector('[data-bundle-launch]')?.removeAttribute('disabled');const label=host.querySelector('[data-bundle-status]');if(label)label.textContent='';}
  async function launch(host){
    if(running.has(host)||!host.closest('.slide.active')||host.closest('.mini'))return;
    const asset=(window.VSTD?.bundles||[]).find(a=>a.id===host.dataset.vstdBundle),button=host.querySelector('[data-bundle-launch]'),status=host.querySelector('[data-bundle-status]');
    if(!asset){if(status)status.textContent='Application asset unavailable';return;}
    const controller=new AbortController(),state={controller};running.set(host,state);if(button)button.disabled=true;
    try{
      const logical='/assets/bundle/'+asset.id;
      const url=location.protocol==='file:'?'assets/bundle/'+asset.id+'.zip':window.VSTDAssetURL?window.VSTDAssetURL(logical):logical;
      if(status)status.textContent='Loading simulation…';
      if(asset.bytes>maxZip)throw Error('Bundle exceeds download limit');
      const key=[asset.hash,asset.entrypoint,asset.expandedBytes,asset.fileCount].join(':');
      let cached=prepared.get(key);
      const relay=authorizeOnly=>new Promise((resolve,reject)=>{
          const request=crypto.randomUUID(),timeout=setTimeout(()=>done(Error('Application download timed out')),120000);
          const done=(error,value)=>{clearTimeout(timeout);removeEventListener('message',receive);controller.signal.removeEventListener('abort',abort);error?reject(error):resolve(value);};
          const abort=()=>{parent.postMessage({type:'vstd-bundle-cancel',request},'*');done(Error('Application download canceled'));};
          const receive=event=>{if(event.source!==parent||event.data?.request!==request)return;
            if(authorizeOnly && event.data.type==='vstd-bundle-authorized')done();
            else if(!authorizeOnly && event.data.type==='vstd-bundle-bytes' && event.data.bytes instanceof Uint8Array)done(null,event.data.bytes);
            else if(event.data.type==='vstd-bundle-error')done(Error('Application download failed'));
            else if(event.data.type==='vstd-bundle-progress' && status)status.textContent='Loading simulation… '+Math.min(100,Math.round(event.data.size/asset.bytes*100))+'%';
          };
          addEventListener('message',receive);controller.signal.addEventListener('abort',abort,{once:true});parent.postMessage({type:authorizeOnly?'vstd-bundle-authorize':'vstd-bundle-fetch',id:asset.id,request},'*');
        });
      if(cached){
        if(window.VSTDBundleRelay)await relay(true);
        else await window.VSTDBundleDownload.authorize(url,controller.signal);
        prepared.delete(key);prepared.set(key,cached);
      }else{
        const bytes=window.VSTDBundleRelay?await relay(false):await window.VSTDBundleDownload.download(url,asset,controller.signal,size=>{if(status)status.textContent='Loading simulation… '+Math.min(100,Math.round(size/asset.bytes*100))+'%';});
        const digest=Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256',bytes)),n=>n.toString(16).padStart(2,'0')).join('');
        if(bytes.length!==asset.bytes||digest!==asset.hash)throw Error('Application integrity check failed');
        if(status)status.textContent='Preparing simulation…';
        cached=await unzip(bytes);
        if(cached.total!==asset.expandedBytes||cached.files.length!==asset.fileCount||!cached.files.some(f=>f.name===asset.entrypoint))throw Error('Application inventory mismatch');
        if(!controller.signal.aborted)remember(key,cached);
      }
      if(controller.signal.aborted||!host.closest('.slide.active'))return;
      const frame=document.createElement('iframe'),id=crypto.randomUUID();state.frame=frame;frame.title=host.getAttribute('aria-label')||'Interactive application';frame.setAttribute('sandbox','allow-scripts');frame.setAttribute('referrerpolicy','no-referrer');frame.style.cssText='position:absolute;inset:0;width:100%;height:100%;border:0;background:#0b1016';
      state.listener=event=>{if(event.source!==frame.contentWindow||event.origin!=='null'||event.data?.type!=='vstd-bundle-ready'||event.data.id!==id)return;removeEventListener('message',state.listener);const files=cached.files.map(f=>({name:f.name,data:f.data.slice()}));frame.contentWindow.postMessage({type:'vstd-bundle-init',id,files,entrypoint:asset.entrypoint},'*',files.map(f=>f.data.buffer));if(status)status.textContent='';};
      addEventListener('message',state.listener);frame.srcdoc='<script>('+boot.toString()+')('+JSON.stringify(id)+');<'+ '/script>';host.append(frame);
    }catch(error){if(controller.signal.aborted)return;cleanup(host);if(status)status.textContent=error.message+' — try again';}
  }
  document.addEventListener('click',event=>{const button=event.target.closest('[data-bundle-launch]');if(button){event.preventDefault();launch(button.closest('[data-vstd-bundle]'));}});
  // Presenter tools use the same validated loader and opaque application frame.
  window.VSTDBundles={launch,frame:host=>running.get(host)?.frame||null};
  new MutationObserver(()=>{for(const host of running.keys())if(!host.isConnected||!host.closest('.slide.active'))cleanup(host);}).observe(document.getElementById('frame'),{subtree:true,attributes:true,attributeFilter:['class'],childList:true});
});

package studio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vessica-labs/vessica-studio/internal/chromium"
)

func TestBundleBrowserCachePersistsAndRequiresCurrentAccess(t *testing.T) {
	browser := chromium.Find("")
	if browser == "" {
		t.Skip("Chrome unavailable")
	}
	source, err := templates.ReadFile("templates/bundle-download.js")
	if err != nil {
		t.Fatal(err)
	}
	bytes := []byte("browser cached immutable bundle")
	hash := sha256.Sum256(bytes)
	asset, _ := json.Marshal(map[string]any{"hash": hex.EncodeToString(hash[:]), "bytes": len(bytes)})
	var downloads, checks, pages atomic.Int32
	var revoked atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		switch r.URL.Path {
		case "/bundle.zip":
			if r.Method == "HEAD" {
				checks.Add(1)
			} else {
				downloads.Add(1)
			}
			if revoked.Load() {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Length", fmt.Sprint(len(bytes)))
			if r.Method != "HEAD" {
				w.Write(bytes)
			}
		case "/revoke":
			revoked.Store(true)
			w.WriteHeader(204)
		default:
			pages.Add(1)
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<script>%s</script><script>
(async()=>{
 const asset=%s,loader=window.VSTDBundleDownload,signal=new AbortController().signal;
 try{
  const bytes=await loader.download('/bundle.zip',asset,signal);
  if(!sessionStorage.phase){sessionStorage.phase='reload';location.reload();return;}
  const persisted=new TextDecoder().decode(bytes)==='browser cached immutable bundle';
  const cache=await caches.open('vstd-bundles-v1');
  await cache.put(new URL('/bundle.zip',location.href),new Response('corrupt'));
  const recovered=new TextDecoder().decode(await loader.download('/bundle.zip',asset,signal))==='browser cached immutable bundle';
  await fetch('/revoke',{method:'POST'});let denied=false;
  try{await loader.download('/bundle.zip',asset,signal);}catch{denied=true;}
  window.__bundleCacheResult={persisted,recovered,denied};
 }catch(e){window.__bundleCacheResult={error:String(e)}}
})();</script>`, source, asset)
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	raw, err := chromium.Evaluate(ctx, browser, srv.URL+"/", `window.__bundleCacheResult?JSON.stringify(window.__bundleCacheResult):''`)
	if err != nil {
		t.Fatalf("%v downloads=%d checks=%d pages=%d", err, downloads.Load(), checks.Load(), pages.Load())
	}
	var result struct {
		Persisted, Recovered, Denied bool
		Error                        string
	}
	if err = json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Persisted || !result.Recovered || !result.Denied || downloads.Load() != 2 || checks.Load() != 3 {
		t.Fatalf("cache result %+v downloads=%d checks=%d", result, downloads.Load(), checks.Load())
	}
}

func TestBundleCacheStorageFailureAndDownloadBounds(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	source, err := templates.ReadFile("templates/bundle-download.js")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(string(source))
	script := `const assert=require('node:assert/strict'),vm=require('node:vm'),{webcrypto,createHash}=require('node:crypto');
const bytes=Buffer.from('validated'),asset={bytes:bytes.length,hash:createHash('sha256').update(bytes).digest('hex')};
let deleted=0,gets=0;const cache={match:async key=>String(key).includes('old')?new Response('',{headers:{'content-length':String(128*1024*1024)}}):undefined,keys:async()=>[new Request('https://example.test/old')],delete:async()=>{deleted++;return true},put:async()=>{throw Error('quota')}};
const window={},scope={window,location:{href:'https://example.test/'},URL,Response,DOMException,AbortController,Uint8Array,Number,Array,crypto:webcrypto,caches:{open:async()=>cache},fetch:async()=>{gets++;return new Response(bytes)}};
vm.runInNewContext(` + string(encoded) + `,scope);
(async()=>{
 assert.deepEqual(Buffer.from(await window.VSTDBundleDownload.download('/bundle',asset)),bytes,'quota denial must allow a verified launch');assert.equal(deleted,1,'evict before exceeding persistent budget');
 scope.fetch=async()=>new Response('',{headers:{'content-length':String(129*1024*1024)}});
 await assert.rejects(window.VSTDBundleDownload.download('/bundle',asset),/download failed/);
 scope.fetch=async()=>new Response(Buffer.concat([bytes,bytes]));
 await assert.rejects(window.VSTDBundleDownload.download('/bundle',asset),/exceeds manifest/);
 scope.fetch=async()=>new Response('tampered');
 await assert.rejects(window.VSTDBundleDownload.download('/bundle',asset),/integrity|incomplete/);
 scope.caches.open=async()=>{throw Error('storage disabled')};scope.fetch=async()=>new Response(bytes);
 assert.deepEqual(Buffer.from(await window.VSTDBundleDownload.download('/bundle',asset)),bytes);
 const large=Buffer.alloc(9*1024*1024+17,23),largeAsset={bytes:large.length,hash:createHash('sha256').update(large).digest('hex')};
 let active=0,maximum=0;const indices=[],progress=[];
 scope.fetch=async(url,options)=>{
  if(options.method==='HEAD')return new Response('',{headers:{'x-vstd-bundle-part-size':String(4*1024*1024)}});
  const index=Number(new URL(url).searchParams.get('vstd_part'));indices.push(index);active++;maximum=Math.max(maximum,active);
  await new Promise(resolve=>setTimeout(resolve,(3-index)*10));active--;
  return new Response(large.subarray(index*4*1024*1024,Math.min((index+1)*4*1024*1024,large.length)),{headers:{'x-vstd-bundle-part':String(index)}});
 };
 assert.deepEqual(Buffer.from(await window.VSTDBundleDownload.download('/large',largeAsset,undefined,size=>progress.push(size))),large,'out-of-order parallel parts must reconstruct the declared archive');
 assert.deepEqual(indices,[0,1,2]);assert.equal(maximum,3);assert.equal(progress.at(-1),large.length);
 assert(progress.every((size,index)=>index===0||size>=progress[index-1]));
 scope.fetch=async(url,options)=>options.method==='HEAD'?new Response('',{headers:{'x-vstd-bundle-part-size':String(4*1024*1024)}}):new Response('wrong',{headers:{'x-vstd-bundle-part':'99'}});
 await assert.rejects(window.VSTDBundleDownload.download('/large',largeAsset),/Invalid application part/);
})().catch(e=>{console.error(e);process.exitCode=1});`
	if output, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", output, err)
	}
}

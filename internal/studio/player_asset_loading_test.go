package studio

import (
	"os/exec"
	"strings"
	"testing"
)

func TestPlayerPromotesAlreadyHydratedImages(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	player, err := templates.ReadFile("templates/player.html")
	if err != nil {
		t.Fatal(err)
	}
	source := string(player)
	start := strings.Index(source, "/* ================= Progressive slide assets")
	end := strings.Index(source[start:], "/* ================= Video assets layer")
	script := `
const assert=require('node:assert/strict');
const attrs={'data-vstd-src':'/library/example.png'};
let loading,fetchPriority,decoding;
const img={tagName:'IMG',get loading(){return loading},set loading(v){loading=v},get fetchPriority(){return fetchPriority},set fetchPriority(v){fetchPriority=v},set decoding(v){decoding=v},
 hasAttribute(k){return k in attrs},getAttribute(k){return attrs[k]}, removeAttribute(k){delete attrs[k]},
 setAttribute(k,v){if(k==='src'){assert.equal(fetchPriority,'low');assert.equal(loading,'lazy')}attrs[k]=v;}};
const slide={classList:{contains(){return false}},querySelectorAll(selector){if(selector==='img')return [img];if(selector==='[data-vstd-srcset],[data-vstd-src]'&&attrs['data-vstd-src'])return [img];return []}};
global.window={};global.document={getElementById(){return {querySelectorAll(){return []}}},addEventListener(){}};global.addEventListener=()=>{};
` + source[start:start+end] + `
window.__vhydrateAssets(slide,'low');
assert.equal(attrs.src,'/library/example.png');assert.equal(attrs['data-vstd-src'],undefined);
window.__vhydrateAssets(slide,'high');
assert.equal(loading,'eager');assert.equal(fetchPriority,'high');assert.equal(decoding,'async');
`
	if output, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", output, err)
	}
}

func TestPlayerWarmsHiddenImagesWithBoundedQueue(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	player, err := templates.ReadFile("templates/player.html")
	if err != nil {
		t.Fatal(err)
	}
	source := string(player)
	start := strings.Index(source, "/* ================= Progressive slide assets")
	end := strings.Index(source[start:], "/* ================= Video assets layer")
	script := `
const assert=require('node:assert/strict');
const pending=[],requests=[],listeners={},timers=[];
class Img extends EventTarget {
 constructor(src){super();this.tagName='IMG';this.attrs={};this.complete=false;if(src)this.attrs['data-vstd-src']=src;}
 hasAttribute(k){return k in this.attrs}getAttribute(k){return this.attrs[k]}removeAttribute(k){delete this.attrs[k]}
 closest(){return null}
 setAttribute(k,v){this.attrs[k]=v;if(k==='src'){assert.equal(this.loading,'eager');requests.push(v);pending.push(this)}}
}
global.Image=Img;global.window={};global.getComputedStyle=el=>({backgroundImage:el.background||'none'});
global.setTimeout=(fn,ms)=>{if(ms===800)timers.push(fn);return 1};global.clearTimeout=()=>{};
const images=Array.from({length:5},(_,i)=>new Img('/image-'+i+'.webp'));
const all=images.map(img=>({tagName:'SECTION',classList:{contains:()=>false},querySelectorAll(selector){if(selector==='img'||selector==='*')return [img];if(selector==='[data-vstd-srcset],[data-vstd-src]')return img.hasAttribute('data-vstd-src')?[img]:[];return []}}));
all[4].background='url("/background.webp")';
global.document={readyState:'loading',getElementById:()=>({querySelectorAll:()=>all}),addEventListener:(k,fn)=>listeners[k]=fn};
global.addEventListener=(k,fn)=>listeners[k]=fn;
` + source[start:start+end] + `
(async()=>{
 // Visible first slide is immediate; all other slides remain pending until load.
 assert.deepEqual(requests,['/image-0.webp']);pending.shift().complete=true;
 listeners.load();timers.shift()();
 const settle=async()=>{for(let i=0;i<12;i++)await Promise.resolve()};
 await settle();assert.equal(pending.length,2);
 // Navigation must not discard queued CSS backgrounds or multiply in-flight jobs.
 listeners['vstd:slide']();await settle();assert.equal(pending.length,2);
 for(let i=0;i<8&&pending.length;i++){
   const img=pending.shift();img.complete=true;img.dispatchEvent(new Event('load'));
   await settle();assert(pending.length<=2,'unbounded preload');
 }
 assert.equal(new Set(requests).size,6);assert(requests.includes('/image-4.webp'));assert(requests.includes('/background.webp'));
})().catch(error=>{console.error(error);process.exitCode=1});
`
	if output, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", output, err)
	}
}

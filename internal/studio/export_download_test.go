package studio

import (
	"encoding/json"
	"os/exec"
	"testing"
)

func TestExportDownloadPollsOnlySameOriginAndRecoversReadOnlyTransfers(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	source, err := templates.ReadFile("templates/export-download.js")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(string(source))
	script := `const assert=require('node:assert/strict'),vm=require('node:vm');
let now=0,requests=[],responses=[];
const window={},scope={window,location:{href:'https://example.test/d/demo/'},URL,Response,AbortController,Date:{now:()=>now},setTimeout:(fn,ms)=>{if(ms===30000)return -1;now+=ms;queueMicrotask(fn);},clearTimeout:()=>{},fetch:async(url,options)=>{requests.push({url:String(url),options});const r=responses.shift();if(r instanceof Error)throw r;return r;}};
vm.runInNewContext(` + string(encoded) + `,scope);
(async()=>{
 const download=window.VSTDExportDownload.fetch;
 responses=[new Response('pdf')];assert.equal(await(await download('/export.pdf')).text(),'pdf');
 assert.equal(requests[0].options.headers.Prefer,'respond-async');assert.equal(requests[0].options.credentials,'same-origin');
 requests=[];responses=[new Response('{}',{status:202,headers:{Location:'/export.pdf?job=1','Retry-After':'1'}}),new Response('{}',{status:202,headers:{Location:'/export.pdf?job=1','Retry-After':'9999'}}),new Response('completed')];
 assert.equal(await(await download('/export.pdf')).text(),'completed');assert.equal(requests.length,3);assert.equal(requests[1].url,'https://example.test/export.pdf?job=1');assert.equal(now,6000);
 responses=[new Error('network'),new Response('retried')];assert.equal(await(await download('/export.pdf')).text(),'retried');
 responses=[new Error('network'),new Error('network'),new Error('network')];await assert.rejects(download('/export.pdf'),/connection interrupted/);
 responses=[new Response('',{status:503}),new Response('recovered')];assert.equal(await(await download('/export.pdf')).text(),'recovered');
 responses=Array.from({length:3},()=>new Response('unavailable',{status:503}));assert.equal((await download('/export.pdf')).status,503);
 for(const location of ['https://other.test/file','https://user:pass@example.test/file','javascript:alert(1)']){responses=[new Response('{}',{status:202,headers:{Location:location}})];await assert.rejects(download('/export.pdf'),/Invalid export status location/);}
 responses=[new Response('{}',{status:202})];await assert.rejects(download('/export.pdf'),/Invalid export status location/);
 responses=[new Response('denied',{status:403})];assert.equal((await download('/export.pdf')).status,403);
 let polls=0;scope.fetch=async()=>{polls++;return new Response('{}',{status:202,headers:{Location:'/export.pdf?job=1','Retry-After':'5'}});};await assert.rejects(download('/export.pdf'),/timed out/);assert(polls<=241);
})().catch(e=>{console.error(e);process.exitCode=1});`
	if output, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", output, err)
	}
}

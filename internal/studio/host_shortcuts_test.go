package studio

import (
	"os/exec"
	"strings"
	"testing"
)

func TestPlayerHostShortcuts(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	player, err := templates.ReadFile("templates/player.html")
	if err != nil {
		t.Fatal(err)
	}
	source := string(player)
	start := strings.Index(source, "  // ---------- Optional host keyboard shortcuts (v1) ----------")
	end := strings.Index(source, "  // ---------- End host keyboard shortcuts ----------")
	if start < 0 || end <= start {
		t.Fatal("host shortcut protocol missing")
	}
	script := `const assert=require('node:assert/strict');
 const handlers={},sent=[];const parent={postMessage:(value,origin)=>sent.push({value,origin})};
 global.window={parent,addEventListener:(name,fn)=>handlers[name]=fn};
 global.document={activeElement:null,addEventListener:(name,fn)=>handlers[name]=fn};
 ` + source[start:end] + `
 const key=(key,extra={})=>{const event={key,prevented:false,stopped:false,preventDefault(){this.prevented=true},stopImmediatePropagation(){this.stopped=true},...extra};handlers.keydown(event);return event;};
 const configure=(source=parent,origin='https://host.example.test',enabled=true)=>handlers.message({source,origin,data:{type:'vstd:host-shortcuts',version:1,enabled}});
 assert.equal(key('v',{ctrlKey:true}).prevented,false,'standalone behavior is unchanged before opt in');
 configure({});configure(parent,'null');assert.equal(key('v',{ctrlKey:true}).prevented,false,'invalid parent or opaque origin is ignored');
 configure();assert.equal(key('v',{ctrlKey:true}).prevented,true);assert.equal(sent.at(-1).value.command,'agent');assert.equal(sent.at(-1).origin,'https://host.example.test');
 assert.deepEqual(Object.keys(sent.at(-1).value).sort(),['command','type','version']);
 const editable={closest:()=>({})};assert.equal(key('v',{ctrlKey:true,target:editable}).prevented,false,'paste remains native in editable fields');
 for(const modifier of ['repeat','isComposing','altKey','shiftKey'])assert.equal(key('v',{ctrlKey:true,[modifier]:true}).prevented,false);
 assert.equal(key('v',{metaKey:true}).prevented,false,'normal macOS paste is unchanged');
 assert.equal(key('k',{metaKey:true,target:editable}).prevented,true);assert.equal(sent.at(-1).value.command,'search');
 configure(parent,'https://host.example.test',false);assert.equal(key('v',{ctrlKey:true}).prevented,false,'host can detach');
 configure();window.parent=window;assert.equal(key('v',{ctrlKey:true}).prevented,false,'top-level player cannot relay shortcuts');
 `
	if output, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
}

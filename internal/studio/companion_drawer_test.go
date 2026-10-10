package studio

import (
	"os/exec"
	"strings"
	"testing"
)

func TestCompanionDrawerPreservesSaveBaseline(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	player, err := templates.ReadFile("templates/player.html")
	if err != nil {
		t.Fatal(err)
	}
	source := string(player)
	start := strings.Index(source, "/* ============ companion Markdown drawer")
	end := strings.Index(source, "/* ============ background-work progress")
	if start < 0 || end <= start {
		t.Fatal("companion drawer missing")
	}
	script := `
const assert=require('node:assert/strict');global.window=global;global.location={protocol:'http:'};
const nodes=new Map();const node=key=>{if(!nodes.has(key))nodes.set(key,{value:'',style:{},classList:{add(){},remove(){},toggle(){}},listeners:{},addEventListener(name,fn){this.listeners[name]=fn;},appendChild(){},focus(){},querySelector:selector=>node(key+selector)});return nodes.get(key);};
const drawer=node('drawer');global.document={getElementById:()=>drawer,querySelector:()=>({dataset:{vstd:'0010-cover'}}),createElement:()=>node('created'),addEventListener(){}};
global.VSTD={deck:'demo'};global.VSTDPresenterControl=()=>true;global.__vme={editable:true};
let stored='## Talk track\nOriginal saved narrative\n',refreshes=0;const writes=[];
global.fetch=async()=>({ok:true,json:async()=>({companion:stored,attachments:[]})});
global.__vreconf=()=>refreshes++;
global.__vwrite=async(path,options)=>{const body=JSON.parse(options.body);writes.push(body);assert.equal(body.base,stored,'save base must remain the last confirmed Markdown');stored=body.markdown;return {ok:true,json:async()=>({markdown:stored,hash:'saved'})};};
` + source[start:end] + `
(async()=>{
 await __companionDrawer.run('set_companion_section',{section:'Talk track',text:'Voice edited narrative'});
 assert.match(stored,/Voice edited narrative/);assert.equal(refreshes,1);
 const textarea=drawer.querySelector('textarea');textarea.value='## Talk track\nTyped narrative\n';textarea.listeners.input();
 await __companionDrawer.save();assert.match(stored,/Typed narrative/);assert.equal(refreshes,2);
 assert.equal(writes.length,2);
})().catch(error=>{console.error(error);process.exit(1);});`
	if output, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
}

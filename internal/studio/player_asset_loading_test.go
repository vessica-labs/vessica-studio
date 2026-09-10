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
 getAttribute(k){return attrs[k]}, removeAttribute(k){delete attrs[k]},
 setAttribute(k,v){if(k==='src'){assert.equal(fetchPriority,'low');assert.equal(loading,'lazy')}attrs[k]=v;}};
const slide={classList:{contains(){return false}},querySelectorAll(selector){if(selector==='img')return [img];if(selector==='[data-vstd-src]'&&attrs['data-vstd-src'])return [img];return []}};
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

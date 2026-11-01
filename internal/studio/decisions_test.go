package studio

import (
	"os/exec"
	"testing"
)

func TestPresentationDecisionsOwnershipAndFreshness(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	source, err := templates.ReadFile("templates/decisions.js")
	if err != nil {
		t.Fatal(err)
	}
	script := `global.window=global;` + string(source) + `
const assert=require('node:assert/strict');
let page=1,presenter=true,now=0,timerId=0;const timers=new Map(),actions=[],reports=[];
const pages=[{id:'a',page:1,title:'Intro'},{id:'parked',page:0,title:'Parked'},{id:'b',page:2,title:'Costs'},{id:'c',page:3,title:'Research'}];
const control=VSTDDecisions.create({snapshot:()=>({current:pages.find(p=>p.page===page),pages}),targets:()=>['Revenue chart','Operating costs'],authorized:()=>presenter,
 execute:(name,args)=>{actions.push([name,args]);if(name==='goto_slide')page=args.number;return JSON.stringify({page});},report:r=>reports.push(r),
 now:()=>now,setTimer:fn=>{timers.set(++timerId,fn);return timerId;},clearTimer:id=>timers.delete(id)});
const flush=()=>{now+=250;const work=[...timers.values()];timers.clear();work.forEach(fn=>fn());};
const configure=()=>{const triggers=control.configuration();control.observe({type:'session.updated',session:{triggers:triggers.map(t=>({...t,id:'trigger'}))}});return triggers;};
let config=configure();assert.equal(config.length,1);assert(!JSON.stringify(config).includes('Parked'));
const input=(id,text,start=now)=>control.observe({type:'session.input_transcript.delta',event_id:id,delta:text,start_ms:start,end_ms:start+100});
const decision=(source,nav='none',highlight='none',configuration=config)=>control.observe({type:'decision.event',trigger_id:'trigger',source_event_type:'session.input_transcript.delta',source_event_id:source,event:{type:'decision.completed',decision:{answers:configuration[0].decisions.questions.map(q=>({type:'choice',name:q.name,choice:q.name.includes('navigation')?(nav==='next'||nav==='previous'?q.choices.find(c=>c.description?.includes('['+nav+']'))?.value||'none':nav):q.name.includes('highlight')?highlight:'yes'}))}}});
const delegate=(id,offset)=>control.observe({type:'session.delegation.created',delegation:{id,target:'responses'},offset_ms:offset});
input('first','Next slide.',0);delegate('d1',100);decision('first','next');flush();assert.deepEqual(actions,[['goto_slide',{number:2}]]);
control.backend('next_slide',{},'d1',()=>{throw Error('duplicate advance');});decision('first','next');flush();assert.equal(actions.length,1);
let mixedHighlight=false;control.backend('highlight',{phrase:'Operating costs'},'d1',()=>{mixedHighlight=true;return 'highlighted';});assert(mixedHighlight,'the backend can finish highlighting on the page selected by Decisions');
config=configure();input('repeat','Next slide.',1200);delegate('d2',1300);
control.backend('next_slide',{},'d2',()=>{actions.push(['backend-next']);page=3;return 'page three';});decision('repeat','next');flush();assert.equal(actions.length,2,'backend-first must suppress Decisions');
config=configure();input('old','Previous slide.',2400);delegate('old-backend',2500);decision('old','previous');
page=1;config=configure();flush();assert.equal(actions.length,2,'manual navigation cancels pending decisions');
control.backend('prev_slide',{},'old-backend',()=>{throw Error('stale backend');});
input('partial','Next slide',3600);decision('partial','next');input('correction',' — actually stay here.',3700);flush();assert.equal(actions.length,2,'correction cancels pending action');
input('highlight','Highlight the revenue chart.',4800);delegate('dh',4900);decision('highlight','none','h0');flush();assert.deepEqual(actions.at(-1),['highlight',{phrase:'Revenue chart'}]);
control.backend('highlight',{phrase:'Revenue chart'},'dh',()=>{throw Error('duplicate highlight');});
const count=actions.length;input('bad','Highlight the revenue chart.',6000);decision('bad','none','invented');flush();assert.equal(actions.length,count,'unknown choices must not execute');
input('audience','Next slide.',7200);decision('audience','next');presenter=false;flush();assert.equal(actions.length,count,'authorization is checked at execution');presenter=true;
input('latest','Next slide.',8400);decision('audience','next');flush();assert.equal(actions.length,count,'older input cannot override latest input');
input('research','Research this company.',9600);decision('research','next');flush();assert.equal(actions.length,count,'unrelated speech cannot navigate');
control.backend('deep_think',{task:'Research this company'},'research',()=>{actions.push(['thinking']);return 'done';});assert.deepEqual(actions.at(-1),['thinking']);
input('quoted','He said “next slide”.',10800);decision('quoted','next');flush();assert.equal(actions.length,count+1,'quoted commands are not controls');
input('negated',"Don't go to the next slide.",12000);decision('negated','next');flush();assert.equal(actions.length,count+1,'negated commands cannot navigate');
input('late','Next slide.',13200);delegate('late-delegation',13300);decision('late','next');
input('changed','Actually go to page three.',13300);delegate('changed-delegation',13400);flush();
control.backend('next_slide',{},'late-delegation',()=>{throw Error('corrected backend request');});
control.backend('goto_slide',{number:3},'changed-delegation',()=>{actions.push(['corrected']);return 'page three';});
assert.deepEqual(actions.at(-1),['corrected']);
config=configure();input('low-confidence','Next slide.',15000);
const low={type:'decision.event',source_event_type:'session.input_transcript.delta',source_event_id:'low-confidence',event:{type:'decision.completed',decision:{answers:config[0].decisions.questions.map(q=>({type:'choice',name:q.name,choice:q.name.includes('navigation')?q.choices.find(c=>c.description?.includes('[next]'))?.value||'none':'none',confidence:0.1}))}}};
control.observe(low);flush();assert.deepEqual(actions.at(-1),['corrected'],'low confidence defers to the agent');
input('wrong-number','Go to page 3.',16200);decision('wrong-number','p:b');flush();assert.deepEqual(actions.at(-1),['corrected'],'a decision cannot override an explicit page number');
control.close();decision('latest','next');flush();assert.equal(actions.length,count+2);
assert(reports.length>0);
// A failed fast execution leaves the original backend able to complete the action.
let fallback=false;const broken=VSTDDecisions.create({snapshot:()=>({current:pages[0],pages}),targets:()=>[],authorized:()=>true,execute:()=>{throw Error('DOM changed');},report:()=>{throw Error('must not report failed actions as confirmed');},now:()=>now,setTimer:fn=>{timers.set(++timerId,fn);return timerId;},clearTimer:id=>timers.delete(id)});
const brokenConfig=broken.configuration();broken.observe({type:'session.updated',session:{triggers:brokenConfig}});
broken.observe({type:'session.input_transcript.delta',event_id:'broken',delta:'Next slide.',start_ms:17000,end_ms:17100});
broken.observe({type:'session.delegation.created',offset_ms:17100,delegation:{id:'broken-delegation',target:'responses'}});
broken.observe({type:'decision.event',source_event_type:'session.input_transcript.delta',source_event_id:'broken',event:{type:'decision.completed',decision:{answers:brokenConfig[0].decisions.questions.map(q=>({type:'choice',name:q.name,choice:q.name.includes('navigation')?'p:b':'none'}))}}});flush();
broken.backend('next_slide',{},'broken-delegation',()=>{fallback=true;return 'advanced';});assert(fallback);broken.close();
const empty=VSTDDecisions.create({snapshot:()=>({current:{id:'parked',page:0},pages:[]}),targets:()=>[],authorized:()=>true,execute:()=>{throw Error('no pages');}});assert.deepEqual(empty.configuration(),[]);empty.close();

`
	if output, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
}

// Capture one HTML slide at a time at the same dimensions as native PowerPoint.
import { mkdir, writeFile } from 'node:fs/promises';
import { resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { parseArgs } from 'node:util';
const { values } = parseArgs({options:{url:{type:'string'},output:{type:'string'},chrome:{type:'string'},playwright:{type:'string'},help:{type:'boolean'}}});
if (values.help || !values.url || !values.output) {
 console.log('node capture.mjs --url <print.html URL> --output <directory> [--chrome <binary>] [--playwright <module path>]');
 process.exit(values.help ? 0 : 1);
}
const { chromium } = await import(values.playwright ? pathToFileURL(resolve(values.playwright)).href : 'playwright');
const output=resolve(values.output);await mkdir(output,{recursive:true});
const browser=await chromium.launch({headless:true,...(values.chrome?{executablePath:values.chrome}:{})});
try {
 const page=await browser.newPage({viewport:{width:1280,height:720},deviceScaleFactor:1});
 await page.goto(values.url,{waitUntil:'load'});
 await page.evaluate(async()=>{await document.fonts.ready;await Promise.all([...document.images].map(image=>image.decode().catch(()=>{})));});
 const slides=page.locator('.vstd-page'),count=await slides.count();
 if (!count) throw Error('No print slides found');
 const rows=[];
 for (let n=0;n<count;n++) {
  // Isolate pages. Scrolling a long print document can capture compositor
  // artifacts or a neighbouring page near the viewport boundary.
  await page.evaluate(index=>{[...document.querySelectorAll('.vstd-page')].forEach((element,i)=>{element.style.display=i===index?'block':'none';element.style.margin='0';});document.body.style.margin='0';window.scrollTo(0,0);},n);
  const slide=slides.nth(n),box=await slide.boundingBox();
  if (!box || Math.abs(box.width-1280)>.1 || Math.abs(box.height-720)>.1) throw Error(`Slide ${n+1} has unexpected dimensions`);
  await slide.screenshot({path:join(output,`html-${String(n+1).padStart(2,'0')}.png`),animations:'disabled'});
  rows.push(await slide.evaluate(element=>({id:element.querySelector('.slide').dataset.vstd,title:element.innerText.split('\n').find(line=>line.trim())||''})));
 }
 await writeFile(join(output,'slides.json'),JSON.stringify(rows,null,2));
 console.log(JSON.stringify({slides:count,width:1280,height:720,output}));
} finally { await browser.close(); }

// Real dashboard fixture; screenshots require explicit WS_CONVO_SHOTS opt-in.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const {chromium} = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base, fixture, shots] = process.argv.slice(2);
const live = JSON.parse(fs.readFileSync(fixture, 'utf8'));
(async () => {
 const browser = await chromium.launch({headless:true});
 try {
  for (const width of [1200,760]) for (const theme of ['dark','light']) {
   const context = await browser.newContext({viewport:{width,height:1000}, colorScheme:theme});
   const errors=[];
   let approvals=[{id:'fixture-allow',tool:'Bash',input:{command:'go test ./internal/wsdashboard'},description:'Run the dashboard tests',tool_use_id:'grammar-approval-call',started_at:Date.now()}, {id:'fixture-pending',tool:'Read',input:{file_path:'dashboard-conversation.css'},description:'Read the conversation styles',started_at:Date.now()+1}];
   await context.route('**/agents/livesession', route=>route.fulfill({json:{id:'livesession',status:'running',can_control:true,approvals}}));
   await context.route('**/agents/livesession/approvals/*', async route=>{
    approvals=approvals.filter(a=>a.id!==route.request().url().split('/').pop());
    await route.fulfill({json:{id:'livesession',status:'running',can_control:true,approvals}});
   });
   await context.route('**/s/livesession?frame=1*', async route=>{
    const response=await route.fetch();
    let body=await response.text();
    body=body.replace('<html ', '<html data-hosted="1" ');
    if(!body.includes('id="apbox"')) body=body.replace('<div id="convo" class="convo">','<div id="convo" class="convo"><div class="apbox" id="apbox" hidden></div>');
    await route.fulfill({response,body});
   });
   const page=await context.newPage();
   page.on('pageerror', e=>errors.push(e.message));
   await page.goto(base+'/s/livesession');
   const frame = await (await page.locator('iframe.sframe.on').elementHandle()).contentFrame();
   await frame.locator('.mailline').first().waitFor();
   await frame.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
   await frame.locator('.boundary-badge.au-sysrow').waitFor({timeout:5000});
   await frame.locator('[data-event-id="grammar-hook"]').first().waitFor({state:'attached',timeout:5000});
   assert.equal(await frame.locator('.centries > [data-event-id="grammar-progress"]').count(),0,'tool progress never stands alone');
   assert(await frame.locator('.conv-audit-items [data-event-id="grammar-hook"]').count()>0,'hook belongs in an expansion');
   for(const sel of ['.mailline.sent','.mailline.recv','.child-report','.agent-note']) {
    assert(await frame.locator(sel+'.turn-trigger').count()>0,sel+' has turn-trigger accent');
   }
   const count=await frame.locator('.conv-row').count();
   assert(count>0,'real conversation rows rendered');
   assert(await frame.locator('article.wbeffects.wbupdate').count()>0,'expanded whiteboard card');
   assert(await frame.locator('.turnline').count()>0,'turn summary rendered');
   const traceHeight=await frame.locator('.mailline > summary').first().evaluate(el=>el.getBoundingClientRect().height);
   const primaryColor=await frame.evaluate(()=>{
    const probe=document.createElement('span');
    probe.style.color='var(--wb-primary)';
    document.body.appendChild(probe);
    const color=getComputedStyle(probe).color;
    probe.remove();
    return color;
   });
   const collapsedRows=frame.locator('.conv-row:not(.aph):not(.mh)');
   for(const row of await collapsedRows.elementHandles()) {
    const metrics=await row.evaluate(el=>({
     classes:el.className,
     text:el.textContent.trim().slice(0,160),
     height:el.getBoundingClientRect().height,
     whiteSpace:getComputedStyle(el.querySelector('.row-content')).whiteSpace,
     contentColor:getComputedStyle(el.querySelector('.row-content :is(.mpeer, b)') || el.querySelector('.row-content')).color,
     iconColor:getComputedStyle(el.querySelector('.row-icon :is(.arr, .i, .li)') || el.querySelector('.row-icon')).color,
     isMail:!!el.closest('.mail-notification') || !!el.querySelector('.mail-icon')
    }));
    assert.equal(metrics.whiteSpace,'nowrap','collapsed row content stays on one line');
    assert(metrics.height<=traceHeight+4,`collapsed row stays one trace line (${metrics.height}px vs ${traceHeight}px; ${metrics.classes}; ${metrics.text})`);
    if(metrics.isMail) {
     assert.equal(metrics.iconColor,primaryColor,'mail icon uses the primary blue family');
     assert.equal(metrics.contentColor,primaryColor,'trigger mail name uses the primary blue family');
    }
   }
   for(const height of await frame.locator('.turnline > summary').evaluateAll(rows=>rows.map(el=>el.getBoundingClientRect().height))) {
    assert(height<=traceHeight+4,`turn summary stays one trace line (${height}px vs ${traceHeight}px)`);
   }
   // Retain collapsed and expanded examples in the same capture.
   await frame.locator('details.turnline').last().evaluate(el=>el.open=true);
   for (const row of await frame.locator('.au-call-details > summary, .mailline > summary, .mail-notification > summary, .turnline > summary').elementHandles()) {
    assert.equal(await row.evaluate(el=>[...el.querySelectorAll('.row-chevron')].some(c=>getComputedStyle(c).display!=='none')),false,'trace chevron hidden');
    assert(!(await row.textContent()).includes('✓'),'trace has no success check');
   }
   const flatCall=frame.locator('.au-call-details').filter({has:frame.locator('[data-call="grammar-call"]')}).first();
   await flatCall.evaluate(el=>{for(let p=el.parentElement;p;p=p.parentElement) if(p.tagName==='DETAILS')p.open=true;el.open=false;});
   await flatCall.locator('summary').click();
   assert(await flatCall.evaluate(el=>el.open),'click expands tool details');
   const geometry=await frame.locator('summary[data-call="grammar-call"], .au-turn-tool').evaluateAll(rows=>rows.filter(el=>el.getClientRects().length).map(el=>{
    const icon=el.querySelector('.row-icon');
    return {x:icon.getBoundingClientRect().x-el.getBoundingClientRect().x,font:getComputedStyle(icon).fontSize};
   }));
   assert(geometry.length>1,'turn references retained');
   for(const metric of geometry) assert.deepEqual(metric,geometry[0],'tool and reference icon alignment and font match');
   const capture=async name=>{
    if (!shots) return;
    fs.mkdirSync(shots,{recursive:true});
    const box=frame.locator('#convo');
    const dims=await box.evaluate(el=>({height:el.clientHeight,total:el.scrollHeight,top:el.scrollTop}));
    for(let top=0,index=0;top<dims.total;top+=Math.max(1,Math.floor(dims.height*.8)),index++) {
     await box.evaluate((el,top)=>el.scrollTop=top,top);
     await box.screenshot({path:path.join(shots,`${width}-${theme}-${name}-${index}.png`),animations:'disabled'});
    }
    await box.evaluate((el,top)=>el.scrollTop=top,dims.top);
   };
   const pending=frame.locator('.apcard').first();
   await pending.waitFor({timeout:5000});
   await capture('pending');
   await pending.locator('button.allow').click({timeout:5000});
   await frame.locator('.apanswered').waitFor({timeout:5000});
   assert.equal(await frame.locator('.apanswered').count(),1,'answered approval is one trace');
   await frame.locator('.apanswered > .conv-audit-items [data-event-id="grammar-approval"]').waitFor({state:'attached',timeout:5000});
   const call=frame.locator('.au-call-details').filter({has:frame.locator('[data-call="grammar-call"]')}).first();
   await call.evaluate(el=>{let p=el.parentElement;while(p){if(p.tagName==='DETAILS')p.open=true;p=p.parentElement;}el.open=true;});
   await call.locator('.au-call-output').waitFor({timeout:5000});
   assert.match(await call.locator('.au-call-output').innerText(),/fixture result/,'retained result expands inline');
   await call.locator('.au-open-call').click();
   await frame.locator('.au-win').first().waitFor({timeout:5000});
   if(shots) await frame.locator('.au-win').first().screenshot({path:path.join(shots,`${width}-${theme}-audit-inspector.png`),animations:'disabled'});
   await capture('inspector');
   await page.keyboard.press('Escape');
   await capture('conversation');
   if (shots) {
    const rows=frame.locator('.centries > [data-entry], .centries > .au-sysrow, .apcard');
    for(let i=0;i<await rows.count();i++) {
     const row=rows.nth(i);
     if(!await row.isVisible()) continue;
     await row.screenshot({path:path.join(shots,`${width}-${theme}-row-${String(i).padStart(2,'0')}.png`),animations:'disabled'});
    }
    fs.writeFileSync(path.join(shots,`${width}-${theme}-rows.json`),JSON.stringify(await rows.evaluateAll(rows=>rows.map((el,index)=>({index,classes:el.className,at:el.dataset.at,text:(el.querySelector('summary')||el).textContent.trim().slice(0,300)}))),null,2));
    // Show owning traces in their real expansions as well as the overview.
    await frame.locator('.centries details').evaluateAll(rows=>rows.forEach(row=>row.open=true));
    await capture('expanded');
   }
   for(const name of ['compacting','retry']) {
    await frame.locator('#r-convlive').evaluate((el,html)=>el.innerHTML=html,live[name]);
    assert(await frame.locator('#r-convlive .boundary-badge').count()>0,name+' uses boundary badge');
    await capture(name);
    await frame.locator('#r-convlive details').evaluateAll(rows=>rows.forEach(row=>row.open=true));
    await capture(name+'-expanded');
   }
   await frame.locator('#r-convlive').evaluate(el=>el.replaceChildren());
   await frame.evaluate(()=>document.dispatchEvent(new CustomEvent('ws-entries-merged')));
   assert.equal(await frame.locator('.boundary-badge.retry').count(),0,'recovered retry badge disappears');
   assert.deepEqual(errors,[],`${width}/${theme} browser errors`);
   console.log(JSON.stringify({width,theme,rows:count,shots:!!shots}));
   if(shots) {
    const reference=await context.newPage();
    await reference.setContent(fs.readFileSync(path.resolve(__dirname,'../../../../ai-docs/assets/mockups/261123/convo-row-grammar/convo-row-grammar.html'),'utf8'));
    await reference.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
    await reference.locator('.crg-col').nth(1).screenshot({path:path.join(shots,`${width}-${theme}-reference.png`),animations:'disabled'});
    const cases=reference.locator('.crg-case > .crg-pane.n');
    for(let i=0;i<await cases.count();i++) await cases.nth(i).screenshot({path:path.join(shots,`${width}-${theme}-reference-case-${i}.png`),animations:'disabled'});
   }
   await context.close();
  }
 } finally {await browser.close();}
})().catch(e=>{console.error(e);process.exit(1);});

// Real control-driven workflow acceptance; launched by TestBrowserWorkflow.
const assert = require('node:assert/strict');
const {chromium} = require('playwright-core');

(async () => {
  const browser = await chromium.launch({headless:true, executablePath:process.env.MIDDEN_BROWSER});
  const page = await browser.newPage({viewport:{width:1440,height:1000}});
  const errors=[];
  page.on('pageerror',error=>errors.push(error.message));
  try {
    await page.goto(process.argv[2]);
    await page.locator('#chat-input').waitFor();
    await page.locator('.nav-item[data-view="tools"]').click();
    const facet=page.locator('.tool-card').filter({has:page.getByRole('heading',{name:'Facet',exact:true})});
    await facet.getByRole('link',{name:'Explore Facet',exact:true}).waitFor();
    assert.equal(await facet.getByRole('button').count(),0,'Facet is a separate project, not a managed adapter');
    assert.equal(await page.getByText('OpenMontage',{exact:false}).count(),0,'retired integration remains advertised');
    await page.locator('.nav-item[data-view="chat"]').click();
    assert.equal(await page.locator('#chat-input').isDisabled(),process.env.MIDDEN_BROWSER_AGENT_TEST!=='1'&&process.env.MIDDEN_BROWSER_STREAM_TEST!=='1','readiness must match configured runtime');
    if(process.env.MIDDEN_BROWSER_STREAM_TEST==='1'){
      await page.locator('#chat-input').fill('Stream until stopped');
      await page.locator('#chat-content').getByRole('button',{name:'Send',exact:true}).click();
      await page.locator('#chat-stream-content').getByText('A visible streaming response',{exact:true}).waitFor();
      await page.getByRole('button',{name:'Stop',exact:true}).click();
      await page.locator('#chat-content').getByRole('button',{name:'Send',exact:true}).waitFor();
      console.log('PASS: real kernel stream visible before completion; browser Stop cancels turn.');
    }
    await page.locator('.nav-item[data-view="plan"]').click();
    await page.getByRole('button',{name:'New recipe',exact:true}).click();
    const modal=page.locator('#modal');
    await modal.locator('select').first().selectOption('browser-fixture');
    await modal.locator('input').fill('Browser fixture production');
    await modal.locator('textarea').fill('Create a provenance manifest of the selected recovery decision.');
    await modal.locator('select').last().selectOption('provenance_manifest');
    await modal.getByRole('button',{name:'Create work item',exact:true}).click();
    await page.locator('#plan-content').getByText('Browser fixture production',{exact:true}).first().waitFor();
    console.log('DESIGNED', (await page.locator('#plan-content').innerText()).slice(0,1800));
    await page.getByRole('button',{name:'Review evidence',exact:true}).first().click();
    await page.locator('#drawer').getByRole('button',{name:'Approve evidence',exact:true}).click();
    await page.getByRole('button',{name:'Preview run',exact:true}).click();
    await page.locator('#modal').getByRole('button',{name:'Run in background',exact:true}).click();
    await page.getByText('Drafts are ready for review.',{exact:true}).waitFor({timeout:45000});
    console.log('PRODUCED',(await page.locator('#plan-content').innerText()).slice(0,2200));
    await page.locator('#plan-content').getByRole('button',{name:'source',exact:true}).click();
    const editor=page.locator('.document-editor');
    await editor.waitFor();
    assert.match(await editor.inputValue(),/browser-fixture|Synthetic recovery/);
    await page.getByRole('button',{name:'Approve output',exact:true}).click();
    await page.getByRole('button',{name:'Export local',exact:true}).click();
    await page.getByText('Exported locally:',{exact:false}).waitFor();
    assert.equal(await page.evaluate(()=>document.body.scrollWidth<=innerWidth),true,'desktop overflow');
    assert.deepEqual(errors,[]);
    console.log('PASS: design → evidence approval → deterministic draft → source review → local export. No page errors.');
    if(process.env.MIDDEN_BROWSER_AGENT_TEST==='1'){
      await page.locator('.nav-item[data-view="chat"]').click();
      await page.getByRole('button',{name:'New chat',exact:true}).click();
      await page.locator('#chat-input').fill('Create, review and export the synthetic browser fixture manifest.');
      await page.locator('#chat-content').getByRole('button',{name:'Send',exact:true}).click();
      for(const tool of ['midden_recipes_design','midden_recipes_evidence','midden_recipes_produce','midden_outputs_review','midden_outputs_export']){
        await page.getByRole('heading',{name:`Allow ${tool}?`,exact:true}).waitFor({timeout:20000});
        await page.getByRole('button',{name:'Allow once',exact:true}).click();
      }
      await page.getByText('Browser fixture workflow exported with provenance.',{exact:true}).waitFor();
      await page.locator('.nav-item[data-view="create"]').click();
      await page.locator('#create-content').getByText('Bundle provenance manifest',{exact:true}).first().waitFor();
      assert.deepEqual(errors,[]);
      console.log('PASS: kernel tool dispatch → five real browser approvals → inspected draft → reviewed export → shared library. Model was scripted.');
    }
  } finally { await browser.close(); }
})().catch(error=>{console.error(error);process.exitCode=1;});

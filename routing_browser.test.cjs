const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');
const {chromium}=require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const net=require('node:net');
const {spawn}=require('node:child_process');
const {once}=require('node:events');
const {randomBytes}=require('node:crypto');

// Opt-in real executable acceptance: no API mocks, no external probe hosts.
test('production UI and backend routing reload/restart acceptance', {skip:!process.env.JSON_PING_BINARY,timeout:90000}, async()=>{
 const root=process.env.JSON_PING_TEST_WORKDIR || process.env.TMPDIR;
 assert.ok(root,'Set JSON_PING_TEST_WORKDIR or TMPDIR to a disposable scratch directory');
 const work=fs.mkdtempSync(path.join(root,'routing-real-'));
 const server=net.createServer(socket=>{connections++;socket.end();});
 let connections=0, child, browser, logs='';
 const password=randomBytes(18).toString('hex');
 const listen=async s=>{s.listen(0,'127.0.0.1');await once(s,'listening');return s.address().port;};
 const poll=async fn=>{const end=Date.now()+15000;while(Date.now()<end){try{const result=await fn();if(result)return result;}catch{}await new Promise(r=>setTimeout(r,100));}throw new Error('Timed out\n'+logs);};
 const stop=async()=>{if(child && child.exitCode===null){const exited=once(child,'exit');child.kill();await exited;}};
 try {
  const probePort=await listen(server), reservation=net.createServer();
  const webPort=await listen(reservation);await new Promise(r=>reservation.close(r));
  const origin=`http://127.0.0.1:${webPort}`;
  fs.mkdirSync(path.join(work,'targets'));
  const ping='# No ICMP or demo targets in this test.\n';
  const tcp=`# Preserve this comment and target definition.\n127.0.0.1:${probePort} Loopback interval=1\n`;
  fs.writeFileSync(path.join(work,'targets/ping.list'),ping);
  fs.writeFileSync(path.join(work,'targets/tcp.list'),tcp);
  const start=async()=>{child=spawn(path.resolve(process.env.JSON_PING_BINARY),['--listen',`127.0.0.1:${webPort}`,'user=acceptance',`passwd=${password}`],{cwd:work,stdio:['ignore','pipe','pipe']});child.stdout.on('data',b=>logs+=b);child.stderr.on('data',b=>logs+=b);await poll(async()=> (await fetch(origin+'/api/version')).ok);};
  await start();
  browser=await chromium.launch({headless:true,channel:process.env.BROWSER_CHANNEL || undefined});
  const page=await browser.newPage(), errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  await page.route('**/*',route=>new URL(route.request().url()).origin===origin?route.continue():route.abort());
  const login=async()=>{await page.goto(origin);await page.locator('#user').fill('acceptance');await page.locator('#pass').fill(password);await page.locator('#go').click();await page.waitForFunction(()=>document.querySelector('#routingSave') && !document.querySelector('#routingSave').disabled);};
  const read=async url=>{const response=await page.request.get(origin+url);assert.equal(response.status(),200);return response.json();};
  const open=async()=>{if(!await page.locator('#routingEditor').evaluate(e=>e.open))await page.locator('#routingEditor summary').click();};
  const save=async(vrf,source)=>{await open();await page.locator('#routingVRF').fill(vrf);await page.locator('#routingSource').fill(source);await page.locator('#routingSave').click();await page.waitForFunction(()=>document.querySelector('#routingMessage').textContent.includes('Saved; reload pending'));await poll(async()=>{const {target}=await read('/api/routing-options?target=Loopback');return (target.vrf||'')===vrf && (target.source_address||'')===source;});assert.deepEqual(JSON.parse(fs.readFileSync(path.join(work,'targets/routing-options.json'))).Loopback,{vrf,source_address:source});};
  await login();await poll(()=>connections>0);
  await save('','');
  await save('','127.0.0.1');
  await page.reload();await page.waitForFunction(()=>document.querySelector('#routingSource').value==='127.0.0.1');
  await save('missingvrf','');
  const failed=await poll(async()=>{const data=await read('/api/targets');return data.find(t=>t.name==='Loopback' && t.error);});
  assert.equal(failed.down,false);assert.match(failed.error,/vrf|routing|unsupported/i);
  const series=await read('/api/series?target=Loopback&minutes=5');
  assert.ok(series.length>0);assert.ok(series.every(r=>r.s===0 && r.r===0 && r.error));
  // A pre-reload 20-sample round can finish its remaining 50ms gaps.
  // Count only after that bounded old round drains; the new history above
  // must contain exclusively explicit zero-sample routing failures.
  await new Promise(r=>setTimeout(r,1500));
  const before=connections;await new Promise(r=>setTimeout(r,2500));assert.equal(connections,before,'unavailable VRF silently fell back');
  await page.reload();await page.waitForFunction(()=>document.querySelector('#conclusion').textContent.includes('Probe error'));
  await stop();await start();await login();
  assert.equal(await page.locator('#routingVRF').inputValue(),'missingvrf');
  await save('','127.0.0.1');await poll(()=>connections>before);
  await stop();await start();await login();
  assert.equal(await page.locator('#routingSource').inputValue(),'127.0.0.1');
  await save('','');
  await page.reload();await page.waitForFunction(()=>document.querySelector('#routingSave') && !document.querySelector('#routingSave').disabled);
  assert.equal(await page.locator('#routingVRF').inputValue(),'');
  assert.equal(await page.locator('#routingSource').inputValue(),'');
  await stop();await start();await login();
  assert.equal(await page.locator('#routingVRF').inputValue(),'');
  assert.equal(await page.locator('#routingSource').inputValue(),'');
  assert.equal(fs.readFileSync(path.join(work,'targets/ping.list'),'utf8'),ping);
  assert.equal(fs.readFileSync(path.join(work,'targets/tcp.list'),'utf8'),tcp);
  assert.deepEqual(errors,[]);
  console.log('Real backend acceptance workspace:',work);
 } finally {if(browser)await browser.close();await stop();await new Promise(r=>server.close(r));}
});

test('routing editor loads saved pending options instead of stale active values', async()=>{
 const browser=await chromium.launch({headless:true,channel:process.env.BROWSER_CHANNEL || undefined});
 try {
  const page=await browser.newPage();
  await page.route('**/*',route=>{
   const u=new URL(route.request().url());
   if(u.origin!=='http://routing.test')return route.abort();
   if(u.pathname==='/')return route.fulfill({contentType:'text/html',body:fs.readFileSync(path.join(__dirname,'static/index.html'))});
   if(u.pathname.startsWith('/static/'))return route.fulfill({contentType:'text/javascript',body:fs.readFileSync(path.join(__dirname,u.pathname))});
   if(u.pathname==='/api/targets')return route.fulfill({json:[]});
   if(u.pathname==='/api/version')return route.fulfill({json:{version:'test'}});
   if(u.pathname==='/api/routing-options')return route.fulfill({json:{editable:true,target:{name:'A',vrf:'',source_address:''},saved_target:{name:'A',vrf:'blue',source_address:'127.0.0.1'},pending_reload:true}});
   return route.abort();
  });
  await page.goto('http://routing.test');
  // Let the initial dashboard render finish before directly exercising the editor.
  // Its empty-target response must not race and clear the explicit test selection.
  await page.waitForFunction(()=>!rendering);
  await page.evaluate(()=>routingEditor.select('A'));
  assert.equal(await page.locator('#routingVRF').inputValue(),'blue');
  assert.equal(await page.locator('#routingSource').inputValue(),'127.0.0.1');
  assert.match(await page.locator('#routingMessage').textContent(),/pending/i);
 } finally {await browser.close();}
});

test('routing editor ignores stale target/save responses and permits failed-save retry', {timeout:30000}, async()=>{
 const browser=await chromium.launch({headless:true,channel:process.env.BROWSER_CHANNEL || undefined});
 try {
  const page=await browser.newPage();let releaseA, releaseSave, calls=0;
  await page.route('**/*',async route=>{
   const u=new URL(route.request().url());
   if(u.origin!=='http://routing.test')return route.abort();
   if(u.pathname==='/')return route.fulfill({contentType:'text/html',body:fs.readFileSync(path.join(__dirname,'static/index.html'))});
   if(u.pathname.startsWith('/static/'))return route.fulfill({contentType:'text/javascript',body:fs.readFileSync(path.join(__dirname,u.pathname))});
   if(u.pathname==='/api/targets')return route.fulfill({json:[]});
   if(u.pathname==='/api/version')return route.fulfill({json:{version:'test'}});
   if(u.pathname==='/api/routing-options'){
    if(route.request().method()==='PUT'){
     calls++;if(calls===1){await new Promise(r=>releaseSave=r);return route.fulfill({json:{saved:true,pending_reload:true}});}
     return route.fulfill(calls===2?{status:500,body:'disk write failed'}:{json:{saved:true,pending_reload:true}});
    }
    const name=u.searchParams.get('target');
    if(name==='A')await new Promise(r=>releaseA=r);
    return route.fulfill({json:{editable:true,target:{name,vrf:'',source_address:name==='B'?'127.0.0.1':''}}});
   }
   return route.abort();
  });
  await page.goto('http://routing.test');
  // Let the initial dashboard render finish before directly exercising the editor.
  // Its empty-target response must not race and clear the explicit test selection.
  await page.waitForFunction(()=>!rendering);
  await page.evaluate(()=>{routingEditor.select('A');});
  while(!releaseA)await new Promise(r=>setTimeout(r,10));
  await page.evaluate(()=>routingEditor.select('B'));
  releaseA();await page.waitForFunction(()=>document.querySelector('#routingSource').value==='127.0.0.1');
  await page.locator('#routingEditor').evaluate(e=>{e.open=true;});
  await page.locator('#routingSave').click();while(!releaseSave)await new Promise(r=>setTimeout(r,10));
  await page.evaluate(()=>routingEditor.select('C'));releaseSave();
  await page.waitForFunction(()=>!document.querySelector('#routingSave').disabled);
  assert.equal(await page.locator('#routingTarget').textContent(),'C');
  assert.equal(await page.locator('#routingSource').inputValue(),'');
  assert.equal(await page.locator('#routingMessage').textContent(),'');
  await page.locator('#routingSource').fill('127.0.0.2');await page.locator('#routingSave').click();
  await page.waitForFunction(()=>document.querySelector('#routingMessage').textContent.includes('Not saved: disk write failed'));
  assert.equal(await page.locator('#routingSource').inputValue(),'127.0.0.2');
  assert.equal(await page.locator('#routingSource').isDisabled(),false);
  await page.locator('#routingSave').click();await page.waitForFunction(()=>document.querySelector('#routingMessage').textContent.includes('Saved; reload pending'));
  assert.equal(calls,3);
 }finally{await browser.close();}
});

test('routing editor preserves optional blanks, locks saves and shows probe errors',async()=>{
 const browser=await chromium.launch({headless:true,channel:process.env.BROWSER_CHANNEL || undefined});
 try {
 const page=await browser.newPage();
 const errors=[];page.on('pageerror',e=>errors.push(e.message));
 let editable=true, saved=[], release;
 const target={name:'A',type:'icmp',host:'127.0.0.1',vrf:'',source_address:'',down:false,error:'source unavailable'};
 await page.route('**/*',async route=>{
  const u=new URL(route.request().url());
  if(u.origin!=='http://routing.test') return route.abort();
  if(u.pathname==='/') return route.fulfill({contentType:'text/html',body:fs.readFileSync(path.join(__dirname,'static/index.html'))});
  if(u.pathname.startsWith('/static/')) return route.fulfill({contentType:'text/javascript',body:fs.readFileSync(path.join(__dirname,u.pathname))});
  if(u.pathname==='/api/targets') return route.fulfill({json:[target]});
  if(u.pathname==='/api/version') return route.fulfill({json:{version:'test'}});
  if(u.pathname==='/api/series') return route.fulfill({json:[{t:1,s:1,r:1,ms:[3]},{t:2,s:0,r:0,error:'source unavailable'},{t:3,s:1,r:1,ms:[4]}]});
  if(u.pathname==='/api/routing-options'){
   if(route.request().method()==='PUT'){
    saved.push(route.request().postDataJSON());await new Promise(r=>release=r);
    return route.fulfill({json:{saved:true,pending_reload:true,target:{...target,...saved.at(-1)}}});
   }
   return route.fulfill({json:{editable,target}});
  }
  return route.abort();
 });
 await page.goto('http://routing.test');
  // Let the initial dashboard render finish before directly exercising the editor.
  // Its empty-target response must not race and clear the explicit test selection.
  await page.waitForFunction(()=>!rendering);
 await page.locator('#routingEditor').waitFor();
 assert.equal(await page.locator('#routingEditor').evaluate(e=>e.open),false);
 await page.locator('#routingEditor summary').click();
 await page.waitForFunction(()=>!document.querySelector('#routingSave').disabled);
 assert.equal(await page.locator('#routingVRF').inputValue(),'');
 assert.equal(await page.locator('#routingSource').inputValue(),'');
 await page.locator('#routingSource').fill('127.0.0.1');
 await page.locator('#routingForm').evaluate(e=>{e.dispatchEvent(new Event('submit',{bubbles:true,cancelable:true}));e.dispatchEvent(new Event('submit',{bubbles:true,cancelable:true}));});
 await page.waitForFunction(()=>document.querySelector('#routingSource').disabled);
 await new Promise(r=>setTimeout(r,100));assert.equal(saved.length,1);
 assert.deepEqual(saved[0],{name:'A',vrf:'',source_address:'127.0.0.1'});
 release();await page.waitForFunction(()=>document.querySelector('#routingMessage').textContent.includes('Saved'));
 assert.match(await page.locator('#routingMessage').textContent(),/pending/i);
 assert.doesNotMatch(await page.locator('#routingMessage').textContent(),/next probe uses/);
 await page.locator('#routingSource').fill('');
 await page.locator('#routingSave').click();
 while(saved.length<2) await new Promise(r=>setTimeout(r,10));
 assert.deepEqual(saved[1],{name:'A',vrf:'',source_address:''});
 release();await page.waitForFunction(()=>!document.querySelector('#routingSave').disabled);
 assert.match(await page.locator('#conclusion').textContent(),/Probe error/);
 const option=await page.evaluate(()=>echarts.getInstanceByDom(document.querySelector('#chart')).getOption());
 assert.deepEqual(option.series[1].data,[[1000,3],[2000,null],[3000,4]]);
 assert.deepEqual(option.series[3].data,[]);
 editable=false;await page.reload();await page.locator('#routingEditor summary').click();
 await page.waitForFunction(()=>document.querySelector('#routingMessage').textContent.includes('read-only'));
 assert.equal(await page.locator('#routingSave').isDisabled(),true);
 assert.deepEqual(errors,[]);
 } finally {await browser.close();}
});

"use strict";
const assert = require("node:assert/strict");
const fs = require("node:fs");
const {chromium} = require("playwright");
(async () => {
 const config = JSON.parse(fs.readFileSync(process.env.NEWSTAR_BROWSER_TEST_CONFIG,"utf8"));
 const browser = await chromium.launch({headless:true,...(process.env.BROWSER_EXECUTABLE?{executablePath:process.env.BROWSER_EXECUTABLE}:{})});
 try {
  for (const [index,viewport] of [{width:1440,height:1000},{width:390,height:844}].entries()) {
   const context = await browser.newContext({viewport});const page=await context.newPage();const errors=[];page.on("pageerror",e=>errors.push(e.message));
   const username="browseruser"+index,password="Synthetic browser password 123",nextPassword="Replacement browser password 456";
   await page.goto(config.account+"/register");
   await page.locator('input[name=username]').fill(username);await page.locator('input[name=password]').fill(password);await page.locator('input[name=invite_code]').fill(config.invites[index]);await page.getByRole("button",{name:"注册并登录"}).click();
   await page.locator('[data-account-status]').waitFor({state:"visible"});
   const result=await page.locator('[data-account-status]').textContent();const match=result.match(/恢复码：([A-Za-z0-9_-]{43})/);assert(match,"one-time recovery code must remain visible");
   await page.goto(config.atlas+"/login");
   const continueLink=page.locator('a[href*="/authorize?"]');if(await continueLink.count())await continueLink.click();
   await page.getByRole("button",{name:"确认并返回星图"}).click();await page.getByRole("link",{name:"继续返回星图"}).click();await page.waitForURL(config.atlas+"/security");assert(await page.getByRole("heading",{name:"安全",exact:true}).isVisible());
   await page.goto(config.atlas+"/");assert(await page.locator('.composer').isVisible());
   assert(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth+1),"horizontal overflow");
   if(config.artifacts) await page.screenshot({path:config.artifacts+"/home-"+viewport.width+".png",fullPage:true});
   const search=page.locator('[data-overlay-open="search-overlay"]');await search.focus();await page.keyboard.press('Enter');
   assert(await page.locator('#search-overlay').isVisible());assert.equal(await page.evaluate(()=>document.activeElement.id),'overlay-query');
   await page.keyboard.press('Escape');assert(await search.evaluate(element=>element===document.activeElement));
   const more=page.locator('[data-overlay-open="more-overlay"]');await more.focus();await page.keyboard.press('Enter');assert(await page.locator('#more-overlay').isVisible());await page.keyboard.press('Escape');assert(await more.evaluate(element=>element===document.activeElement));
   for(const route of ['security','security/recovery','security/password']) {
    await page.goto(config.account+'/'+route);assert(await page.locator('h1').isVisible());
    assert(!(await page.locator('body').innerText()).includes(match[1]),'recovery secret repeated on security page');
    assert(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth+1),'security page horizontal overflow');
    if(config.artifacts)await page.screenshot({path:config.artifacts+'/'+route.replaceAll('/','-')+'-'+viewport.width+'.png',fullPage:true});
   }
   // Reset through the actual UI in a separate browser session.
   const recoveryContext=await browser.newContext({viewport});const recoveryPage=await recoveryContext.newPage();await recoveryPage.goto(config.account+"/forgot");
   await recoveryPage.locator('input[name=username]').fill(username);await recoveryPage.locator('input[name=recovery_code]').fill(match[1]);await recoveryPage.locator('input[name=password]').fill(nextPassword);await recoveryPage.getByRole('button',{name:'重置密码并撤销旧会话'}).click();await recoveryPage.getByText('密码已重置，恢复码和原有会话已失效。请重新登录并生成新恢复码。',{exact:true}).waitFor();
   const oldSession=await (await context.request.get(config.account+"/api/v1/auth/session")).json();assert.equal(oldSession.authenticated,false);
   await recoveryPage.goto(config.account+"/login");await recoveryPage.locator('input[name=username]').fill(username);await recoveryPage.locator('input[name=password]').fill(nextPassword);await recoveryPage.getByRole('button',{name:'登录',exact:true}).click();await recoveryPage.waitForURL(config.account+"/");
   await recoveryPage.goto(config.atlas+'/login');const again=recoveryPage.locator('a[href*="/authorize?"]');if(await again.count())await again.click();
   await recoveryPage.getByRole('button',{name:'确认并返回星图'}).click();await recoveryPage.getByRole('link',{name:'继续返回星图'}).click();await recoveryPage.waitForURL(config.atlas+'/security');
   assert.equal((await (await recoveryContext.request.get(config.atlas+'/api/v1/session')).json()).authenticated,true);
   const nativeContext=await browser.newContext({viewport,javaScriptEnabled:false,storageState:await recoveryContext.storageState()});const native=await nativeContext.newPage();
   await native.goto(config.account+'/security/password');
   const dailyPassword='Native browser replacement password 789';
   await native.locator('input[name=current_password]').fill('Incorrect synthetic password 123');await native.locator('input[name=password]').fill(dailyPassword);await native.locator('input[name=password_confirmation]').fill(dailyPassword);await native.getByRole('button',{name:'修改密码并退出登录'}).click();
   assert(await native.locator('[role=alert]').isVisible());for(const name of ['current_password','password','password_confirmation'])assert.equal(await native.locator('input[name='+name+']').inputValue(),'');
   await native.locator('input[name=current_password]').fill(nextPassword);await native.locator('input[name=password]').fill(dailyPassword);await native.locator('input[name=password_confirmation]').fill(dailyPassword);await native.getByRole('button',{name:'修改密码并退出登录'}).click();await native.getByText(/密码已修改/).first().waitFor();
   assert.equal((await (await recoveryContext.request.get(config.account+'/api/v1/auth/session')).json()).authenticated,false);
   assert.equal((await (await recoveryContext.request.get(config.atlas+'/api/v1/session')).json()).authenticated,false);
   await nativeContext.close();
   assert.equal(errors.length,0,errors.join("\n"));await recoveryContext.close();await context.close();
   console.log("PASS real Chromium desktop/mobile registration, OIDC, recovery, security pages, keyboard overlays, no-JavaScript daily password change, cross-service revocation and overflow: "+viewport.width);
  }
 } finally {await browser.close();}
})().catch(error=>{console.error(error);process.exit(1)});

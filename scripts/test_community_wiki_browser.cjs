"use strict";
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const {chromium} = require("playwright");

(async () => {
 const config = JSON.parse(fs.readFileSync(process.env.NEWSTAR_COMMUNITY_WIKI_CONFIG, "utf8"));
 const browser = await chromium.launch({headless:true});
 const results=[];
 const sourceLines=sample=>sample.sources.map(s=>[s.id,s.title,s.url,s.accessed].join(" | ")).join("\n");
 try {
  for(const [index,settings] of [
   {width:1440,js:true},{width:390,js:true},{width:1440,js:false},{width:390,js:false}
  ].entries()) {
   const label=settings.width+"-"+(settings.js?"js":"native");
   const contexts=[], errors=[];
   async function actor(role) {
    const context=await browser.newContext({viewport:{width:settings.width,height:1000},javaScriptEnabled:settings.js,
     ...(role?{storageState:config.states[role]}:{})});
    contexts.push(context);const page=await context.newPage();page.on("pageerror",e=>errors.push(e.message));return page;
   }
   const admin=await actor("admin"),rep=await actor("representative"),author=await actor("author"),anonymous=await actor();
   const stages=[];let current=author;
   async function screenshot(page,name) {
    current=page;
    assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),"horizontal overflow: "+name+" "+label);
    await page.evaluate(()=>window.scrollTo(0,0));
    await page.screenshot({path:path.join(config.artifacts,name+"-"+label+".png"),fullPage:true});
   }
   async function submit(page,action,button,within) {
    current=page;
    const form=within||page.locator('form[action="'+action+'"]');
    await form.getByRole("button",{name:button,exact:true}).focus();
    await Promise.all([page.waitForNavigation({waitUntil:"domcontentloaded"}),page.keyboard.press("Enter")]);
   }
   async function review(entry,number,decision,note) {
    await admin.goto(config.atlas+entry+"/submissions/"+number);
    const form=admin.locator('form[action="/admin'+entry+'/versions/'+number+'/review"]');
    await form.locator('[name=decision]').selectOption(decision);await form.locator('[name=note]').fill(note);
    await form.locator('[name=confirm]').check();await submit(admin,"/admin"+entry+"/versions/"+number+"/review","保存审核决定");
   }
   try {
    const base="/admin/communities/pixel-game-dev/tools";
    for(const kind of ["announcement","guide","discussion"]) {
     await rep.goto(config.atlas+base+"/new?kind="+kind);
     const title="页面验收 "+kind+" "+label;
     await rep.locator('[name=title]').fill(title);await rep.locator('[name=description]').fill("公开说明，临时验收数据。");
     if(kind==="discussion")await rep.locator('[name=references]').fill(config.posts[index]);
     else await rep.locator('[name=content]').fill("## 开始\n\n这是通过实际表单填写的 **公开内容**。");
     await rep.locator('[name=status]').selectOption("draft");await rep.locator('[name=summary]').fill("保存草稿");
     if(kind==="announcement")await rep.locator('[name=pinned]').check();
     await screenshot(rep,"tool-"+kind+"-form");await submit(rep,base,"保存");
     const edit=new URL(rep.url()).pathname;assert(edit.startsWith(base+"/")&&edit!==base+"/new");
     const publicPath="/c/pixel-game-dev/"+({announcement:"announcements",guide:"guide",discussion:"collections"}[kind]);
     await anonymous.goto(config.atlas+publicPath);assert(!(await anonymous.locator('body').innerText()).includes(title));
     await rep.locator('[name=status]').selectOption("published");await rep.locator('[name=summary]').fill("发布草稿");await submit(rep,edit,"保存");
     await anonymous.goto(config.atlas+publicPath);assert((await anonymous.locator('body').innerText()).includes(title));
     if(kind==="discussion")assert((await anonymous.locator('body').innerText()).includes("浏览器合集原帖 "+index));
     await screenshot(anonymous,"tool-"+kind+"-public");
     await rep.goto(config.atlas+edit+"/history");assert((await rep.locator('body').innerText()).includes("版本 2"));
     await rep.goto(config.atlas+edit);await rep.locator('[name=status]').selectOption("archived");await rep.locator('[name=summary]').fill("撤下验收资料");await submit(rep,edit,"保存");
     await anonymous.goto(config.atlas+publicPath);assert(!(await anonymous.locator('body').innerText()).includes(title));
    }
    stages.push("三类社群工具草稿、发布、版本与撤下");
    await author.goto(config.atlas+"/wiki/new");const sample=config.samples[0];
    await author.locator('[name=title]').fill("发现页 "+label);await author.locator('[name=body]').fill(sample.body);
    await author.locator('[name=sources]').fill(sourceLines(sample));await author.locator('[name=edit_summary]').fill("记录线索");await author.locator('[name=license]').check();
    await submit(author,"/wiki","保存发现页");const discovery=new URL(author.url()).pathname;
    await author.goto(config.atlas+discovery+"/edit");await rep.goto(config.atlas+discovery+"/edit");
    await rep.locator('[name=body]').fill(sample.body+"\n\n第二位编辑者整理。");await rep.locator('[name=edit_summary]').fill("共同编辑");await rep.locator('[name=license]').check();await submit(rep,discovery+"/edit","保存发现页");
    await author.locator('[name=body]').fill(sample.body+"\n\n冲突输入应保留。");await author.locator('[name=edit_summary]').fill("旧表单冲突");await author.locator('[name=license]').check();
    await submit(author,discovery+"/edit","保存发现页");assert((await author.locator('[name=body]').inputValue()).includes("冲突输入应保留"));assert(await author.locator('[role=alert]').isVisible());await screenshot(author,"wiki-conflict");
    stages.push("发现页双身份修改冲突与输入保留");
    for(const sample of config.samples) {
     await author.goto(config.atlas+"/wiki/entries/new?kind="+sample.kind);
     for(const field of ["title","summary","body"])await author.locator('[name='+field+']').fill(sample[field]+(field==="title"?" "+label:""));
     await author.locator('[name=sources]').fill(sourceLines(sample));await author.locator('[name=edit_summary]').fill("提交来源资料");
     if(sample.kind==="community") {await author.locator('[name=status_value]').selectOption(sample.status_value);await author.locator('[name=first_verified_at]').fill(sample.first_verified_at);}
     else {await author.locator('[name=work_type]').fill(sample.work_type);await author.locator('[name=creator]').fill(sample.creator);await author.locator('[name=public_url]').fill(sample.public_url);}
     await author.locator('[name=public_scope]').check();await author.locator('[name=license]').check();await screenshot(author,"wiki-"+sample.kind+"-form");
     await submit(author,"/wiki/entries","提交人工审核");const privatePath=new URL(author.url()).pathname;const entry=privatePath.split('/submissions/')[0];
     assert.equal((await anonymous.goto(config.atlas+entry)).status(),404);assert.equal((await rep.goto(config.atlas+privatePath)).status(),404);
     await review(entry,1,"accept","验收：已核对公开来源及脚注");await author.goto(config.atlas+entry);assert((await author.locator('body').innerText()).includes("人工审核发布"));
     await screenshot(author,"wiki-"+sample.kind+"-public");
     await author.locator('#wiki-suggestion-form>summary').click();let form=author.locator('form[action="'+entry+'/suggestions"]');
     await form.locator('[name=target]').selectOption('summary');await form.locator('[name=proposed]').fill(sample.suggested_summary);await form.locator('[name=evidence]').fill(sample.suggestion_evidence);await form.locator('[name=license]').check();await submit(author,entry+"/suggestions","提交建议");
     const suggestionURL=new URL(author.url());const suggestion=suggestionURL.searchParams.get('suggestion');assert(suggestion);
     current=admin;await admin.goto(suggestionURL.href);await admin.locator('#suggestion-'+suggestion+' details.admin-edit>summary').click();
     form=admin.locator('form[action="/admin'+entry+'/suggestions/'+suggestion+'/resolve"]');await form.locator('[name=decision]').selectOption('accepted');await form.locator('[name=note]').fill("摘要与公开资料一致，采纳");await form.locator('[name=confirm]').check();
     if(settings.width===390) {const field=await form.locator('[name=confirm]').boundingBox(),bar=await admin.locator('.sidebar').boundingBox();assert(field.y+field.height<=bar.y,'confirmation checkbox obscured by bottom navigation');}
     await screenshot(admin,"wiki-"+sample.kind+"-suggestion-review");await submit(admin,"/admin"+entry+"/suggestions/"+suggestion+"/resolve","保存处理结果");
     await author.goto(config.atlas+entry);assert((await author.locator('.wiki-summary').innerText()).includes(sample.suggested_summary));
     await author.locator('#wiki-suggestion-form>summary').click();form=author.locator('form[action="'+entry+'/suggestions"]');
     await form.locator('[name=target]').selectOption('body');await form.locator('[name=proposed]').fill('错误脚注的输入。[^missing]');await form.locator('[name=evidence]').fill('本次依据应保留');await form.locator('[name=license]').check();await submit(author,entry+"/suggestions","提交建议");
     assert.equal(await author.locator('[name=proposed]').inputValue(),'错误脚注的输入。[^missing]');assert(await author.locator('[role=alert]').isVisible());await screenshot(author,"wiki-"+sample.kind+"-suggestion-error");
     await author.goto(config.atlas+entry+"/propose");await author.locator('[name=body]').fill(sample.body+"\n\n退回稿备注应保留。");await author.locator('[name=edit_summary]').fill("提交完整修订");await author.locator('[name=license]').check();await author.locator('[name=public_scope]').check();await submit(author,entry+"/propose","提交人工审核");
     await review(entry,3,"reject","请保留公开事实与来源，调整整理备注");await author.goto(config.atlas+entry+"/submissions/3");await author.getByRole('link',{name:'重新提交',exact:true}).click();assert((await author.locator('[name=body]').inputValue()).includes('退回稿备注应保留'));await screenshot(author,"wiki-"+sample.kind+"-resubmit");
     await author.goto(config.atlas+entry+"/history");assert((await author.locator('body').innerText()).includes("版本 2"));await screenshot(author,"wiki-"+sample.kind+"-history");
     await author.goto(config.atlas+entry);form=author.locator('.wiki-discussion>form');await form.locator('[name=content]').fill('浏览器根讨论 '+label);await form.locator('[name=license]').check();await submit(author,entry+'/replies','发表讨论',form);
     const root=new URL(author.url()).hash.slice('#wiki-reply-'.length);assert(root);
     await rep.goto(config.atlas+entry);await rep.locator('#wiki-reply-'+root+'>details>summary').click();form=rep.locator('#wiki-reply-'+root+'>details>form');await form.locator('[name=content]').fill('浏览器下游回复 '+label);await form.locator('[name=license]').check();await submit(rep,entry+'/replies','发表回复',form);
     await anonymous.goto(config.atlas+entry);assert((await anonymous.locator('body').innerText()).includes('浏览器下游回复 '+label));await screenshot(anonymous,'wiki-'+sample.kind+'-discussion');
     await author.goto(config.atlas+entry);await submit(author,entry+'/replies/'+root+'/delete','删除回复及下游');await anonymous.goto(config.atlas+entry);assert(!(await anonymous.locator('body').innerText()).includes('浏览器下游回复 '+label));
    }
    stages.push("两类正式条目私有提交、审核、建议采纳、错误回填、退回重提、历史、多层讨论与下游撤回");
    assert.equal(errors.length,0,errors.join("\n"));results.push({label,status:"passed",stages});console.log("PASS community and Wiki real Chromium: "+label);
   } catch(error) {
    await current.screenshot({path:path.join(config.artifacts,"failure-"+label+".png"),fullPage:true}).catch(()=>{});
    results.push({label,status:"failed",stages,error:String(error)});throw error;
   } finally {for(const context of contexts)await context.close();}
  }
 } finally {fs.writeFileSync(path.join(config.artifacts,"results.json"),JSON.stringify(results,null,2));await browser.close();}
})().catch(error=>{console.error(error);process.exit(1)});

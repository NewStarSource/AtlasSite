"use strict";
document.addEventListener("DOMContentLoaded", () => {
  const editor = document.querySelector('input[name="draft_id"]')?.form;
  const status = document.querySelector("[data-draft-status]");
  const reply = document.querySelector("#reply-composer form");
  const errorMessages = {VERSION_CONFLICT:"内容已在另一处更新。请保留文字，重新打开后再修改。", CONFLICT:"内容已在另一处更新。请保留文字，重新打开后再修改。", INPUT_INVALID:"请检查必填项、正文长度、图片数量与许可。", REAUTH_REQUIRED:"请先到登录与安全页面重新确认身份。", READ_ONLY:"当前暂停投稿，请保留文字，稍后重试。", IDENTITY_UNAVAILABLE:"账户服务暂时不可用，请保留文字，稍后重试。", CSRF_INVALID:"页面验证已过期，请保留文字后刷新页面。"};
  const message = (data, code) => data.message || errorMessages[data.code] || (code === 409 ? "内容已更新，请保留文字后重新打开。" : "操作未完成，请保留文字后重试。");
  document.querySelectorAll("[data-state-form]").forEach(form => {
    let pending = false;
    form.addEventListener("submit", async event => {
      event.preventDefault();
      if (pending) return;
      const name = form.dataset.stateName, input = form.elements[name];
      const button = form.querySelector("button"), feedback = form.querySelector("[data-state-status]");
      const data = new URLSearchParams(new FormData(form));
      pending = true; button.disabled = true; form.setAttribute("aria-busy", "true"); feedback.textContent = "";
      try {
        const response = await fetch(form.action, {method:"POST", body:data, headers:{Accept:"application/json"}});
        const result = await response.json();
        if (!response.ok) throw new Error(message(result, response.status));
        if (typeof result[name] !== "boolean") throw new Error("状态未确认，请刷新后重试。");
        const active = result[name], label = active ? button.dataset.labelOn : button.dataset.labelOff;
        input.value = String(!active);
        button.setAttribute("aria-pressed", String(active)); button.setAttribute("aria-label", label); button.title = label;
        const text = button.querySelector("[data-action-label]"); if (text) text.textContent = label;
        const count = button.querySelector("[data-action-count]");
        if (count && Number.isInteger(result.count)) count.textContent = String(result.count);
        feedback.textContent = active ? label : "已取消";
      } catch (error) { feedback.textContent = error.name === "TypeError" ? "连接中断，请稍后重试。" : error.name === "SyntaxError" ? "状态未确认，请刷新后重试。" : error.message; }
      finally { pending = false; button.disabled = false; form.removeAttribute("aria-busy"); }
    });
  });
  let dirty = false, busy = false, revision = 0, uploading = false, submitting = false;
  const touch = () => { dirty = true; revision++; };
  if (editor) {
    editor.addEventListener("input", touch);
    const draftID = editor.elements.draft_id.value;
    const save = async () => {
      if (!draftID || !dirty || busy || submitting) return;
      busy = true; const savedRevision = revision;
      if(status) status.textContent = "正在保存草稿…";
      try {
        const response = await fetch("/api/v1/drafts/" + encodeURIComponent(draftID), {method:"POST", body:new URLSearchParams(new FormData(editor)), headers:{Accept:"application/json"}});
        const data = await response.json(); if(!response.ok) throw new Error(message(data,response.status));
        editor.elements.version.value = data.version;
        dirty = revision !== savedRevision;
        if(status) status.textContent = dirty ? "待保存" : "已保存 · " + new Date().toLocaleTimeString("zh-CN",{hour:"2-digit",minute:"2-digit"});
      } catch(error) {if(status) status.textContent=error.message;} finally {busy=false;}
    };
    if(draftID) setInterval(save,15000);
    editor.querySelector('button[formaction]')?.addEventListener("click", event => {event.preventDefault();touch();save();});
    let previewTimer, previewRequest, previewRevision = 0;
    const editPanel = editor.querySelector("[data-edit-panel]");
    const previewPanel = editor.querySelector("[data-preview-panel]");
    const modes = editor.querySelectorAll("[data-editor-mode]");
    const smallScreen = window.matchMedia("(max-width: 1023px)");
    let mode = "edit";
    const applyMode = () => {
      if (!editPanel || !previewPanel) return;
      editPanel.hidden = smallScreen.matches && mode === "preview";
      previewPanel.hidden = smallScreen.matches && mode === "edit";
      modes.forEach(button => {
        const active = button.dataset.editorMode === mode;
        button.classList.toggle("active", active);
        button.setAttribute("aria-pressed", String(active));
      });
    };
    smallScreen.addEventListener("change", applyMode);
    applyMode();
    const preview = async () => {
      const output = editor.querySelector("[data-preview-output]");
      if (!output) return;
      const current = ++previewRevision;
      previewRequest?.abort();
      previewRequest = new AbortController();
      try {
        const response = await fetch("/api/v1/preview", {method:"POST",body:new URLSearchParams(new FormData(editor)),headers:{Accept:"text/html"},signal:previewRequest.signal});
        if (!response.ok) throw new Error("预览未完成，请重试。");
        const html = await response.text();
        if (current === previewRevision) output.innerHTML = html;
      } catch(error) {
        if (error.name !== "AbortError" && current === previewRevision && status) status.textContent = error.message;
      }
    };
    modes.forEach(button => button.addEventListener("click", () => {
      mode = button.dataset.editorMode;
      applyMode();
      if (mode === "preview") preview();
    }));
    const queuePreview = () => {clearTimeout(previewTimer);previewTimer = setTimeout(preview, 300);};
    editor.elements.content.addEventListener("input", queuePreview);
    if (editor.elements.content.value) queuePreview();
    editor.querySelectorAll("[data-insert]").forEach(button=>button.addEventListener("click",()=>{
      const input=editor.elements.content;const selected=input.value.slice(input.selectionStart,input.selectionEnd);const type=button.dataset.insert;
      const text=type==="bold"?"**"+(selected||"重点内容")+"**":type==="heading"?"\n## "+(selected||"小标题")+"\n":"["+(selected||"链接文字")+"](https://)";
      input.setRangeText(text,input.selectionStart,input.selectionEnd,"end");input.focus();touch();queuePreview();
    }));
    const upload=document.querySelector("[data-upload-form]");
    upload?.addEventListener("submit",async event=>{
      event.preventDefault();if(uploading)return;
      const feedback=upload.querySelector("[data-upload-status]");const file=upload.elements.image.files[0];
      if(!file)return;if(file.size>8*1024*1024){feedback.textContent="图片超过 8 MiB，请缩小后上传。";return;}
      if((editor.elements.content.value.match(/!\[[^\]]*\]\(\/media\//g)||[]).length>=4){feedback.textContent="每条动态最多 4 张图片。";return;}
      uploading=true;const button=upload.querySelector("button");button.disabled=true;feedback.textContent="上传中…";
      try {const response=await fetch(upload.action,{method:"POST",body:new FormData(upload),headers:{Accept:"application/json"}});const data=await response.json();if(!response.ok)throw new Error(message(data,response.status));
        const input=editor.elements.content;input.setRangeText("\n"+data.markdown+"\n",input.selectionStart,input.selectionEnd,"end");touch();queuePreview();
        const image=document.createElement("img");image.src="/media/"+encodeURIComponent(data.id)+"/thumbnail";image.alt="已插入正文的自有图片";upload.querySelector("[data-upload-preview]").appendChild(image);
        feedback.textContent="图片已插入正文";upload.elements.image.value="";
      }catch(error){feedback.textContent=error.message;}finally{uploading=false;button.disabled=false;}
    });
  }
  document.querySelectorAll("[data-content-form]").forEach(form=>form.addEventListener("submit",async event=>{
    if(event.submitter?.hasAttribute("formaction"))return;event.preventDefault();if(submitting)return;
    const feedback=form.querySelector("[data-form-error]");feedback.hidden=false;
    if(uploading||busy){feedback.textContent="图片或草稿正在保存，请稍等后再提交。";return;}
    submitting=true;const button=event.submitter;if(button)button.disabled=true;feedback.textContent="正在提交…";
    try {const response=await fetch(form.action,{method:"POST",body:new URLSearchParams(new FormData(form)),headers:{Accept:"application/json"}});
      if(!response.ok){const data=await response.json();throw new Error(message(data,response.status));}
      if(response.redirected){dirty=false;window.location.assign(response.url);}else{feedback.textContent="已保存。";}
    }catch(error){feedback.textContent=error.message;}finally{submitting=false;if(button)button.disabled=false;}
  }));
  if(reply){
    const target=reply.querySelector("[data-reply-target]");
    document.querySelectorAll("[data-reply-id]").forEach(link=>link.addEventListener("click",event=>{event.preventDefault();reply.elements.parent_id.value=link.dataset.replyId;target.hidden=false;reply.querySelector("[data-reply-label]").textContent="回复 "+link.dataset.replyName;reply.scrollIntoView({block:"center"});reply.elements.content.focus();}));
    reply.querySelector("[data-reply-cancel]")?.addEventListener("click",()=>{reply.elements.parent_id.value="";target.hidden=true;reply.elements.content.focus();});
  }
});

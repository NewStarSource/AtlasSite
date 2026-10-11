// Runs the shipped event handlers against a minimal DOM adapter. This is not browser QA.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

class Element {
  constructor() { this.listeners = {}; this.attributes = {}; this.dataset = {}; this.textContent = ''; this.disabled = false; }
  addEventListener(name, callback) { this.listeners[name] = callback; }
  setAttribute(name, value) { this.attributes[name] = value; }
  removeAttribute(name) { delete this.attributes[name]; }
  fire(name) { return this.listeners[name]?.({preventDefault() {}}); }
}
class FormDataAdapter {
  constructor(form) { this.form = form; }
  *[Symbol.iterator]() { for (const [key, input] of Object.entries(this.form.elements)) yield [key, input.value]; }
}
function run(file, selector, form, fetch, overlays = {}) {
  const document = {
    addEventListener(_name, callback) { callback(); },
    querySelector() { return null; },
    getElementById(id) { return overlays[id]; },
    documentElement: {classList: {add() {}, remove() {}}},
    querySelectorAll(value) { return value === selector ? [form] : []; }
  };
  vm.runInNewContext(fs.readFileSync(file, 'utf8'), {document, fetch, FormData: FormDataAdapter, URLSearchParams, window: {location: {assign() { throw new Error('unexpected navigation'); }}}});
}
async function main() {
  const root = path.resolve(__dirname, '..');
  const form = new Element(), button = new Element(), status = new Element(), label = new Element(), count = new Element();
  form.action = '/api/v1/p/synthetic/bookmark'; form.dataset.stateName = 'bookmarked';
  form.elements = {bookmarked: {value:'true'}, 'gorilla.csrf.Token': {value:'synthetic-csrf'}};
  button.dataset = {labelOn:'已收藏', labelOff:'收藏'};
  button.setAttribute('aria-pressed', 'false'); count.textContent = '2';
  button.querySelector = selector => selector === '[data-action-count]' ? count : label;
  form.querySelector = selector => selector === 'button' ? button : status;
  let requests = 0, resolve;
  run(path.join(root, 'internal/atlas/core.js'), '[data-state-form]', form, async (_url, options) => {
    requests++;
    assert.equal(options.body.get('gorilla.csrf.Token'), 'synthetic-csrf');
    return await new Promise(done => { resolve = done; });
  });
  let pending = form.fire('submit');
  assert.equal(button.disabled, true);
  await form.fire('submit'); assert.equal(requests, 1, 'duplicate clicks must not submit twice');
  resolve({ok:true, json:async () => ({bookmarked:true, count:3})}); await pending;
  assert.equal(form.elements.bookmarked.value, 'false');
  assert.equal(button.attributes['aria-pressed'], 'true'); assert.equal(count.textContent, '3'); assert.equal(button.disabled, false);
  assert.equal(status.textContent, '', 'bookmark success should update the icon without confirmation text');
  pending = form.fire('submit');
  resolve({ok:false, status:403, json:async () => ({message:'请重新登录'})}); await pending;
  assert.equal(form.elements.bookmarked.value, 'false', 'failure must preserve the last confirmed state');
  assert.equal(button.attributes['aria-pressed'], 'true'); assert.equal(count.textContent, '3'); assert.equal(status.textContent, '请重新登录'); assert.equal(button.disabled, false);
  pending = form.fire('submit');
  resolve({ok:true, json:async () => ({bookmarked:false, count:2})}); await pending;
  assert.equal(form.elements.bookmarked.value, 'true'); assert.equal(button.attributes['aria-pressed'], 'false'); assert.equal(count.textContent, '2');
  assert.equal(status.textContent, '', 'bookmark cancellation should stay quiet');
  form.dataset.stateName = 'liked'; form.elements.liked = {value:'true'};
  button.dataset = {labelOn:'取消点赞', labelOff:'点赞'};
  pending = form.fire('submit');
  resolve({ok:true, json:async () => ({liked:true})}); await pending;
  assert.equal(button.attributes['aria-pressed'], 'true'); assert.equal(status.textContent, '', 'like success should stay quiet');
  pending = form.fire('submit');
  resolve({ok:true, json:async () => ({liked:false})}); await pending;
  assert.equal(button.attributes['aria-pressed'], 'false'); assert.equal(status.textContent, '', 'like cancellation should stay quiet');

  const trigger = new Element(), overlay = new Element(), close = new Element();
  trigger.dataset.overlayOpen = 'search-overlay';
  let focused = false;
  trigger.focus = () => { focused = true; };
  overlay.querySelector = () => close;
  overlay.showModal = () => { overlay.open = true; };
  overlay.close = () => { overlay.open = false; overlay.fire('close'); };
  run(path.join(root, 'internal/atlas/core.js'), '[data-overlay-open]', trigger, async () => { throw new Error('unexpected request'); }, {'search-overlay': overlay});
  await trigger.fire('click');
  assert.equal(overlay.open, true); assert.equal(trigger.attributes['aria-expanded'], 'true');
  await close.fire('click');
  assert.equal(overlay.open, false); assert.equal(trigger.attributes['aria-expanded'], 'false'); assert.equal(focused, true);
  await trigger.fire('click');
  overlay.getBoundingClientRect = () => ({left:100, right:500, top:100, bottom:400});
  overlay.listeners.click({target:overlay, clientX:120, clientY:120});
  assert.equal(overlay.open, true, 'clicking empty space inside the panel should not close it');
  overlay.listeners.click({target:overlay, clientX:20, clientY:20});
  assert.equal(overlay.open, false, 'clicking the backdrop should close the panel');

  const login = new Element(), loginButton = new Element(), loginStatus = new Element();
  login.action = '/api/v1/auth/login'; login.elements = {username:{value:'synthetic'}, password:{value:'synthetic-only'}, 'gorilla.csrf.Token':{value:'csrf'}};
  login.querySelector = selector => selector === 'button' ? loginButton : loginStatus;
  run(path.resolve(root, '../StarAccount/internal/account/forms.js'), '[data-account-form]', login, async () => ({ok:false, json:async () => ({message:'无法登录，请检查用户名和密码。'})}));
  await login.fire('submit');
  assert.equal(loginStatus.hidden, false); assert.equal(loginStatus.textContent, '无法登录，请检查用户名和密码。');
  assert.equal(login.elements.username.value, 'synthetic'); assert.equal(login.elements.password.value, 'synthetic-only'); assert.equal(loginButton.disabled, false);
  await login.fire('input'); assert.equal(loginStatus.hidden, true);
  console.log('PASS shipped JavaScript handlers: quiet confirmed state/count, duplicate-submit guard, failed-action recovery, overlay open/close and focus return, login error input retention. Minimal DOM adapter; no graphical browser.');
}
main().catch(error => { console.error(error); process.exitCode = 1; });

// Copyright 2026ff novatechflow (Alexander Alten)
// SPDX-License-Identifier: PolyForm-Shield-1.0.0
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const html = fs.readFileSync('index.html','utf8');
const source = html.slice(html.indexOf('/* Pointer events support'),html.indexOf('/* ---------- pull-to-refresh'));
function fixture(pointer) {
  const listeners = {}, classes = () => ({add(){},remove(){}});
  const list = {children:[],classList:classes(),scrollHeight:500,clientHeight:500,scrollTop:0,
    querySelectorAll(){return this.children.slice();},
    insertBefore(from,to){this.children.splice(this.children.indexOf(from),1); const at=to?this.children.indexOf(to):this.children.length; this.children.splice(at,0,from);},
    addEventListener(name,handler,options){listeners[name]={handler,options};},
    setPointerCapture(id){this.captured=id;},hasPointerCapture(id){return this.captured===id;},releasePointerCapture(){this.captured=null;},
    getBoundingClientRect(){return {top:0,bottom:500};}};
  function tile(id){
    const el={id,parentNode:list,classList:classes(),getAttribute(){return id;},closest(selector){return selector==='.coin'?el:null;}};
    Object.defineProperty(el,'nextSibling',{get(){return list.children[list.children.indexOf(el)+1]||null;}});
    el.handle={closest(selector){return selector==='.coin'?el:selector==='.tile-handle'?el.handle:null;},focus(){}};
    return el;
  }
  list.children=['a','b','c','d'].map(tile);
  const status={}, saved=[], lifecycle={}, timers={}, ctx={tileDrag:null,orderSaving:false,
    window:{PointerEvent:pointer?function(){}:undefined,addEventListener(name,fn){lifecycle[name]=fn;}},
    document:{elementFromPoint(){return ctx.hit;},addEventListener(name,fn){lifecycle[name]=fn;}},
    $:id => id==='cxList'||id==='dashboard'?list:status,
    api(path,opts,cb){saved.push({path,ids:JSON.parse(opts.body).ids});cb({ok:true});},
    pullState(){ctx.pulls=(ctx.pulls||0)+1;},setTimeout(fn,delay){const id=Object.keys(timers).length+Math.random();timers[id]={fn,delay};return id;},clearTimeout(id){delete timers[id];},
    requestAnimationFrame(fn){ctx.frame=fn;return 1;},cancelAnimationFrame(){},
    getComputedStyle(){return {gridTemplateColumns:'200px 200px'};}};
  vm.createContext(ctx);vm.runInContext(source,ctx);
  return {ctx,list,listeners,saved,status,lifecycle,timers,ids:()=>list.children.map(c=>c.id)};
}
function pointerEvent(target){return {target,isPrimary:true,button:0,pointerId:1,clientX:100,clientY:200,preventDefault(){}};}
test('pointer drag persists complete order and releases capture',()=>{
  const f=fixture(true),handle=f.list.children[0].handle;
  f.list.onpointerdown(pointerEvent(handle));
  assert.ok(f.ctx.tileDrag);
  assert.equal(f.ctx.frame,undefined); // stationary drags must not animate
  f.ctx.hit=f.list.children[2];f.list.onpointermove(pointerEvent(handle));
  f.list.onpointerup(pointerEvent(handle));
  assert.deepEqual(f.ids(),['b','c','a','d']);
  assert.deepEqual(f.saved,[{path:'/api/order',ids:['b','c','a','d']}]);
  assert.equal(f.ctx.tileDrag,null);assert.equal(f.list.captured,null);
});
test('iOS 10 touch drag uses non-passive handlers and saves on touch end',()=>{
  const f=fixture(false),handle=f.list.children[0].handle;
  const touch={identifier:7,clientX:100,clientY:200};
  const e={target:handle,touches:[touch],changedTouches:[touch],preventDefault(){}};
  assert.equal(f.listeners.touchstart.options.passive,false);
  assert.equal(f.listeners.touchmove.options.passive,false);
  f.listeners.touchstart.handler(e);assert.equal(f.ctx.frame,undefined);
  f.ctx.hit=f.list.children[3];f.listeners.touchmove.handler(e);
  f.listeners.touchend.handler(e);
  assert.deepEqual(f.ids(),['b','c','d','a']);assert.equal(f.saved.length,1);
});
test('scrolling a tile body does not begin dragging; canceled drags never save',()=>{
  const f=fixture(false),tile=f.list.children[0],touch={identifier:7,clientX:100,clientY:200};
  const e={target:tile,touches:[touch],preventDefault(){}};
  f.listeners.touchstart.handler(e);assert.equal(f.ctx.tileDrag,null);
  e.target=tile.handle;f.listeners.touchstart.handler(e);
  f.ctx.hit=f.list.children[2];f.listeners.touchmove.handler(e);
  f.listeners.touchcancel.handler();assert.equal(f.saved.length,0);assert.ok(f.ctx.pulls);
});
test('keyboard down moves by one grid row',()=>{
  const f=fixture(true),handle=f.list.children[0].handle;
  f.list.onkeydown({target:handle,key:'ArrowDown',preventDefault(){}});
  assert.deepEqual(f.ids(),['b','c','a','d']);assert.equal(f.saved.length,1);
});
test('tablet grid includes iOS 10 spacing syntax',()=>{
  assert.match(html,/#cxList\{[^}]*grid-gap:8px/);
});

test('abandoned legacy touch drag times out without saving or leaving scroll timers',()=>{
  const f=fixture(false),touch={identifier:7,clientX:100,clientY:200};
  f.listeners.touchstart.handler({target:f.list.children[0].handle,touches:[touch],preventDefault(){}});
  const watchdog=Object.values(f.timers).find(t=>t.delay===10000);
  assert.ok(watchdog);watchdog.fn();assert.equal(f.ctx.tileDrag,null);assert.equal(f.saved.length,0);
  assert.equal(Object.values(f.timers).filter(t=>t.delay===100).length,0);
});
test('hidden page or window blur cancels a drag',()=>{
  for(const event of ['blur','pagehide','visibilitychange']){
    const f=fixture(true);f.list.onpointerdown(pointerEvent(f.list.children[0].handle));
    f.ctx.document.hidden=true;f.lifecycle[event]();
    assert.equal(f.ctx.tileDrag,null);assert.equal(f.saved.length,0);
  }
});

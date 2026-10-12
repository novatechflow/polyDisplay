// Copyright 2026ff novatechflow (Alexander Alten)
// SPDX-License-Identifier: PolyForm-Shield-1.0.0
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs'),vm=require('node:vm');
const html=fs.readFileSync('index.html','utf8');
const now=1800000000000;
function tilesFixture(){
 class Node {
  constructor(){this.children=[];this.style={};this.fields={};this.textContent='';this.className='';this.writes=0;}
  set innerHTML(v){this.markup=v;this.writes++;this.children=[];}
  get innerHTML(){return this.markup;}
  querySelector(sel){return this.fields[sel]||(this.fields[sel]=new Node());}
  setAttribute(k,v){this[k]=v;}
  insertBefore(child,before){if(child.parentNode)child.parentNode.removeChild(child);const i=before?this.children.indexOf(before):this.children.length;this.children.splice(i,0,child);child.parentNode=this;}
  removeChild(child){this.children.splice(this.children.indexOf(child),1);child.parentNode=null;}
 }
 const list=new Node();
 const ctx={tileEntries:{},Date:class extends Date{static now(){return now;}},document:{createElement(){return new Node();}},window:{matchMedia(){return {matches:true};}},
  $(){return list;},setText(el,value){if(el.textContent!==value)el.textContent=value;},setHTML(el,value){if(el._html!==value){el.innerHTML=value;el._html=value;}},
  esc:String,fmtUsd:String,fmtPct:String,cls(){return 'up';},chartWithAxis(){return '<svg>candles</svg>';},commodityChart(){return '<svg>commodity</svg>';},
  chartMetrics(){return {plotLo:90,plotRange:20};},chartY(m,p,h){return h-(p-m.plotLo)/m.plotRange*h;}};
 vm.createContext(ctx);vm.runInContext(html.slice(html.indexOf('/* Keep candle SVGs'),html.indexOf('/* ---------- settings')),ctx);
 const coin={id:'bitcoin',sym:'BTC',name:'Bitcoin',price:100,chg24h:1,candlesUpdated:now,candles:[[now-1800000,100,110,90,100],[now-900000,100,110,90,100]]};
 return {ctx,list,coin,render(coins=[coin]){ctx.renderTiles({coins,candleDays:1});}};
}
test('quote updates retain tile and SVG container without rebuilding candles',()=>{
 const f=tilesFixture();f.render();const tile=f.list.children[0],chart=tile.querySelector('.tile-chart');
 assert.equal(chart.writes,1);
 for(let i=0;i<20;i++){f.coin.price=100+i/10;f.render();}
 assert.equal(f.list.children[0],tile);assert.equal(chart.writes,1);
 assert.equal(tile.querySelector('.val').textContent,'101.9');
 assert.equal(tile.querySelector('.ylast').textContent,'101.9');
 f.coin.candlesUpdated+=1000;f.render();assert.equal(chart.writes,2);
 f.coin.price=120;f.render();assert.equal(chart.writes,3,'breakouts must rescale chart');
});
test('custom order moves existing tiles and removing an asset discards its cache',()=>{
 const f=tilesFixture(),other={...f.coin,id:'ethereum',sym:'ETH',name:'Ethereum'};
 f.render([f.coin,other]);const a=f.list.children[0],b=f.list.children[1];
 f.render([other,f.coin]);assert.equal(f.list.children[0],b);assert.equal(f.list.children[1],a);
 f.render([other]);assert.equal(f.list.children.length,1);assert.equal(f.ctx.tileEntries.bitcoin,undefined);
});
test('chart invalidation tracks stale data and closed-candle boundaries',()=>{
 const f=tilesFixture(),key=t=>f.ctx.tileChartKey(f.coin,1,76,t);
 assert.equal(key(now),key(now+1000));
 assert.notEqual(key(now),key(now+900001));
});
function pollingFixture(){
 const timers=new Map(),requests=[],listeners={};let seq=0;
 const ctx={pollTimer:null,clockTimer:null,stateInFlight:false,stateAgain:false,pageSuspended:false,POLL:20000,
  document:{hidden:false,addEventListener(name,fn){listeners[name]=fn;}},window:{addEventListener(name,fn){listeners[name]=fn;}},
  pageActive(){return !ctx.document.hidden&&!ctx.pageSuspended;},
  api(path,opts,cb){requests.push(cb);},setTimeout(fn,delay){timers.set(++seq,{fn,delay});return seq;},clearTimeout(id){timers.delete(id);},clearInterval(id){timers.delete(id);},
  startClock(){},setDot(){},showErr(){},render(){ctx.renders=(ctx.renders||0)+1;}};
 vm.createContext(ctx);vm.runInContext(html.slice(html.indexOf('function schedulePoll(){'),html.indexOf('/* P/L line')),ctx);
 return {ctx,timers,requests,listeners};
}
test('polling has one request/timer and sleeps when hidden',()=>{
 const f=pollingFixture();f.ctx.pullState(true);f.ctx.pullState(true);
 assert.equal(f.requests.length,1);f.requests[0](null);assert.equal(f.timers.size,1);
 f.ctx.document.hidden=true;f.listeners.visibilitychange();assert.equal(f.timers.size,0);
 f.ctx.pullState(true);assert.equal(f.requests.length,1);
 f.ctx.document.hidden=false;f.listeners.visibilitychange();assert.equal(f.requests.length,2);
});
test('late responses do not render a hidden page',()=>{
 const f=pollingFixture();f.ctx.pullState(true);f.ctx.document.hidden=true;
 f.requests[0]({ok:true,json(){throw Error('hidden response should not be decoded');}});
 assert.equal(f.ctx.renders,undefined);assert.equal(f.timers.size,0);assert.equal(f.ctx.stateInFlight,false);
});

test('page renders the version received from server state',()=>{
 const nodes={};
 const ctx={tileDrag:null,orderSaving:false,tileEntries:{},Date,document:{},p2:v=>String(v).padStart(2,"0"),
  $:id=>nodes[id]||(nodes[id]={}),setText(el,v){el.textContent=v;},setHTML(el,v){el.innerHTML=v;}};
 vm.createContext(ctx);vm.runInContext(html.slice(html.indexOf('var tileDrag=null'),html.indexOf('/* Keep candle SVGs')),ctx);
 ctx.renderTiles=()=>{};
 ctx.render({version:'v-test',wallet:'',positions:[],coins:[],candleDays:1});
 assert.equal(nodes.appVer.textContent,'v-test · server');
});

/* Panel IoT bergaya app Android: tile + layar Detail / Scene / Laporan energi.
   Memakai global dari index.html: $, esc, iot, power, energy, hhmm, hhmmss, fmtSpan, fmtAge, fmtCoarse, rssiBars,
   chipText, toast, openChart, chartId. */
"use strict";
const nf=(v,d)=>v.toLocaleString("id-ID",{minimumFractionDigits:d,maximumFractionDigits:d});
const rp=v=>"Rp "+Math.round(v).toLocaleString("id-ID");
const devOf=id=>iot.find(x=>x.id===id);
const isAC=d=>!!d&&(d.power!=null||!!power[d.id]);
const stateTxt=d=>d.state=="on"?"ON":d.state=="off"?"OFF":"—";
const SVG=(p,s)=>`<svg width="${s||22}" height="${s||22}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${p}</svg>`;
const ICO={
  bulb:SVG('<path d="M9 18h6M10 21h4M12 3a6 6 0 0 0-4 10.5c.8.8 1 1.5 1 2.5h6c0-1 .2-1.7 1-2.5A6 6 0 0 0 12 3z"/>'),
  ac:SVG('<rect x="3" y="4" width="18" height="8" rx="2"/><path d="M7 16c0 2-1 3-1 4M12 16c0 2-1 3-1 4M17 16c0 2-1 3-1 4"/>'),
  back:SVG('<path d="M19 12H5M12 19l-7-7 7-7"/>',24),
  sunrise:SVG('<path d="M17 18a5 5 0 0 0-10 0M12 2v7M4.2 10.2l1.4 1.4M1 18h2M21 18h2M18.4 11.6l1.4-1.4M23 22H1M8 6l4-4 4 4"/>',30),
  sunset:SVG('<path d="M17 18a5 5 0 0 0-10 0M12 9V2M4.2 10.2l1.4 1.4M1 18h2M21 18h2M18.4 11.6l1.4-1.4M23 22H1M16 5l-4 4-4-4"/>',30),
  clock:SVG('<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',18)
};
const COL={comp:"#58a6ff",fan:"#2dd4bf",idle:"rgba(139,148,158,.35)",off:"rgba(139,148,158,.22)",gap:"rgba(248,81,73,.45)",on:"rgba(63,185,80,.6)"};
const PSTATE={comp:"Kompresor ON",fan:"Fan saja",idle:"AC mati (standby)",off:"Relay OFF",gap:"Tidak ada data"};
async function api(path,opt){
  const r=await fetch(path,opt);
  let j=null;try{j=await r.json();}catch(x){}
  if(!r.ok){const e=new Error((j&&j.error)||("HTTP "+r.status));e.status=r.status;throw e;}
  return j;
}
const send=(method,path,body)=>api(path,{method,headers:{"Content-Type":"application/json","X-Dashboard":"1"},body:body==null?undefined:JSON.stringify(body)});
const seg=(items,cur,attr,cls)=>`<div class="seg" role="group">${items.map(([v,l])=>`<button type="button" class="${String(v)===String(cur)?"on "+(cls||""):""}" ${attr}="${esc(v)}" aria-pressed="${String(v)===String(cur)}">${esc(l)}</button>`).join("")}</div>`;
const tgl=(on,attr,dis,label)=>`<button type="button" class="tg ${on?"on":""}" role="switch" aria-checked="${!!on}" aria-label="${esc(label)}" ${attr} ${dis?"disabled":""}><i></i></button>`;

/* ---------- Tile ---------- */
const pend={};
function renderIot(){
  const g=$("iot");if(!g)return;
  const now=Date.now();
  g.innerHTML=iot.map(d=>{
    const p=pend[d.id];if(p&&(now-p.t>5000||d.state!==p.from))delete pend[d.id];
    const ac=isAC(d),pl=power[d.id];
    let st;
    if(!d.online)st=`<span class="tx" title="Terakhir terdengar ${d.last_seen?hhmmss(d.last_seen):"-"}">Offline</span>`;
    else if(pl)st=`<span class="tx" data-chip="${esc(d.id)}">${stateTxt(d)}</span>`;
    else if(d.since&&(d.state=="on"||d.state=="off"))st=`<span class="tx" data-since="${d.since}" data-lbl="${stateTxt(d)}">${stateTxt(d)}</span>`;
    else st=`<span class="tx">${stateTxt(d)}</span>`;
    if(d.online&&d.state=="on"&&d.power!=null&&!pl)st=`<span class="tx">ON · ${Math.round(d.power)} W</span>`;
    const on=d.state=="on";
    return `<div class="dv ${d.online?(on?"on":"offs"):"off stale"} ${pend[d.id]?"pend":""}" data-id="${esc(d.id)}" tabindex="0" role="button" aria-label="Buka detail ${esc(d.name)}">
      <img class="ico" src="ico-${ac?"ac":"bulb"}-${d.online&&on?"on":"off"}.png" alt="" width="34" height="34">
      ${tgl(on,`data-tg="${esc(d.id)}"`,!d.online||!!pend[d.id],"Saklar "+d.name)}
      <div><div class="nm">${esc(d.name)}</div><div class="st"><span class="d"></span>${st}${rssiBars(d.rssi)}</div></div></div>`;
  }).join("")||'<div class="mut" style="padding:8px">Belum ada perangkat.</div>';
  $("sIot").textContent=iot.filter(d=>d.state=="on").length;$("sIotN").textContent=iot.length;
  updateChips();updateSince();syncSheet();
}
function updateSince(){
  document.querySelectorAll("[data-since]").forEach(el=>{
    const t=+el.dataset.since;if(t>0){el.textContent=el.dataset.lbl+" · "+fmtCoarse(Date.now()/1000-t);el.title="Detail: "+fmtAge(Date.now()/1000-t);}
  });
}
function tickIot(){updateSince();}
async function toggleDev(id){
  const d=devOf(id);if(!d||!d.online||pend[id])return;
  pend[id]={t:Date.now(),from:d.state};renderIot();
  try{await send("POST","/api/iot/"+encodeURIComponent(id),{cmd:"toggle"});}
  catch(x){toast("Gagal: "+x.message);delete pend[id];}
  setTimeout(()=>{if(pend[id]){delete pend[id];renderIot();}},5200);
  renderIot();
}
$("iot").addEventListener("click",e=>{
  const t=e.target.closest("[data-tg]");if(t){e.stopPropagation();toggleDev(t.dataset.tg);return;}
  const c=e.target.closest(".dv[data-id]");if(c)openSheet("detail",c.dataset.id);
});
$("iot").addEventListener("keydown",e=>{
  if((e.key=="Enter"||e.key==" ")&&e.target.classList.contains("dv")){e.preventDefault();openSheet("detail",e.target.dataset.id);}
});
if($("bAddDev"))$("bAddDev").onclick=()=>openSheet("add");
$("bScene").onclick=()=>openSheet("scene");
$("bRep").onclick=()=>openSheet("report");
$("eTot").onclick=()=>openSheet("report");$("eTot").style.cursor="pointer";

/* energi hari ini di header panel */
async function loadEnergy(){
  try{energy=await api("/api/energy");}catch(x){return false;}
  const t=energy&&energy.total;
  $("eTot").textContent=t&&t.n?`⚡ hari ini ${nf(t.today.kwh,2)} kWh`+(energy.tariff?" · "+rp(t.today.cost):""):"";
  return true;
}
loadEnergy();setInterval(loadEnergy,60000);

/* ---------- Kerangka layar (sheet) ---------- */
let S=null,timers=[];
function clearTimers(){timers.forEach(clearInterval);timers=[];}
function closeSheet(){clearTimers();S=null;$("sheet").classList.remove("open");$("sheet").innerHTML="";closeDlg();if(lastFocus&&lastFocus.focus)lastFocus.focus();}
let lastFocus=null;
function openSheet(kind,arg){
  lastFocus=document.activeElement;
  clearTimers();
  S={kind,id:arg,tab:"usage",period:"today",powH:24,logH:24,onH:24,seq:0,data:{}};
  $("sheet").classList.add("open");
  if(kind=="detail"){buildDetail();}
  else if(kind=="scene"){S.tab="preset";buildScene();}
  else if(kind=="add"){buildAdd();}
  else{S.rid="all";S.sel=null;buildReport();}
}
$("sheet").addEventListener("mousedown",e=>{if(e.target===$("sheet"))closeSheet();});
document.addEventListener("keydown",e=>{
  if(e.key!="Escape"||(typeof chartId!="undefined"&&chartId))return;
  if($("dlg").classList.contains("open"))closeDlg();else if(S)closeSheet();
});
const head=(t1,t2,extra)=>`<div class="shead"><button class="bk" id="sBack" aria-label="Kembali">${ICO.back}</button><div class="ttl"><div class="t1" id="sT1">${t1}</div><div class="t2" id="sT2">${t2||""}</div></div>${extra||""}</div>`;
function shell(headHtml){
  $("sheet").innerHTML=`<div class="sbox" role="dialog" aria-modal="true">${headHtml}<div class="sbody" id="sBody"></div></div>`;
  $("sBack").onclick=closeSheet;$("sBack").focus();
}
function syncSheet(){
  if(!S)return;
  if(S.kind=="detail")syncDetail();
  else if(S.kind=="scene")renderSceneNow();
}
function closeDlg(){$("dlg").classList.remove("open");$("dlg").innerHTML="";}
$("dlg").addEventListener("mousedown",e=>{if(e.target===$("dlg"))closeDlg();});

/* ---------- Timeline ---------- */
function timeline(segs,from,now,colorOf){
  const W=600,H=40,span=Math.max(1,now-from);
  let s=`<svg class="tl" viewBox="0 0 ${W} ${H+16}" role="img" aria-label="Linimasa"><rect x="0" y="0" width="${W}" height="${H}" rx="6" fill="#1b212a"/>`;
  segs.forEach(g=>{
    const x0=Math.max(0,(g.t0-from)/span*W),x1=Math.min(W,(g.t1-from)/span*W);if(x1<=x0)return;
    s+=`<rect x="${x0.toFixed(1)}" y="0" width="${Math.max(.8,x1-x0).toFixed(1)}" height="${H}" fill="${colorOf(g.state)}"><title>${esc(g.state)} ${hhmm(g.t0)}–${hhmm(g.t1)}</title></rect>`;
  });
  const hrs=span/3600,stepH=hrs<=7?1:hrs<=13?2:hrs<=25?4:hrs<=49?8:hrs<=200?24:72;
  const d0=new Date(from*1000);d0.setMinutes(0,0,0);
  for(let t=d0.getTime()/1000;t<=now;t+=3600){
    if(t<from)continue;const dt=new Date(t*1000);if(dt.getHours()%stepH)continue;
    const x=(t-from)/span*W;
    s+=`<line x1="${x}" x2="${x}" y1="${H}" y2="${H+4}" stroke="#444c56"/><text x="${Math.min(W-12,Math.max(12,x))}" y="${H+14}" text-anchor="middle">${hrs>49?(dt.getDate()+"/"+(dt.getMonth()+1)):pad(dt.getHours())}</text>`;
  }
  return s+"</svg>";
}
const tile2=(l,v,sub)=>`<div class="tl2"><small>${esc(l)}</small><b>${v}</b>${sub?`<small>${esc(sub)}</small>`:""}</div>`;
const dayName=["Min","Sen","Sel","Rab","Kam","Jum","Sab"];

/* ---------- Detail ---------- */
const WIDTHS=[500,1000,2000,3000,5000,10000,30000,60000];
const wLbl=w=>w<60000?(w/1000)+" dtk":(w/60000)+" mnt";
function buildDetail(){
  const d=devOf(S.id);if(!d){closeSheet();return;}
  shell(head(esc(d.name),"",`<span id="sSw"></span>`));
  const b=$("sBody");
  b.insertAdjacentHTML("beforebegin",`<div style="padding:0 14px 8px">${seg([["usage","Usage"],["detail","Detail"],["log","Log"]],S.tab,"data-tab")}</div>`);
  b.parentElement.querySelector("[data-tab]").parentElement.parentElement.onclick=e=>{
    const t=e.target.closest("[data-tab]");if(!t)return;S.tab=t.dataset.tab;S.data={};S.seq++;
    t.parentElement.querySelectorAll("button").forEach(x=>{x.classList.toggle("on",x===t);x.setAttribute("aria-pressed",x===t);});
    drawTab();
  };
  S.built=false;syncHeader();drawTab();
  clearTimers();timers.push(setInterval(()=>{if(S&&S.kind=="detail"&&S.tab!="detail")loadTab(true);},20000));
}
function syncHeader(){
  const d=devOf(S.id);if(!d)return;
  $("sT2").innerHTML=(d.online?`<span style="color:${d.state=="on"?"var(--ok)":"var(--mut)"}">${stateTxt(d)}${d.since?" · "+fmtAge(Date.now()/1000-d.since):""}</span>`:'<span style="color:var(--bad)">Offline</span>')+" "+rssiBars(d.rssi);
  const sw=$("sSw");if(sw){
    const on=d.state=="on",dis=!d.online||!!pend[d.id];
    const cur=sw.firstElementChild;
    if(!cur||cur.classList.contains("on")!==on||cur.disabled!==dis){sw.innerHTML=tgl(on,'id="sTg"',dis,"Saklar "+d.name);$("sTg").onclick=()=>toggleDev(d.id);}
  }
}
function syncDetail(){
  syncHeader();
  if(S.tab=="detail")fillDetail();
}
function drawTab(){
  const d=devOf(S.id);if(!d)return;
  const b=$("sBody");
  if(S.tab=="usage"){
    b.innerHTML=`<div id="uEn"></div><div id="uPw"></div>`;renderUsageEnergy();loadTab();
  }else if(S.tab=="detail"){
    b.innerHTML=`<div id="dWrap"><div id="dSet"></div></div>`;buildSettings(d);fillDetail();loadEnergy().then(fillEnergy);
  }else{
    const rg=isAC(d)?[[6,"6j"],[12,"12j"],[24,"24j"],[48,"48j"]]:[[6,"6j"],[24,"24j"],[72,"3h"],[168,"7h"]];
    if(!rg.some(r=>r[0]==S.logH))S.logH=24;
    b.innerHTML=`<div id="lgSeg">${seg(rg,S.logH,"data-lh")}</div><div id="lgBody"><div class="mut small">Memuat…</div></div>`;
    $("lgSeg").onclick=e=>{const t=e.target.closest("[data-lh]");if(!t)return;S.logH=+t.dataset.lh;$("lgSeg").innerHTML=seg(rg,S.logH,"data-lh");loadTab();};
    loadTab();
  }
}
function renderUsageEnergy(){
  const d=devOf(S.id),r=energy&&energy.devices&&energy.devices[S.id];
  const el=$("uEn");if(!el)return;
  const p=r&&r[S.period];
  const bas=r?r.basis:"none";
  const bd=bas=="measured"?'<span class="badge" style="color:var(--ok);border-color:var(--ok)">Terukur</span>':bas=="estimate"?'<span class="badge" style="color:var(--acc);border-color:var(--acc)">Estimasi</span>':'<span class="badge mut" style="border-color:var(--line)">Tak ada data</span>';
  el.innerHTML=`<div class="card"><div class="row sb">${seg([["today","Hari ini"],["week","7 hari"],["month","Bulan ini"]],S.period,"data-pr")}</div>
    <div class="row sb"><span class="mut small">Pemakaian listrik</span>${bd}</div>
    <div class="big"><b>${p?nf(p.kwh,2):"–"}</b><small>kWh</small>${p&&energy.tariff?`<span class="rp">${rp(p.cost)}</span>`:""}</div>
    ${p&&p.unknown_sec>0?`<div class="note">${fmtAge(p.unknown_sec)} tidak terdengar/belum tercatat, tidak ikut dihitung.</div>`:""}
    ${bas=="estimate"&&r.watt?`<div class="note">Estimasi: ${Math.round(r.watt)} W × lama ON.</div>`:""}</div>`;
  el.querySelector(".seg").onclick=e=>{const t=e.target.closest("[data-pr]");if(!t)return;S.period=t.dataset.pr;S.seq++;renderUsageEnergy();if(!isAC(d)){$("uPw").innerHTML='<div class="card mut small">Memuat…</div>';loadTab();}};
}
async function loadTab(quiet){
  const d=devOf(S.id);if(!d)return;
  const id=S.id,seq=S.seq,tab=S.tab;
  try{
    if(tab=="usage"){
      await loadEnergy();renderUsageEnergy();
      if(isAC(d)){
        const h=await api(`/api/power/${encodeURIComponent(id)}?hours=${S.powH}`);
        if(!S||S.seq!=seq||S.id!=id)return;S.data.pow=h;renderPow();
      }else{
        const hrs=periodHours();
        const h=await api(`/api/onoff/${encodeURIComponent(id)}?hours=${hrs}`).catch(x=>x.status==404?emptyOnOff(hrs):Promise.reject(x));
        if(!S||S.seq!=seq||S.id!=id)return;S.data.on=h;S.data.onP=S.period;renderOnoffUsage();
      }
    }else if(tab=="log"){
      let h;
      if(isAC(d))h=await api(`/api/power/${encodeURIComponent(id)}?hours=${S.logH}`);
      else h=await api(`/api/onoff/${encodeURIComponent(id)}?hours=${S.logH}`).catch(x=>emptyOnOff(S.logH));
      if(!S||S.seq!=seq||S.id!=id)return;S.data.log=h;renderLog();
    }
  }catch(x){
    const t=tab=="log"?$("lgBody"):$(isAC(d)?"uPw":"uPw");
    if(t&&!quiet)t.innerHTML=`<div class="card err">Gagal memuat: ${esc(x.message)}</div>`;
  }
}
function emptyOnOff(h){const n=Math.floor(Date.now()/1000);return{now:n,from:n-h*3600,segs:[],events:[],stats:{on_sec:0,off_sec:0,gap_sec:0,toggles:0}};}
function renderPow(){
  const h=S.data.pow,el=$("uPw");if(!h||!el)return;
  const st=h.stats||{};
  el.innerHTML=`<div class="card"><h3>Kompresor</h3>${seg([[1,"1j"],[6,"6j"],[12,"12j"],[24,"24j"],[48,"48j"]],S.powH,"data-ph")}
    ${timeline(h.segs||[],h.from,h.now,s=>COL[s]||COL.gap)}
    <div class="legend">${["comp","fan","idle","off","gap"].map(k=>`<span><i style="background:${COL[k]}"></i>${PSTATE[k]}</span>`).join("")}</div>
    <div class="tiles4">${tile2("Kompresor",fmtAge(st.comp_sec||0),st.duty!=null?Math.round(st.duty*100)+"% waktu ON":"")}${tile2("Siklus",st.cycles||0,st.cycles_per_hour?nf(st.cycles_per_hour,1)+"/jam":"")}${tile2("Fan saja",fmtAge(st.fan_sec||0),"")}${tile2("Daya maks",Math.round(st.max_w||0)+" W","")}</div>
    <div class="row"><button class="btn" id="bZoom">📈 Grafik detail (zoom)</button></div></div>`;
  el.querySelector(".seg").onclick=e=>{const t=e.target.closest("[data-ph]");if(!t)return;S.powH=+t.dataset.ph;S.seq++;el.innerHTML='<div class="card mut small">Memuat…</div>';loadTab();};
  $("bZoom").onclick=()=>openChart(S.id);
}
/* awal periode (detik) dan jumlah jam yang perlu diminta: hari ini = sejak 00:00, 7 hari, bulan ini = sejak tanggal 1 */
function periodStart(p){
  const n=new Date();
  if(p=="week")return Math.floor(n.getTime()/1000)-168*3600;
  const d=new Date(n.getFullYear(),n.getMonth(),p=="month"?1:n.getDate());
  return Math.floor(d.getTime()/1000);
}
function periodHours(){return Math.max(1,Math.min(800,Math.ceil((Date.now()/1000-periodStart(S.period))/3600)));}
const PLBL={today:"Hari ini",week:"7 hari",month:"Bulan ini"};
/* statistik dihitung ulang dari segmen yang dipotong tepat di awal periode */
function onoffClip(h,from){
  const segs=[],st={on_sec:0,off_sec:0,gap_sec:0,toggles:0};
  (h.segs||[]).forEach(g=>{
    const a=Math.max(g.t0,from);if(g.t1<=a)return;segs.push({t0:a,t1:g.t1,state:g.state});
    const d=g.t1-a;if(g.state=="on")st.on_sec+=d;else if(g.state=="off")st.off_sec+=d;else st.gap_sec+=d;
  });
  const ev=h.events||[];
  ev.forEach((e,i)=>{if(e.t>=from&&(e.state=="on"||e.state=="off")&&i>0&&(ev[i-1].state=="on"||ev[i-1].state=="off"))st.toggles++;});
  return{segs,st};
}
function renderOnoffUsage(){
  const h=S.data.on,el=$("uPw");if(!h||!el)return;
  const from=Math.max(h.from,periodStart(S.period)),c=onoffClip(h,from),st=c.st,tot=st.on_sec+st.off_sec;
  el.innerHTML=`<div class="card"><h3>ON / OFF · ${PLBL[S.period]}</h3>${timeline(c.segs,from,h.now,s=>s=="on"?COL.on:s=="off"?COL.off:COL.gap)}
    <div class="legend"><span><i style="background:${COL.on}"></i>ON</span><span><i style="background:${COL.off}"></i>OFF</span><span><i style="background:${COL.gap}"></i>Tidak terdengar</span></div>
    <div class="tiles4">${tile2("Total ON",fmtAge(st.on_sec),tot?Math.round(st.on_sec/tot*100)+"% waktu terpantau":"")}${tile2("Total OFF",fmtAge(st.off_sec),"")}${tile2("Perpindahan",st.toggles,"ON ↔ OFF")}${tile2("Tidak terdengar",fmtAge(st.gap_sec),"")}</div></div>`;
}
function renderLog(){
  const h=S.data.log,el=$("lgBody");if(!h||!el)return;
  const ac=isAC(devOf(S.id)),ev=(h.events||[]).slice().reverse();
  const days=[];
  ev.forEach(e=>{
    const d=new Date(e.t*1000),k=d.getFullYear()+"/"+pad(d.getMonth()+1)+"/"+pad(d.getDate());
    let g=days[days.length-1];if(!g||g.k!=k){g={k,rows:[]};days.push(g);}
    let st,mid,cls;
    if(ac){st=PSTATE[e.state]||e.state;mid=e.w!=null?Math.round(e.w)+" W":"";cls=e.state=="comp"?"on":e.state=="gap"?"gap":"";}
    else{st=e.state=="on"?"On":e.state=="off"?"Off":"Hilang";mid=e.state=="gap"?"Tidak terdengar":(e.src||"—");cls=e.state=="gap"?"gap":"";}
    g.rows.push(`<div class="lrow ${cls}"><span class="ls">${esc(st)}</span><span class="lm" title="${esc(mid)}">${esc(mid)}</span><span class="lt">${hhmmss(e.t)}</span></div>`);
  });
  el.innerHTML=days.length?days.map(g=>`<div class="lday"><div class="ldh"><i></i><b>${g.k}</b></div><div class="lbd">${g.rows.join("")}</div></div>`).join(""):'<div class="mut small">Belum ada kejadian pada rentang ini.</div>';
}

const FR=(lb,ctl,id)=>`<div class="fr"><span class="fl">${lb}</span><span class="fc" ${id?`id="${id}"`:""}>${ctl||""}</span></div>`;
function buildSettings(d){
  const dis=!d.configurable;S.cfgDis=dis;
  const dev=$("dWrap");
  const sc=((S.data.sc&&S.data.sc.scenes)||[]).filter(x=>(x.actions||[]).some(a=>a.device==d.id));
  dev.innerHTML=`<div class="card g" id="dA"></div><div class="card g" id="dScn"></div><div class="card g" id="dSet"></div><div class="card g" id="dEn"></div><div class="card g" id="dInf"></div><div class="card g" id="dDel"></div>`;
  $("dA").innerHTML=FR("Nama perangkat",`<span class="ro">${esc(d.name)}</span>`)+FR("Versi firmware",esc(d.fw||"-"));
  $("dScn").innerHTML=FR("Scene",sc.length?sc.map(x=>`<button type="button" class="pill" data-gsc>${esc(x.name)}</button>`).join(""):'<span class="mut">—</span>');
  $("dScn").querySelectorAll("[data-gsc]").forEach(b=>b.onclick=()=>openSheet("scene"));
  if(!S.data.sc)api("/api/sched").then(j=>{if(S&&S.kind=="detail"&&S.id==d.id&&S.tab=="detail"){S.data.sc=j;buildSettings(devOf(S.id));fillDetail();}}).catch(()=>{});
  $("dSet").innerHTML=FR("Mode inching",`<span id="fPu"></span><select class="sel" id="fWd">${WIDTHS.map(w=>`<option value="${w}">${wLbl(w)}</option>`).join("")}</select>`)+
    FR("Status saat daya menyala",`<select class="sel" id="fSt"><option value="on">Menyala (ON)</option><option value="off">Mati (OFF)</option><option value="stay">Status terakhir</option></select>`)+
    FR("Indikator jaringan",'',"fLe")+
    FR("Log",'<button type="button" class="lnk" id="fLog">Lihat</button>')+
    `<div class="note">${dis?"Pengaturan perangkat POWR belum didukung dari dashboard; nilai hanya dibaca.":"Perubahan langsung dikirim ke perangkat (lokal, tanpa cloud)."}</div>`;
  const metered=d.kind=="switches";
  $("dEn").innerHTML=(metered?FR("Estimasi energi",'<span class="ro">Terukur oleh meter perangkat</span>'):
    FR("Daya beban saat ON (W)",'<input class="inp" id="eW" type="number" inputmode="decimal" min="0" max="5000" step="any" placeholder="mis. 9">')+
    FR("Konsumsi modul Sonoff (W)",'<input class="inp" id="eS" type="number" inputmode="decimal" min="0" max="500" step="any" placeholder="mis. 1">'))+
    FR("Catatan",'<input class="inp" id="eN" maxlength="200" placeholder="mis. lampu LED 9 W">')+
    `<div class="note">${metered?"":"Estimasi kWh = beban × lama ON + konsumsi modul Sonoff × lama perangkat terdengar (ON maupun OFF). Modul Sonoff sendiri memakai energi tambahan walau relay OFF. "}Disimpan di dashboard.</div>
     <div class="row" style="padding:6px 0 10px;gap:10px"><button class="btn pri" id="eSave">Simpan</button><span class="note" id="eMsg"></span></div>`;
  S.enDirty=false;
  $("dEn").addEventListener("input",()=>{S.enDirty=true;$("eMsg").textContent="";});
  $("eSave").onclick=async()=>{
    const num=id=>{const v=$(id);return v?(v.value===""?0:+v.value):0;};
    const dd=energy&&energy.devices&&energy.devices[d.id]||{};
    const body={watt:metered?(dd.watt||0):num("eW"),standby_watt:metered?(dd.standby_watt||0):num("eS"),note:$("eN").value};
    $("eSave").disabled=true;
    try{await send("PUT","/api/energy/cfg/"+encodeURIComponent(d.id),body);S.enDirty=false;$("eMsg").textContent="Tersimpan";await loadEnergy();fillEnergy();}
    catch(x){$("eMsg").textContent="";toast("Gagal: "+x.message);}
    $("eSave").disabled=false;
  };
  const post=async body=>{try{await send("POST","/api/iot/"+encodeURIComponent(d.id)+"/config",body);}catch(x){toast("Gagal: "+x.message);}fillDetail();};
  $("fSt").onchange=e=>post({startup:e.target.value});
  $("fWd").onchange=e=>post({pulse_width:+e.target.value});
  $("fPu").onclick=e=>{
    if(!e.target.closest(".tg")||dis)return;const dd=devOf(S.id),on=dd.pulse=="on";
    if(!on&&!confirm("Inching ON: relay otomatis kembali ke OFF setelah "+wLbl(+$("fWd").value)+" setiap kali dinyalakan. Lanjutkan?"))return;
    post({pulse:on?"off":"on",pulse_width:+$("fWd").value});
  };
  $("fLe").onclick=e=>{if(!e.target.closest(".tg")||dis)return;post({led:devOf(S.id).led=="off"?"on":"off"});};
  $("fLog").onclick=()=>{const t=document.querySelector('[data-tab=log]');if(t)t.click();};
  $("dDel").innerHTML=`<div class="row sb"><div><b style="color:var(--bad)">Hapus Perangkat</b><div class="note" style="margin:2px 0 0">Hapus perangkat ini dari database SQLite</div></div><button type="button" class="btn bad" id="bDelDev">Hapus</button></div>`;
  $("bDelDev").onclick=async()=>{
    if(!confirm(`Hapus perangkat "${d.name}" (${d.id})? Perangkat dapat diadopsi kembali lewat mDNS nanti.`))return;
    try{
      await api("/api/devices/"+encodeURIComponent(d.id),{method:"DELETE",headers:{"X-Dashboard":"1"}});
      closeSheet();
      toast("Perangkat "+d.name+" berhasil dihapus");
    }catch(x){toast("Gagal menghapus: "+x.message);}
  };
}
function fillEnergy(){
  if(S.enDirty||!$("eN"))return;
  const r=energy&&energy.devices&&energy.devices[S.id];if(!r)return;
  const set=(id,v)=>{const el=$(id);if(el&&document.activeElement!==el)el.value=v;};
  set("eW",r.watt||"");set("eS",r.standby_watt||"");set("eN",r.note||"");
}
function fillDetail(){
  const d=devOf(S.id);if(!d||!$("fSt"))return;
  const dis=S.cfgDis;
  if(document.activeElement!==$("fWd")){
    const el=$("fWd");if(d.pulse_width&&![...el.options].some(o=>+o.value==d.pulse_width)){const o=document.createElement("option");o.value=d.pulse_width;o.textContent=wLbl(d.pulse_width);el.appendChild(o);}
    if(d.pulse_width)el.value=d.pulse_width;
  }
  if(document.activeElement!==$("fSt")&&d.startup)$("fSt").value=d.startup;
  $("fWd").disabled=dis||d.pulse!="on";$("fSt").disabled=dis;
  $("fPu").innerHTML=tgl(d.pulse=="on","",dis,"Inching");$("fLe").innerHTML=tgl(d.led!="off","",dis,"Indikator jaringan");
  fillEnergy();
  const row=(k,v)=>FR(k,`<span class="ro">${esc(v)}</span>`);
  const ls=d.last_seen?hhmmss(d.last_seen)+" ("+fmtSpan(Date.now()/1000-d.last_seen)+" lalu)":"-";
  $("dInf").innerHTML=row("ID perangkat",d.id)+row("Model",d.kind=="switches"?"POWR3":"MINI")+row("Produsen","SONOFF")+row("Alamat MAC",(d.mac||"-").toUpperCase())+
    row("Alamat IP",d.ip||"-")+row("WiFi",d.ssid||"-")+row("Sinyal",d.rssi!=null?d.rssi+" dBm":"-")+row("Terakhir terdengar",ls)+
    (d.power!=null?row("Daya beban",Math.round(d.power)+" W")+row("Tegangan",d.voltage!=null?d.voltage.toFixed(0)+" V":"-")+row("Arus",d.current!=null?d.current.toFixed(2)+" A":"-"):"");
}

/* ---------- Scene ---------- */
const daysText=a=>{
  if(!a||!a.length||a.length==7)return"Setiap hari";
  const s=[...a].sort().join();if(s=="1,2,3,4,5")return"Hari kerja";if(s=="0,6")return"Akhir pekan";
  return[1,2,3,4,5,6,0].filter(x=>a.includes(x)).map(x=>dayName[x]).join(", ");
};
const offTxt=o=>o?(o>0?" +":" −")+Math.abs(o)+" mnt":"";
const trigText=t=>t.type=="sunrise"?"Terbit"+offTxt(t.offset):t.type=="sunset"?"Terbenam"+offTxt(t.offset):(t.at||"--:--");
function nextText(ts){
  if(!ts)return"";const d=new Date(ts*1000),n=new Date(),k=x=>x.getFullYear()*400+x.getMonth()*32+x.getDate();
  const tm=pad(d.getHours())+":"+pad(d.getMinutes()),diff=k(d)-k(n);
  if(diff==0)return"hari ini "+tm;if(new Date(n.getTime()+864e5).toDateString()==d.toDateString())return"besok "+tm;
  return pad(d.getDate())+"/"+pad(d.getMonth()+1)+" "+tm;
}
const dName=id=>{const d=devOf(id);return d?d.name:id;};
function buildScene(){
  shell(head("Scene","",`<button class="bk add" id="scAdd" aria-label="Tambah scene">+</button>`));
  $("sBody").insertAdjacentHTML("beforebegin",`<div style="padding:0 14px 8px" id="scTabs"></div>`);
  $("scTabs").onclick=e=>{const t=e.target.closest("[data-sc]");if(t){S.tab=t.dataset.sc;renderSceneNow();}};
  $("scAdd").onclick=()=>editScene(null);
  S.data.sc=null;loadScene();
  timers.push(setInterval(loadScene,30000));
}
async function loadScene(){
  try{const j=await api("/api/sched");if(!S||S.kind!="scene")return;S.data.sc=j;S.data.err=null;}
  catch(x){if(!S||S.kind!="scene")return;S.data.err=x.message;}
  renderSceneNow();
}
function renderSceneNow(){
  if(!S||S.kind!="scene")return;
  const tb=$("scTabs");if(tb)tb.innerHTML=seg([["preset","PRESET"],["log","LOG"]],S.tab,"data-sc");
  const b=$("sBody");if(!b)return;const j=S.data.sc;
  if(!j){b.innerHTML=S.data.err?`<div class="card err">Gagal memuat: ${esc(S.data.err)}</div>`:'<div class="mut small">Memuat…</div>';return;}
  const top=b.scrollTop;
  if(S.tab=="preset"){
    const sun=j.sun;
    let h=sun?`<div class="card"><div class="sun"><div><span style="color:var(--warn)">${ICO.sunrise}</span><div><div class="l">Terbit</div><div class="v">${hhmm(sun.sunrise)}</div></div></div><div><span style="color:#f0883e">${ICO.sunset}</span><div><div class="l">Terbenam</div><div class="v">${hhmm(sun.sunset)}</div></div></div></div></div>`
      :`<div class="card mut small">Terbit/terbenam butuh <code>location</code> di konfigurasi gateway.</div>`;
    h+=(j.scenes||[]).map(s=>{
      const t=(s.triggers||[])[0];
      const ic=t?(t.type=="sunrise"?ICO.sunrise.replace(/30/g,"20"):t.type=="sunset"?ICO.sunset.replace(/30/g,"20"):ICO.clock):"";
      return `<div class="card scn" data-sid="${esc(s.id)}" tabindex="0" role="button" aria-label="Ubah scene ${esc(s.name)}">
        <div class="hd"><span class="t">${esc(s.name)}</span>${tgl(s.enabled,`data-stg="${esc(s.id)}"`,false,"Aktifkan "+s.name)}</div>
        <div class="scl"><span class="k">Jika</span><span style="display:flex;align-items:center;gap:6px">${ic}${esc(t?trigText(t):"-")}</span></div>
        <div class="scl"><span class="k">Maka</span><div class="acts">${(s.actions||[]).map(a=>`<div><span>${esc(dName(a.device))}</span><span class="ac2 ${a.action=="on"?"on":""}">${a.action=="on"?"ON":"OFF"}</span></div>`).join("")}</div></div>
        <div class="ft"><span>${esc(daysText(s.days))}${s.enabled&&s.next?" · berikutnya "+esc(nextText(s.next)):""}</span>
        <div class="r">${s.last_ok?`<span style="color:var(--ok)">✓ ${esc(fmtWhen(s.last_ok))}</span>`:""}${s.last_fail?`<span style="color:var(--bad)">✕ ${esc(fmtWhen(s.last_fail))}</span>`:""}</div></div></div>`;
    }).join("");
    if(!(j.scenes||[]).length)h+='<div class="mut small" style="text-align:center;padding:20px">Belum ada scene. Tekan + untuk menambah.</div>';
    b.innerHTML=h;
    b.querySelectorAll("[data-stg]").forEach(t=>t.onclick=async e=>{
      e.stopPropagation();const s=j.scenes.find(x=>x.id==t.dataset.stg);if(!s)return;
      try{await send("PUT","/api/sched/"+encodeURIComponent(s.id),sceneBody(s,{enabled:!s.enabled}));}catch(x){toast("Gagal: "+x.message);}loadScene();
    });
    b.querySelectorAll("[data-sid]").forEach(c=>{
      const open=()=>editScene(j.scenes.find(x=>x.id==c.dataset.sid));
      c.onclick=e=>{if(!e.target.closest("[data-stg]"))open();};
      c.onkeydown=e=>{if((e.key=="Enter")&&e.target===c)open();};
    });
  }else{
    const runs=j.runs||[],days=[];
    runs.forEach(r=>{
      const d=new Date(r.t*1000),k=d.getFullYear()+"/"+pad(d.getMonth()+1)+"/"+pad(d.getDate());
      let g=days[days.length-1];if(!g||g.k!=k){g={k,rows:[]};days.push(g);}
      const sub=[r.label?esc(r.label):"",r.fire?"dijadwalkan "+hhmm(r.fire):""].filter(Boolean).join(" · ");
      g.rows.push(`<div class="srow ${r.ok?"ok":"bad"}"><span class="ls">${r.ok?"Berhasil":"Gagal"}</span><div class="sm"><div>${esc(r.name||"Scene dihapus")}</div>${sub?`<div class="sub">${sub}</div>`:""}${(r.fails||[]).map(f=>`<div class="sub f">• ${esc(f.name||dName(f.device))}: ${esc(f.err)}</div>`).join("")}${r.err&&!(r.fails||[]).length?`<div class="sub f">${esc(r.err)}</div>`:""}</div><span class="lt">${hhmmss(r.t)}</span></div>`);
    });
    b.innerHTML=days.length?days.map(g=>`<div class="lday"><div class="ldh"><i></i><b>${g.k}</b></div><div class="lbd">${g.rows.join("")}</div></div>`).join(""):'<div class="mut small" style="text-align:center;padding:20px">Belum ada eksekusi scene.</div>';
  }
  b.scrollTop=top;
}
function fmtWhen(t){const d=new Date(t*1000),n=new Date();const tm=hhmm(t);
  if(d.toDateString()==n.toDateString())return"hari ini "+tm;
  if(new Date(n.getTime()-864e5).toDateString()==d.toDateString())return"kemarin "+tm;
  return pad(d.getDate())+"/"+pad(d.getMonth()+1)+" "+tm;}
const sceneBody=(s,over)=>Object.assign({name:s.name,triggers:s.triggers,actions:s.actions,days:s.days||[],enabled:s.enabled},over||{});

function editScene(s){
  const isNew=!s;
  const E=s?JSON.parse(JSON.stringify(s)):{name:"",triggers:[{type:"time",at:"07:00",offset:0}],actions:[],days:[],enabled:true};
  const t=E.triggers[0]||(E.triggers[0]={type:"time",at:"07:00",offset:0});
  const acts={};(E.actions||[]).forEach(a=>acts[a.device]=a.action);
  const days=new Set(E.days||[]);
  const hasLoc=S.data.sc&&S.data.sc.has_location;
  const draw=()=>{
    const [hh,mm]=(t.at||"07:00").split(":").map(Number);
    $("dlg").innerHTML=`<div class="dbox" role="dialog" aria-modal="true" aria-label="${isNew?"Scene baru":"Ubah scene"}">
      <div class="shead"><div class="ttl" style="padding-left:10px"><div class="t1">${isNew?"Scene baru":"Ubah scene"}</div></div></div>
      <div class="dbody">
       <div class="fld"><label for="eNm">Nama</label><input class="inp" id="eNm" maxlength="40" value="${esc(E.name)}"></div>
       <div class="fld"><span class="lbl">Jika (pemicu)</span>${seg([["time","Jam"],["sunrise","Terbit"],["sunset","Terbenam"]],t.type,"data-k")}
        ${t.type=="time"?`<input class="inp" type="time" id="eAt" value="${pad(hh||0)}:${pad(mm||0)}" aria-label="Jam">`
          :`<div class="stepper"><button type="button" data-o="-5" aria-label="Kurangi">−</button><b>${t.offset?(t.offset>0?"+":"−")+Math.abs(t.offset)+" mnt":"tepat"}</b><button type="button" data-o="5" aria-label="Tambah">+</button></div>
            ${hasLoc?"":'<div class="note">Butuh <code>location</code> di konfigurasi gateway agar terbit/terbenam aktif.</div>'}`}</div>
       <div class="fld"><span class="lbl">Maka (perangkat)</span>${iot.map(d=>`<div class="act"><span>${esc(d.name)}</span><div class="seg">${[["","—"],["on","ON"],["off","OFF"]].map(([v,l])=>`<button type="button" data-d="${esc(d.id)}" data-v="${v}" class="${(acts[d.id]||"")==v?"on "+(v?"g":""):""}" aria-pressed="${(acts[d.id]||"")==v}">${l}</button>`).join("")}</div></div>`).join("")||'<div class="mut small">Belum ada perangkat.</div>'}</div>
       <div class="fld"><span class="lbl">Hari <span class="mut">(kosong = setiap hari)</span></span><div class="days">${[1,2,3,4,5,6,0].map(x=>`<button type="button" data-day="${x}" class="${days.has(x)?"on":""}" aria-pressed="${days.has(x)}">${dayName[x]}</button>`).join("")}</div></div>
       <div class="err" id="eErr" role="alert"></div></div>
      <div class="dfoot">${isNew?"":'<button class="btn bad" id="eDel" style="margin-right:auto">Hapus</button>'}<button class="btn" id="eCx">Batal</button><button class="btn pri" id="eOk">Simpan</button></div></div>`;
    const dlg=$("dlg");
    dlg.querySelectorAll("[data-k]").forEach(b=>b.onclick=()=>{keep();t.type=b.dataset.k;if(t.type!="time")t.offset=t.offset||0;draw();});
    dlg.querySelectorAll("[data-o]").forEach(b=>b.onclick=()=>{keep();t.offset=Math.max(-180,Math.min(180,(t.offset||0)+ +b.dataset.o));draw();});
    dlg.querySelectorAll("[data-d]").forEach(b=>b.onclick=()=>{keep();const id=b.dataset.d,v=b.dataset.v;if(v)acts[id]=v;else delete acts[id];draw();});
    dlg.querySelectorAll("[data-day]").forEach(b=>b.onclick=()=>{keep();const x=+b.dataset.day;days.has(x)?days.delete(x):days.add(x);draw();});
    $("eCx").onclick=closeDlg;
    if($("eDel"))$("eDel").onclick=async()=>{
      if(!confirm('Hapus scene "'+E.name+'"?'))return;
      try{await send("DELETE","/api/sched/"+encodeURIComponent(s.id));closeDlg();loadScene();}catch(x){$("eErr").textContent=x.message;}
    };
    $("eOk").onclick=async()=>{
      keep();
      const body={name:E.name.trim(),triggers:[t.type=="time"?{type:"time",at:t.at||"07:00"}:{type:t.type,offset:t.offset||0}],
        actions:iot.filter(d=>acts[d.id]).map(d=>({device:d.id,action:acts[d.id]})),days:[...days].sort(),enabled:E.enabled};
      // aksi pada perangkat yang sudah tidak ada tetap dipertahankan
      (E.actions||[]).forEach(a=>{if(!devOf(a.device)&&acts[a.device])body.actions.push({device:a.device,action:acts[a.device]});});
      if(!body.name){$("eErr").textContent="Nama wajib diisi.";return;}
      if(!body.actions.length){$("eErr").textContent="Pilih minimal satu perangkat (ON/OFF).";return;}
      $("eOk").disabled=true;
      try{await(isNew?send("POST","/api/sched",body):send("PUT","/api/sched/"+encodeURIComponent(s.id),body));closeDlg();loadScene();}
      catch(x){$("eErr").textContent=x.message;$("eOk").disabled=false;}
    };
    $("eNm").focus();
  };
  const keep=()=>{const n=$("eNm");if(n)E.name=n.value;const a=$("eAt");if(a&&a.value)t.at=a.value;};
  $("dlg").classList.add("open");draw();
}

/* ---------- Laporan energi ---------- */
async function buildReport(){
  shell(head("Laporan energi","Pemakaian listrik"));
  $("sBody").innerHTML='<div class="mut small">Memuat…</div>';
  try{
    const [c,m]=await Promise.all([api("/api/energy/compare"),api("/api/energy/monthly?months=6")]);
    if(!S||S.kind!="report")return;S.data.cmp=c;S.data.mon=m;S.sel=S.sel||(m.months[0]&&m.months[0].key);renderReport();
  }catch(x){if($("sBody"))$("sBody").innerHTML=`<div class="card err">Gagal memuat: ${esc(x.message)}</div>`;}
}
const monName=k=>{const[y,m]=k.split("-");return new Date(+y,+m-1,1).toLocaleDateString("id-ID",{month:"long",year:"numeric"});};
const monShort=k=>{const[y,m]=k.split("-");return new Date(+y,+m-1,1).toLocaleDateString("id-ID",{month:"short"});};
function pct(a,b){if(!b)return null;return(a-b)/b*100;}
function pctTxt(p){if(p==null)return"";const dn=p<=0;return`<span style="color:${dn?"var(--ok)":"var(--warn)"}">${dn?"▼":"▲"} ${Math.abs(Math.round(p))}%</span>`;}
function renderReport(){
  const c=S.data.cmp,m=S.data.mon;if(!c||!m||!$("sBody"))return;
  const rid=S.rid,months=m.months.slice().reverse();
  const val=(o)=>o?{kwh:o.kwh||0,cost:o.cost||0}:{kwh:0,cost:0};
  const per=k=>rid=="all"?val(c.total&&c.total[k]):val(c.devices[rid]&&c.devices[rid].periods&&c.devices[rid].periods[k]);
  const mdev=mo=>rid=="all"?val(mo.total):val(mo.devices[rid]);
  const tar=m.tariff;
  const cmp=(l,a,b,la,lb)=>{const x=per(a),y=per(b);return`<div class="cmp"><div class="a"><div class="l">${l}</div><div class="v">${nf(x.kwh,2)} <small>kWh</small></div></div><div class="b"><div class="p">${pctTxt(pct(x.kwh,y.kwh))}</div><div>${lb}: ${nf(y.kwh,2)} kWh</div></div></div>`;};
  const mx=Math.max(.01,...months.map(x=>mdev(x).kwh));
  const sel=months.find(x=>x.key==S.sel)||months[months.length-1];
  const si=months.indexOf(sel),prev=si>0?months[si-1]:null;
  let h=`<select class="sel" id="rDev" aria-label="Perangkat"><option value="all">Semua perangkat</option>${iot.map(d=>`<option value="${esc(d.id)}" ${d.id==rid?"selected":""}>${esc(d.name)}</option>`).join("")}</select>
   <div class="card"><h3>Perbandingan</h3>${cmp("Hari ini","today","yesterday","Hari ini","Kemarin")}${cmp("Minggu ini","week","last_week","","Minggu lalu")}${cmp("Bulan ini","month","last_month","","Bulan lalu")}
    <div class="note">Periode berjalan dibanding periode sebelumnya yang penuh. Minggu dimulai Senin.</div></div>`;
  if(sel){
    const v=mdev(sel),pv=prev?mdev(prev):null;
    h+=`<div class="card"><h3>6 bulan terakhir</h3><div class="mbar" role="group" aria-label="Pilih bulan">${months.map(x=>`<button data-mo="${x.key}" class="${x.key==sel.key?"on":""}" aria-label="${monName(x.key)}: ${nf(mdev(x).kwh,1)} kWh"><i style="height:${Math.max(2,mdev(x).kwh/mx*100)}%"></i></button>`).join("")}</div>
      <div class="mlab">${months.map(x=>`<span class="${x.key==sel.key?"on":""}">${monShort(x.key)}</span>`).join("")}</div></div>
     <div class="card"><div class="row sb"><h3 style="text-transform:capitalize">${monName(sel.key)}</h3>${sel.partial?'<span class="badge" style="color:var(--acc);border-color:var(--acc)">berjalan</span>':""}</div>
      <div class="big"><b>${nf(v.kwh,2)}</b><small>kWh</small>${tar?`<span class="rp">${rp(v.cost)}</span>`:""}</div>
      ${pv&&pv.kwh>0?`<div class="small mut">${pctTxt(pct(v.kwh,pv.kwh))} dibanding ${monName(prev.key)} (${nf(pv.kwh,2)} kWh)${sel.partial?" — bulan berjalan":""}</div>`:""}</div>
     <div class="card"><h3>Per perangkat</h3>${(rid=="all"?iot:iot.filter(d=>d.id==rid)).map(d=>{const md=sel.devices[d.id];if(!md)return"";
       const mm=Math.max(.01,...iot.map(x=>(sel.devices[x.id]||{}).kwh||0));
       const note=!md.known?"tak ada data":md.basis=="measured"?"Terukur"+(md.days?" · "+md.days+" hari tercatat":""):"Estimasi"+(md.unknown_sec?" · "+fmtAge(md.unknown_sec)+" tak diketahui":"");
       return`<div class="pdv"><div class="r"><span>${esc(d.name)}</span><span>${md.known?nf(md.kwh,2)+" kWh":"–"}</span></div><div class="dbar"><i class="${md.basis=="measured"?"m":""}" style="width:${md.known?Math.max(1,md.kwh/mm*100):0}%"></i></div><div class="n"><span>${esc(note)}</span><span>${md.known&&tar?rp(md.cost):""}</span></div></div>`;}).join("")}
      <div class="note">Estimasi = daya (W) × lama ON dari riwayat; Terukur = meter perangkat (POWR). Bulan sebelum dashboard mencatat tidak ikut terhitung.</div></div>`;
  }
  const b=$("sBody"),top=b.scrollTop;b.innerHTML=h;b.scrollTop=top;
  $("rDev").onchange=e=>{S.rid=e.target.value;renderReport();};
  b.querySelectorAll("[data-mo]").forEach(x=>x.onclick=()=>{S.sel=x.dataset.mo;renderReport();});
}

/* ---------- Tambah / Discovery / Adopsi Perangkat ---------- */
let discoveredList = [];

async function loadDiscovered() {
  try {
    discoveredList = await api("/api/devices/discovered");
    renderDiscovered();
  } catch (x) {}
}

function renderDiscovered() {
  const el = $("discList");
  if (!el) return;
  if (!discoveredList || !discoveredList.length) {
    el.innerHTML = `
      <div class="mut small" style="padding:14px;text-align:center;background:var(--bg);border-radius:12px;border:1px dashed var(--line)">
        📡 Belum ada perangkat Sonoff baru terdeteksi via mDNS.<br>
        <span class="xs" style="color:var(--mut)">Pastikan perangkat Sonoff menyala di jaringan WiFi LAN yang sama.</span>
      </div>`;
    return;
  }
  el.innerHTML = discoveredList.map(d => `
    <div class="card" style="border-color:var(--acc);background:rgba(88,166,255,0.06);padding:12px">
      <div class="row sb">
        <div>
          <b style="font-size:14px">${esc(d.id)}</b>
          <span class="badge" style="color:var(--acc);border-color:var(--acc);margin-left:6px">${esc(d.type || "Sonoff")}</span>
        </div>
        <button type="button" class="btn pri" data-adopt="${esc(d.id)}" style="min-height:28px;padding:3px 12px">Adopsi</button>
      </div>
      <div class="mut xs" style="display:flex;gap:12px;margin-top:6px;flex-wrap:wrap">
        <span>IP: <b class="mono" style="color:var(--txt)">${esc(d.ip || "-")}</b></span>
        ${d.mac ? `<span>MAC: <b class="mono" style="color:var(--txt)">${esc(d.mac.toUpperCase())}</b></span>` : ""}
        <span>Terlihat: ${fmtAge(Date.now()/1000 - d.last_seen)} lalu</span>
      </div>
    </div>
  `).join("");

  el.querySelectorAll("[data-adopt]").forEach(btn => {
    btn.onclick = () => {
      const id = btn.dataset.adopt;
      const target = discoveredList.find(x => x.id === id);
      if (!target) return;
      selectAdopt(target);
    };
  });
}

function selectAdopt(d) {
  $("formTtl").textContent = "Adopsi Perangkat: " + d.id;
  $("fDevId").value = d.id;
  $("fDevId").disabled = true;
  $("fDevIp").value = d.ip || "";
  $("fDevMac").value = (d.mac || "").toUpperCase();
  if (d.type && d.type.includes("switches")) {
    $("fDevKind").value = "switches";
  } else {
    $("fDevKind").value = "switch";
  }
  $("fDevName").focus();
  $("btnManualMode").style.display = "inline-block";
}

function resetManualForm() {
  $("formTtl").textContent = "Tambah Perangkat Baru";
  $("fDevId").value = "";
  $("fDevId").disabled = false;
  $("fDevName").value = "";
  $("fDevKey").value = "";
  $("fDevIp").value = "";
  $("fDevMac").value = "";
  $("fDevKind").value = "switch";
  $("fDevWatt").value = "";
  $("fDevStandby").value = "";
  $("btnManualMode").style.display = "none";
}

function buildAdd() {
  shell(head("Tambah Perangkat IoT", "mDNS Discovery & Adopsi"));
  const b = $("sBody");
  b.innerHTML = `
    <div class="card">
      <div class="row sb" style="margin-bottom:4px">
        <h3>🔍 Perangkat Ditemukan (mDNS)</h3>
        <button type="button" class="btn" id="bScan" style="min-height:28px;padding:3px 10px;font-size:12px">🔄 Cari Ulang</button>
      </div>
      <div id="discList" style="display:flex;flex-direction:column;gap:8px">
        <div class="mut small" style="padding:10px;text-align:center">Memuat perangkat ditemukan...</div>
      </div>
    </div>

    <div class="card" id="formCard">
      <div class="row sb" style="margin-bottom:8px">
        <h3 id="formTtl">Form Adopsi & Pengaturan</h3>
        <button type="button" class="btn" id="btnManualMode" style="display:none;min-height:26px;padding:2px 8px;font-size:11px">Mode Manual</button>
      </div>
      <form id="fAddDev" style="display:flex;flex-direction:column;gap:10px" onsubmit="return false">
        <div>
          <div class="frow" style="margin-bottom:4px"><span class="lb">ID Perangkat</span><span class="ds">ID unik eWeLink</span></div>
          <input type="text" id="fDevId" class="sel" placeholder="Contoh: 1000c47454" required>
        </div>
        <div>
          <div class="frow" style="margin-bottom:4px"><span class="lb">Nama Perangkat</span><span class="ds">Nama tampilan</span></div>
          <input type="text" id="fDevName" class="sel" placeholder="Contoh: Lampu Teras, AC Kamar" required>
        </div>
        <div>
          <div class="frow" style="margin-bottom:4px"><span class="lb">Device Key (API Key)</span><span class="ds">32 karakter hex</span></div>
          <input type="text" id="fDevKey" class="sel mono" placeholder="Kunci enkripsi LAN dari aplikasi eWeLink" required autocomplete="off">
          <div class="mut xs" style="margin-top:3px">Kunci enkripsi diperoleh saat perangkat dipairing ke aplikasi eWeLink atau mode DIY.</div>
        </div>
        <div style="display:grid;grid-template-columns:1fr 1fr;gap:10px">
          <div>
            <div class="frow" style="margin-bottom:4px"><span class="lb">IP Address</span></div>
            <input type="text" id="fDevIp" class="sel mono" placeholder="10.142.27.x">
          </div>
          <div>
            <div class="frow" style="margin-bottom:4px"><span class="lb">MAC Address</span></div>
            <input type="text" id="fDevMac" class="sel mono" placeholder="E0:98:...">
          </div>
        </div>
        <div>
          <div class="frow" style="margin-bottom:4px"><span class="lb">Jenis Perangkat</span></div>
          <select id="fDevKind" class="sel">
            <option value="switch">Saklar Biasa / Sonoff Mini (1-gang)</option>
            <option value="switches">Saklar Daya / Sonoff POWR3 (dengan meteran listrik)</option>
          </select>
        </div>
        <div style="display:grid;grid-template-columns:1fr 1fr;gap:10px">
          <div>
            <div class="frow" style="margin-bottom:4px"><span class="lb">Daya Beban ON (W)</span><span class="ds">Opsional</span></div>
            <input type="number" id="fDevWatt" class="sel" placeholder="mis. 12" min="0" step="any">
          </div>
          <div>
            <div class="frow" style="margin-bottom:4px"><span class="lb">Standby (W)</span><span class="ds">Opsional</span></div>
            <input type="number" id="fDevStandby" class="sel" placeholder="mis. 0.5" min="0" step="any">
          </div>
        </div>
        <div class="row" style="margin-top:6px;gap:10px">
          <button type="submit" class="btn pri" id="bSubmitDev" style="flex:1">Simpan & Hubungkan</button>
          <button type="button" class="btn" id="bCancelAdd">Batal</button>
        </div>
      </form>
    </div>
  `;

  $("bScan").onclick = async () => {
    $("bScan").disabled = true;
    $("bScan").textContent = "⏳ Memindai...";
    try {
      await send("POST", "/api/devices/discover");
      await loadDiscovered();
    } catch (x) {
      toast("Gagal query mDNS: " + x.message);
    }
    setTimeout(() => {
      if ($("bScan")) {
        $("bScan").disabled = false;
        $("bScan").textContent = "🔄 Cari Ulang";
      }
    }, 1200);
  };

  $("btnManualMode").onclick = resetManualForm;
  $("bCancelAdd").onclick = closeSheet;

  $("fAddDev").onsubmit = async (e) => {
    e.preventDefault();
    const id = $("fDevId").value.trim();
    const name = $("fDevName").value.trim();
    const key = $("fDevKey").value.trim();
    if (!id || !name || !key) {
      toast("Mohon lengkapi ID, Nama, dan Device Key");
      return;
    }
    const dev = {
      id: id,
      name: name,
      key: key,
      ip: $("fDevIp").value.trim(),
      mac: $("fDevMac").value.trim().toLowerCase(),
      kind: $("fDevKind").value,
      watt: $("fDevWatt").value === "" ? 0 : +$("fDevWatt").value,
      standby_watt: $("fDevStandby").value === "" ? 0 : +$("fDevStandby").value
    };

    $("bSubmitDev").disabled = true;
    $("bSubmitDev").textContent = "Menyimpan...";
    try {
      await send("POST", "/api/devices", dev);
      closeSheet();
      toast("Perangkat " + name + " berhasil ditambahkan!");
      loadEnergy();
    } catch (x) {
      toast("Gagal menyimpan: " + x.message);
      $("bSubmitDev").disabled = false;
      $("bSubmitDev").textContent = "Simpan & Hubungkan";
    }
  };

  loadDiscovered();
  clearTimers();
  timers.push(setInterval(loadDiscovered, 3000));
}

renderIot();

// l402-selftest.mjs — offline self-test of l402.mjs and l402-mock.mjs: no network, no wallet, no money.
// Uses fake (unsigned) invoices and a fake hub that applies the same budget rule as the hub
// (fee reserve max(10 sats, 1%)). Usage: node l402-selftest.mjs   → prints PASS/FAIL lines, ends with ALL PASS.
import { createHash, randomBytes } from "node:crypto";
import { runL402, decodeBolt11 } from "./l402.mjs";
import { createL402Server } from "./l402-mock.mjs";
const CH="qpzry9x8gf2tvdw0s3jn54khce6mua7l";
function polymod(v){const G=[0x3b6a57b2,0x26508e6d,0x1ea119fa,0x3d4233dd,0x2a1462b3];let c=1;for(const x of v){const b=c>>25;c=((c&0x1ffffff)<<5)^x;for(let i=0;i<5;i++)if((b>>i)&1)c^=G[i];}return c;}
const hx=h=>[...h].map(c=>c.charCodeAt(0)>>5).concat([0],[...h].map(c=>c.charCodeAt(0)&31));
const words=(buf)=>{let a=0,b=0;const o=[];for(const x of buf){a=(a<<8)|x;b+=8;while(b>=5){b-=5;o.push((a>>b)&31);}a&=(1<<b)-1;}if(b>0)o.push((a<<(5-b))&31);return o;};
const tag=(t,w)=>[t,w.length>>5,w.length&31,...w];
const preimages=new Map();
function fakeInvoice(sats,net="bc"){const pre=randomBytes(32);const ph=createHash("sha256").update(pre).digest();preimages.set(ph.toString("hex"),pre.toString("hex"));
 const hrp=`ln${net}${sats*10}n`;let ts=Math.floor(Date.now()/1000);const tw=[];for(let i=0;i<7;i++){tw.unshift(ts&31);ts=Math.floor(ts/32);}
 const d=[...tw,...tag(1,words(ph)),...tag(6,[0,18,24].slice(-2)),...tag(13,words(Buffer.from("mock"))),...Array(104).fill(0)];
 let chk=polymod(hx(hrp).concat(d,[0,0,0,0,0,0]))^1;const cs=[];for(let i=0;i<6;i++)cs.push((chk>>(5*(5-i)))&31);
 return hrp+"1"+d.concat(cs).map(x=>CH[x]).join("");}
// fake hub: budget accounting like transactions_service.go (reserve max(10 sats,1%)), pays by preimage lookup
function fakeHub(budgetSats,{badPreimage=false}={}){let usedMsat=0;return{payInvoice:async({invoice})=>{const inv=decodeBolt11(invoice);const amt=Number(inv.amountMsat);const res=Math.max(Math.ceil(amt*0.01),10000);
 if(Math.floor((amt+res)/1000)>budgetSats-Math.floor(usedMsat/1000)){const e=new Error("budget exceeded");e.code="QUOTA_EXCEEDED";throw e;}
 usedMsat+=amt+1000;return{preimage:badPreimage?"00".repeat(32):preimages.get(inv.paymentHash),fees_paid:1000};},
 getBudget:async()=>({used_budget:usedMsat,total_budget:budgetSats*1000}),setBudget:(b)=>{budgetSats=b;}};}
const srv=createL402Server({priceSats:5,getInvoice:async(s)=>fakeInvoice(s)});await new Promise(r=>srv.listen(18402,"127.0.0.1",r));
const url="http://127.0.0.1:18402/l402/ping";const hub=fakeHub(20000);const R=[];const check=(n,c)=>{R.push([n,c]);console.log(c?"PASS":"FAIL",n);};
for(let i=0;i<3;i++){const r=await runL402({url,payer:hub,expectSats:5});check(`call ${i+1} ok, preimage ok, budget +6000 msat`,r.ok&&r.preimage_ok&&r.budget_after.used_budget-r.budget_before.used_budget===6000&&r.credential_binds_hash===false||r.ok&&r.preimage_ok&&r.budget_after.used_budget-r.budget_before.used_budget===6000);}
const used=(await hub.getBudget()).used_budget/1000;hub.setBudget(used+15);
const r1=await runL402({url,payer:hub,expectSats:5});check("headroom 15: 5-sat call needs 15 -> allowed",r1.ok);
const r2=await runL402({url,payer:hub,expectSats:5});check("over budget -> refused QUOTA_EXCEEDED, not paid",!r2.ok&&r2.paid===false&&r2.error_code==="QUOTA_EXCEEDED");
const r3=await runL402({url,payer:fakeHub(100,{badPreimage:true})});check("bad preimage -> token not presented",!r3.ok&&r3.preimage_ok===false&&r3.status_after===undefined);
const r4=await runL402({url,payer:hub,maxSats:4});check("price above max-sats -> refused before paying",!r4.ok&&r4.paid===undefined);
const r5=await runL402({url,payer:hub,dryRun:true});check("dry run never pays",r5.ok&&r5.dry_run&&r5.paid===undefined);
// forged token / wrong preimage against the server
const c=await fetch(url);const h=c.headers.get("www-authenticate");const tok=h.match(/macaroon="([^"]+)"/)[1];
const bad=await fetch(url,{headers:{Authorization:`L402 ${tok}:${"11".repeat(32)}`}});check("server rejects wrong preimage (401)",bad.status===401);
const inv=decodeBolt11(h.match(/invoice="([^"]+)"/)[1]);const pre=preimages.get(inv.paymentHash);
const forged=tok.split(".")[0]+"."+Buffer.alloc(32).toString("base64url");
const f=await fetch(url,{headers:{Authorization:`L402 ${forged}:${pre}`}});check("server rejects forged HMAC (401)",f.status===401);
const g=await fetch(url,{headers:{Authorization:`L402 ${tok}:${pre}`}});check("server accepts valid token+preimage (200)",g.status===200);
const srv2=createL402Server({priceSats:5,getInvoice:async(s)=>fakeInvoice(s,"tb")});await new Promise(r=>srv2.listen(18403,"127.0.0.1",r));
const w=await fetch("http://127.0.0.1:18403/l402/ping");check("mock refuses testnet invoice from wallet (502)",w.status===502);
srv.close();srv2.close();console.log(R.every(x=>x[1])?"ALL PASS":"SOME FAILED");process.exit(0);

(function(){
function esc(s){return String(s).replace(/[&<>"]/g,function(c){return{'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]})}
function bps(n){var u=['bps','kbps','Mbps','Gbps','Tbps'],i=0;while(n>=1000&&i<4){n/=1000;i++}
  return (i?(n>=100?n.toFixed(0):n>=10?n.toFixed(1):n.toFixed(2)):String(Math.round(n)))+' '+u[i]}
function bytes(n){var u=['B','KB','MB','GB','TB'],i=0;while(n>=1024&&i<4){n/=1024;i++}return (i?n.toFixed(1):n)+' '+u[i]}
var sel=document.getElementById('tdev'),tb=document.querySelector('#ttb tbody'),er=document.getElementById('terr'),inf=document.getElementById('tinfo'),busy=false;
var saved=sessionStorage.getItem('bvtdev');
if(saved&&[].some.call(sel.options,function(o){return o.value===saved}))sel.value=saved;
else{var pick=[].find.call(sel.options,function(o){return /^(ens|eth|enp|eno)/.test(o.value)});if(pick)sel.value=pick.value}
sel.onchange=function(){sessionStorage.setItem('bvtdev',sel.value);tb.innerHTML='';poll()};
function poll(){
  if(busy||document.hidden||!sel.value)return;busy=true;
  fetch('/api/traffic?dev='+encodeURIComponent(sel.value),{cache:'no-store',credentials:'same-origin'}).then(function(r){
    if(r.status===401||r.redirected){location='/login';throw 0}return r.json().then(function(d){return{ok:r.ok,d:d}})}).then(function(x){
    busy=false;er.hidden=x.ok;if(!x.ok){er.textContent=x.d.error||'error';return}
    var d=x.d;inf.textContent=d.warming?'collecting...':'total '+bps(d.total_bps)+' · '+d.rows.length+' flows';
    tb.innerHTML=d.rows.length?d.rows.map(function(r){return '<tr><td>'+esc(r.src)+'</td><td>'+esc(r.dst)+'</td><td>'+esc(r.proto)+'</td><td class="n">'+(r.port||'')+'</td><td class="n">'+bps(r.r2)+'</td><td class="n">'+bps(r.r10)+'</td><td class="n">'+bps(r.r40)+'</td><td class="n">'+bytes(r.total)+'</td></tr>'}).join(''):'<tr><td colspan="8" style="color:#888">'+(d.warming?'collecting...':'no traffic seen')+'</td></tr>';
  }).catch(function(){busy=false});
}
poll();setInterval(poll,2000);
})();

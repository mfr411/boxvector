// VPP interface editing: toolbar on the selected row + commit-confirm banner.
(function(){
var add=document.getElementById('vpadd'),tb=document.getElementById('vptb'),bn=document.getElementById('vpbn'),bt=document.getElementById('vpbt');
if(!add)return;
var csrf=(document.querySelector('input[name=csrf]')||{}).value||'',can=false,pend=null,sel='',dl=0;
var B={ip:'vp-ip',dip:'vp-dip',mt:'vp-mtu',st:'vp-st',dv:'vp-del'};for(var k in B)B[k]=document.getElementById(B[k]);
function open(path,title){if(window.bvOpen)window.bvOpen(path,title,path.split('?')[0])}
function post(op){return fetch('/api/vppconfirm',{method:'POST',credentials:'same-origin',body:new URLSearchParams({op:op,csrf:csrf})}).then(function(r){return r.json()})}
function status(){
  fetch('/api/vppstatus',{cache:'no-store',credentials:'same-origin'}).then(function(r){return r.json()}).then(function(s){
    can=!!(s.helper&&s.can&&s.vppcfg&&s.vpp);add.hidden=!can;tb.hidden=!can;
    pend=s.pending||null;dl=pend?new Date(pend.deadline).getTime():0;draw()}).catch(function(){})}
function draw(){
  if(!pend){bn.hidden=true;return}
  var left=Math.max(0,Math.round((dl-Date.now())/1000));
  bn.hidden=false;bt.textContent=pend.summary+' applied. Confirm within '+left+' s or it reverts automatically.';
}
function sync(){var on=!!sel&&can,sub=on&&sel.indexOf('.')>0;[B.ip,B.dip,B.mt,B.st].forEach(function(b){b.disabled=!on});B.dv.disabled=!sub}
document.querySelector('#vif tbody').addEventListener('click',function(e){var tr=e.target.closest('tr');sel=tr?tr.dataset.n||'':'';sync()});
function go(b,op,t){b.onclick=function(){if(sel)open('/vppif/act?op='+op+'&dev='+encodeURIComponent(sel),t+' · '+sel)}}
go(B.ip,'addaddr','add address');go(B.dip,'deladdr','remove address');go(B.mt,'mtu','set MTU');go(B.st,'state','set state');go(B.dv,'del','delete');
add.onclick=function(e){e.preventDefault();open('/vppif/new','add sub-interface')};
document.getElementById('vpok').onclick=function(){post('confirm').then(function(r){if(r.error)alert(r.error);status()})};
document.getElementById('vprv').onclick=function(){if(confirm('Revert the pending change now?'))post('revert').then(function(r){if(r.error)alert(r.error);status()})};
status();setInterval(status,3000);setInterval(draw,1000);
})();

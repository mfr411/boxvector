// Interface editing controls: toolbar actions on the selected row and the commit-confirm banner.
(function(){
var add=document.getElementById('ifadd'),tb=document.getElementById('iftb'),bn=document.getElementById('ifbn'),bt=document.getElementById('ifbt');
if(!add)return;
var csrf=(document.querySelector('input[name=csrf]')||{}).value||'',can=false,pend=null,sel='',dl=0;
var ip=document.getElementById('if-ip'),mt=document.getElementById('if-mtu'),dv=document.getElementById('if-del');
var body=document.querySelector('#ift tbody');
function open(path,title){if(window.bvOpen)window.bvOpen(path,title,path.split('?')[0].replace('/act','/act'))}
function post(op){return fetch('/api/ifconfirm',{method:'POST',credentials:'same-origin',body:new URLSearchParams({op:op,csrf:csrf})}).then(function(r){return r.json()})}
function status(){
  fetch('/api/ifstatus',{cache:'no-store',credentials:'same-origin'}).then(function(r){return r.json()}).then(function(s){
    can=!!(s.helper&&s.can&&s.ifupdown2);add.hidden=!can;tb.hidden=!can;
    pend=s.pending||null;dl=pend?new Date(pend.deadline).getTime():0;draw()}).catch(function(){})}
function draw(){
  if(!pend){bn.hidden=true;return}
  var left=Math.max(0,Math.round((dl-Date.now())/1000));
  bn.hidden=false;bt.textContent=pend.summary+' applied. Confirm within '+left+' s or it reverts automatically.';
}
function sync(){var on=!!sel&&can;[ip,mt,dv].forEach(function(b){b.disabled=!on})}
body.addEventListener('click',function(e){var tr=e.target.closest('tr');sel=tr?tr.dataset.n||'':'';sync()});
ip.onclick=function(){if(sel)open('/ifaces/act?op=addaddr&dev='+encodeURIComponent(sel),'add address · '+sel)};
mt.onclick=function(){if(sel)open('/ifaces/act?op=mtu&dev='+encodeURIComponent(sel),'set MTU · '+sel)};
dv.onclick=function(){if(sel)open('/ifaces/act?op=del&dev='+encodeURIComponent(sel),'delete · '+sel)};
document.getElementById('ifok').onclick=function(){post('confirm').then(function(r){if(r.error)alert(r.error);status()})};
document.getElementById('ifrv').onclick=function(){if(confirm('Revert the pending change now?'))post('revert').then(function(r){if(r.error)alert(r.error);status()})};
status();setInterval(status,3000);setInterval(draw,1000);
})();

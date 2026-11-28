/* Peers master/detail: renders the selected peer on the right. Actions reuse the hidden toolbar buttons. */
(function(){
var tb=document.querySelector('#peers tbody'),pd=document.getElementById('pd');if(!tb||!pd)return;
var las=pd.dataset.las||'';
function esc(s){return String(s).replace(/[&<>"]/g,function(c){return{'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]})}
function num(v){return v==='-1'||v===''?'-':v}
function rows(){return[].filter.call(tb.rows,function(r){return r.dataset.n})}
function addNb(){rows().forEach(function(r){var td=r.cells[1];if(td&&!td.querySelector('.nbl')){var s=document.createElement('span');s.className='nbl';s.textContent=r.dataset.nb;td.appendChild(s)}})}
function cur(){return tb.querySelector('tr.sel')}
var last='';
function render(){
  addNb();var r=cur();
  if(!r){var v=rows().filter(function(x){return x.style.display!=='none'})[0];if(v){r=v;r.classList.add('sel');document.dispatchEvent(new Event('bvsel'))}}
  if(!r){if(last!=='none'){pd.innerHTML='<div class="none">No peers match.</div>';last='none'}return}
  var d=r.dataset,v6=d.nb.indexOf(':')>=0,off=d.off==='1',up=d.up==='1';
  var cl=off?'o':(up?'':(/Idle|Active|Connect|OpenSent|OpenConfirm|Down/i.test(d.st)?'d':'w'));
  var sess=(las&&d.as&&String(d.as)===String(las))?'iBGP':'eBGP';
  var h='<h2>'+esc(d.nm)+'<span class="pill '+cl+'">'+esc(off?'Disabled':d.st)+'</span></h2>'+
   '<div class="sub">AS'+esc(las||'?')+' ⇄ AS'+esc(d.as)+' · '+(v6?'IPv6':'IPv4')+(d.since?' · since '+esc(d.since):'')+(d.desc?' · '+esc(d.desc):'')+'</div>'+
   '<div class="cards"><div><small>Imported</small><b>'+num(d.im)+'</b></div><div><small>Exported</small><b>'+num(d.ex)+'</b></div><div><small>Neighbor</small><b class="m">'+esc(d.nb)+'</b></div><div><small>Session</small><b style="font-size:15px;line-height:33px">'+sess+'</b></div></div>'+
   '<dl><dt>Protocol<dd class="mono">'+esc(d.n)+'<dt>Local AS<dd>'+esc(las||'-')+'<dt>Remote AS<dd>'+esc(d.as)+'<dt>Family<dd>'+(v6?'IPv6':'IPv4')+'</dl>'+
   '<div class="acts"><button type="button" class="btn" data-a="edit">Edit</button><button type="button" class="btn" data-a="tb-cm">Comment</button>'+
   (off?'<button type="button" class="btn" data-a="tb-en">Enable</button>':'<button type="button" class="btn" data-a="tb-dis">Disable</button>')+
   '<button type="button" class="btn dng" data-a="del">Delete</button></div>';
  var key=r.dataset.n+'|'+h.length+'|'+h;
  if(key!==last){last=key;pd.innerHTML=h}
}
pd.addEventListener('click',function(e){var b=e.target.closest('button[data-a]');if(!b)return;var r=cur();if(!r)return;var a=b.dataset.a;
  if(a==='edit'){if(window.bvEditPeer)window.bvEditPeer(r.dataset.nm)}
  else if(a==='del'){if(window.bvDeletePeer)window.bvDeletePeer(r.dataset.nm)}
  else{var t=document.getElementById(a);if(t&&!t.disabled)t.click()}});
tb.addEventListener('click',function(){setTimeout(render,0)});
document.addEventListener('bvrefilter',render);
setInterval(render,1000);setTimeout(render,50);
})();

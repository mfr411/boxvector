(function(){
var tb=document.querySelector('#peers tbody'),q=document.getElementById('filter'),f=document.getElementById('pst'),cn=document.getElementById('pcount'),ov=document.getElementById('ov'),dlg=document.getElementById('dlg'),win=document.getElementById('win');
if(!tb)return;
function esc(s){return String(s).replace(/[&<>"]/g,function(c){return{'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]})}
function num(v){return v==='-1'?'-':v}
function filt(){var k=q.value.toLowerCase(),m=f.value,n=0,t=0;
  [].forEach.call(tb.rows,function(r){if(!r.dataset.n)return;t++;
    var ok=(!m||(m==='up')===(r.dataset.up==='1'))&&(!k||r.textContent.toLowerCase().indexOf(k)>=0);
    r.style.display=ok?'':'none';if(ok)n++});
  cn.textContent=n+' of '+t+' items'}
q.addEventListener('input',filt);f.addEventListener('change',filt);filt();
[].forEach.call(tb.rows,function(r){if(!r.dataset.nb)return;var s=r.querySelector('.fam');if(s)s.textContent=r.dataset.nb.indexOf(':')>=0?'v6':'v4'});
tb.addEventListener('click',function(e){var tr=e.target.closest('tr');if(!tr||!tr.dataset.n)return;[].forEach.call(tb.rows,function(r){r.classList.toggle('sel',r===tr)})});
function close_(){ov.classList.remove('on');win.classList.remove('inactive')}
function show(r){var d=r.dataset,fam=d.nb.indexOf(':')>=0?'IPv6':'IPv4';
  var rows=[['Peer',d.nm],['Protocol',d.n],['Neighbor',d.nb],['Family',fam],['Remote AS','AS'+d.as],['State',d.st],['Imported routes',num(d.im)],['Exported routes',num(d.ex)],['Since',d.since||'-']];
  dlg.innerHTML='<div class="wt">Peer &lt;'+esc(d.nm)+' '+fam+'&gt;<b id="dx">✕</b></div><div class="bd"><div class="bl"><div class="dtabs"><span class="on">General</span></div><div class="pn" style="height:auto;min-height:260px"><div class="ifg">'+rows.map(function(a){return '<span class="k">'+a[0]+'</span><span class="v">'+esc(a[1])+'</span>'}).join('')+'</div></div></div><div class="br"><button id="dok">OK</button><button id="dde" class="dng">Delete</button><button id="ded" class="pri">Edit</button></div></div><div class="ds"><span>'+(d.up==='1'?'established':'not established')+'</span><span style="margin-left:auto">live from bird · read-only</span></div>';
  ov.classList.add('on');win.classList.add('inactive');
  document.getElementById('dx').onclick=document.getElementById('dok').onclick=close_;
  document.getElementById('dde').onclick=function(){close_();if(window.bvDeletePeer)window.bvDeletePeer(d.nm)};
  document.getElementById('ded').onclick=function(){close_();if(window.bvEditPeer)window.bvEditPeer(d.nm)}}
tb.addEventListener('dblclick',function(e){var tr=e.target.closest('tr');if(tr&&tr.dataset.n)show(tr)});
document.addEventListener('keydown',function(e){if(e.key==='Escape')close_()});
ov.addEventListener('mousedown',function(e){if(e.target===ov)close_()});
})();

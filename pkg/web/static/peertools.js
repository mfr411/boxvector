// Toolbar of the Peers page: Remove / Enable / Disable / Comment act on the selected row (all go through
// the review dialog, nothing is applied without Apply); Filter opens a WinBox-style filter bar.
(function(){
var tb=document.querySelector('#peers tbody');if(!tb)return;
var $=function(i){return document.getElementById(i)},rm=$('tb-rm'),en=$('tb-en'),di=$('tb-dis'),cm=$('tb-cm'),fl=$('tb-fl'),bar=$('fbar'),rows=$('frows');
function cur(){return tb.querySelector('tr.sel')}
function sync(){var r=cur(),on=r&&r.dataset.nm,off=r&&r.dataset.off==='1';
  rm.disabled=!on;cm.disabled=!on;en.disabled=!on||!off;di.disabled=!on||off}
tb.addEventListener('click',function(){setTimeout(sync,0)});sync();
function nm(){var r=cur();return r?r.dataset.nm:''}
rm.onclick=function(){if(nm()&&window.bvDeletePeer)window.bvDeletePeer(nm())};
en.onclick=function(){if(nm()&&window.bvTogglePeer)window.bvTogglePeer(nm(),'enable')};
di.onclick=function(){if(nm()&&window.bvTogglePeer)window.bvTogglePeer(nm(),'disable')};
cm.onclick=function(){if(nm()&&window.bvCommentPeer)window.bvCommentPeer(nm())};
// ---- filter bar ----
var F=[['Name','nm'],['Neighbor','nb'],['AS','as'],['State','st'],['Comment','desc']],O=[['contains','c'],['does not contain','n'],['is','i'],['is not','x']];
function opt(a){return a.map(function(o){return '<option value="'+o[1]+'">'+o[0]+'</option>'}).join('')}
function add(){var d=document.createElement('div');d.className='frow';
  d.innerHTML='<select class="ff">'+opt(F)+'</select><select class="fo">'+opt(O)+'</select><input class="fv" type="text" autocomplete="off" aria-label="Filter value"><button type="button" class="ib fp" title="Add condition">+</button><button type="button" class="ib fm" title="Remove condition">−</button>';
  d.querySelector('.fp').onclick=add;d.querySelector('.fm').onclick=function(){if(rows.children.length>1)d.remove();else d.querySelector('.fv').value='';run()};
  d.querySelector('.fv').addEventListener('keydown',function(e){if(e.key==='Enter'){e.preventDefault();run()}});
  rows.appendChild(d);d.querySelector('.fv').focus()}
function hit(r,f,o,v){var s=String(r.dataset[f]||'').toLowerCase();v=v.toLowerCase();
  return o==='c'?s.indexOf(v)>=0:o==='n'?s.indexOf(v)<0:o==='i'?s===v:s!==v}
function run(){var cs=[].map.call(rows.children,function(d){return[d.querySelector('.ff').value,d.querySelector('.fo').value,d.querySelector('.fv').value.trim()]}).filter(function(c){return c[2]!==''}),n=0,t=0;
  [].forEach.call(tb.rows,function(r){if(!r.dataset.n)return;t++;var ok=cs.every(function(c){return hit(r,c[0],c[1],c[2])});r.dataset.fx=ok?'':'1';n++});
  var ev=document.getElementById('filter');if(ev)ev.dispatchEvent(new Event('input'))}
// peers.js hides rows by its own Find box; combine both by re-hiding rows we filtered out
var q=$('filter');
if(q)q.addEventListener('input',function(){setTimeout(function(){var n=0,t=0;[].forEach.call(tb.rows,function(r){if(!r.dataset.n)return;t++;if(r.dataset.fx==='1')r.style.display='none';if(r.style.display!=='none')n++});var c=$('pcount');if(c)c.textContent=n+' of '+t+' items'},0)});
function show(on){bar.hidden=!on;fl.setAttribute('aria-pressed',on?'true':'false');fl.classList.toggle('on',on);if(on&&!rows.children.length)add()}
fl.onclick=function(){show(bar.hidden)};
$('fgo').onclick=run;
$('fclr').onclick=function(){rows.innerHTML='';add();[].forEach.call(tb.rows,function(r){delete r.dataset.fx});if(q)q.dispatchEvent(new Event('input'))};
})();

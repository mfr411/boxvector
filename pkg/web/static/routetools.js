// Routes toolbar: select a static route (defined in the config) and remove / enable / disable / comment it.
(function(){
var tb=document.querySelector('table.rt tbody'),KV=!!document.querySelector('table.rt.rk');if(!tb)return;
var rm=document.getElementById('rt-rm'),en=document.getElementById('rt-en'),di=document.getElementById('rt-dis'),cm=document.getElementById('rt-cm');if(!rm)return;
var cur=null;
function sync(){var on=!!cur,off=on&&cur.dataset.off==='1';rm.disabled=!on;cm.disabled=!on;en.disabled=!on||!off;di.disabled=!on||off}
tb.addEventListener('click',function(e){var tr=e.target.closest('tr');if(!tr)return;
  cur=(tr.dataset.p||tr.dataset.k)?tr:null;[].forEach.call(tb.rows,function(r){r.classList.toggle('sel',r===tr)});sync()});
function kq(d){var c=cur.dataset;return 'dst='+encodeURIComponent(c.dst)+'&via='+encodeURIComponent(c.via)+'&dev='+encodeURIComponent(c.dev)+'&metric='+encodeURIComponent(c.m)+'&do='+d}
function act(d,t){return function(){if(cur&&cur.dataset.k&&window.bvOpen){var kd=d==='remove'?'del':d;window.bvOpen('/kroutes/act?'+kq(kd),t+' route · '+cur.dataset.dst,'/kroutes/act');return}if(cur&&window.bvOpen)window.bvOpen('/routes/act?do='+d+'&prefix='+encodeURIComponent(cur.dataset.p),t+' route · '+cur.dataset.p,'/routes/act')}}
rm.onclick=act('remove','remove');en.onclick=act('enable','enable');di.onclick=act('disable','disable');
cm.onclick=function(){if(cur&&cur.dataset.k&&window.bvOpen){window.bvOpen('/kroutes/comment?'+kq('')+'&comment='+encodeURIComponent(cur.dataset.cm||''),'comment route · '+cur.dataset.dst,'/kroutes/comment');return}if(cur&&window.bvOpen)window.bvOpen('/routes/comment?prefix='+encodeURIComponent(cur.dataset.p),'comment route · '+cur.dataset.p,'/routes/comment')};
sync();
})();

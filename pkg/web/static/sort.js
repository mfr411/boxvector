// Click a sortable header (Name / Peer / Dst. address) to sort A-Z, natural order (2 before 10,
// IPv4 before IPv6). Click again = Z-A, third click = back to the original order.
// Choice is kept per table in localStorage. Interfaces redraws every 2 s and calls bvSortRows.
(function(){
var K='bvsort';
function ld(){try{return JSON.parse(localStorage.getItem(K))||{}}catch(e){return{}}}
function sv(o){try{localStorage.setItem(K,JSON.stringify(o))}catch(e){}}
function key(t){return t.id||(t.className.indexOf('rk')>=0?'routes-kernel':t.className.indexOf('rt')>=0?'routes':location.pathname)}
function tok(s){s=String(s).trim().toLowerCase();var v6=s.indexOf(':')>=0;return {v6:v6,p:s.split(/(\d+)/).filter(function(x){return x!==''}).map(function(x){return /^\d+$/.test(x)?parseInt(x,10):x})}}
function cmp(a,b){var x=tok(a),y=tok(b);if(x.v6!==y.v6)return x.v6?1:-1;
  for(var i=0;i<Math.max(x.p.length,y.p.length);i++){var p=x.p[i],q=y.p[i];if(p===undefined)return -1;if(q===undefined)return 1;
    if(p===q)continue;if(typeof p==='number'&&typeof q==='number')return p-q;return String(p)<String(q)?-1:1}return 0}
window.bvSortCmp=cmp;
window.bvSortState=function(id){return ld()[id]||null};
function txt(td){return (td.getAttribute('data-sv')||td.textContent).replace(/^\s+|\s+$/g,'').replace(/\s+(v4|v6)$/,'')}
function apply(t,col,dir){
  var tb=t.tBodies[0],rows=[].slice.call(tb.rows).filter(function(r){return r.cells.length>col&&!r.querySelector('td[colspan]')});
  if(!t._orig)t._orig=rows.slice();
  var out=dir?rows.slice().sort(function(a,b){var r=cmp(txt(a.cells[col]),txt(b.cells[col]));return dir==='d'?-r:r}):t._orig.filter(function(r){return rows.indexOf(r)>=0});
  out.forEach(function(r){tb.appendChild(r)});
}
function mark(t,col,dir){[].forEach.call(t.tHead.rows[0].cells,function(th,i){th.classList.remove('sa','sd');if(i===col&&dir)th.classList.add(dir==='a'?'sa':'sd')})}
function colOf(t){var th=[].slice.call(t.tHead.rows[0].cells).filter(function(h){return h.hasAttribute('data-sort')})[0];return th?th.cellIndex:-1}
document.querySelectorAll('table.ift').forEach(function(t){
  var col=colOf(t);if(col<0||!t.tHead)return;var id=key(t),th=t.tHead.rows[0].cells[col];th.classList.add('srt');
  var s=ld()[id]||null;if(s&&t.tBodies[0].rows.length&&id!=='ift'){apply(t,col,s)}mark(t,col,s);
  th.addEventListener('click',function(e){if(e.target.classList.contains('rz'))return;
    var o=ld(),cur=o[id]||null,nx=cur===null?'a':cur==='a'?'d':null;if(nx)o[id]=nx;else delete o[id];sv(o);
    if(id==='ift'){mark(t,col,nx);if(window.bvRedraw)window.bvRedraw()}else{apply(t,col,nx);mark(t,col,nx)}});
});
})();

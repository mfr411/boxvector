// Resizable table columns.
// Default: the table fills its window and columns share the width proportionally (CSS).
// After the user drags a column, widths are frozen in pixels and saved in a cookie (per table);
// double-click a header edge to go back to the default layout.
(function(){
var CK='bvcols',MIN=24;
function load(){var m=document.cookie.match(/(?:^|; )bvcols=([^;]*)/);if(!m)return{};try{return JSON.parse(decodeURIComponent(m[1]))||{}}catch(e){return{}}}
function save(o){document.cookie=CK+'='+encodeURIComponent(JSON.stringify(o))+'; path=/; max-age=31536000; SameSite=Lax'}
function key(t){return t.id||(location.pathname+(location.search.indexOf('v=kernel')>=0?'#k':''))}
document.querySelectorAll('table.ift').forEach(function(t){
  var cols=[].slice.call(t.querySelectorAll('colgroup col')),ths=[].slice.call(t.querySelectorAll('thead th'));
  if(!cols.length||cols.length!==ths.length)return;
  var k=key(t),frozen=false;
  function px(i){return parseFloat(cols[i].style.width)||ths[i].getBoundingClientRect().width}
  function sum(){var s=0;cols.forEach(function(c,i){s+=px(i)});return Math.round(s)}
  function freeze(w){cols.forEach(function(c,i){c.style.width=Math.max(MIN,Math.round(w[i]))+'px'});t.style.minWidth='0';t.style.width=sum()+'px';frozen=true}
  function thaw(){cols.forEach(function(c){c.style.width=''});t.style.width='';t.style.minWidth='';frozen=false}
  function persist(){var s=load();if(frozen)s[k]=cols.map(function(c){return Math.round(parseFloat(c.style.width))});else delete s[k];save(s)}
  var w=load()[k];if(w&&w.length===cols.length)freeze(w);
  ths.forEach(function(th,i){
    var h=document.createElement('span');h.className='rz';h.title='Drag to resize · double-click to reset all columns';th.appendChild(h);
    h.addEventListener('mousedown',function(e){e.preventDefault();e.stopPropagation();
      if(!frozen)freeze(ths.map(function(x){return x.getBoundingClientRect().width}));
      var x0=e.clientX,w0=px(i);document.body.classList.add('rzing');
      function mv(ev){cols[i].style.width=Math.max(MIN,w0+ev.clientX-x0)+'px';t.style.width=sum()+'px'}
      function up(){document.removeEventListener('mousemove',mv);document.removeEventListener('mouseup',up);document.body.classList.remove('rzing');persist()}
      document.addEventListener('mousemove',mv);document.addEventListener('mouseup',up)});
    h.addEventListener('dblclick',function(e){e.stopPropagation();thaw();persist()});
    h.addEventListener('click',function(e){e.stopPropagation()});
  });
});
})();

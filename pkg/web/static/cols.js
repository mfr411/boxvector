// resizable table columns; widths persist in a cookie (per table), applied before first paint of the table
(function(){
var CK='bvcols',MIN=24;
function load(){var m=document.cookie.match(/(?:^|; )bvcols=([^;]*)/);if(!m)return{};try{return JSON.parse(decodeURIComponent(m[1]))||{}}catch(e){return{}}}
function save(o){document.cookie=CK+'='+encodeURIComponent(JSON.stringify(o))+'; path=/; max-age=31536000; SameSite=Lax'}
function key(t){return t.id||(location.pathname+(location.search.indexOf('v=kernel')>=0?'#k':''))}
function total(cols){var s=0;cols.forEach(function(c){s+=parseFloat(c.style.width)||c.getBoundingClientRect().width});return s}
document.querySelectorAll('table.ift').forEach(function(t){
  var cols=[].slice.call(t.querySelectorAll('colgroup col')),ths=[].slice.call(t.querySelectorAll('thead th'));
  if(!cols.length||cols.length!==ths.length)return;
  var k=key(t),def=cols.map(function(c){return Math.round(c.getBoundingClientRect().width)});
  function fit(){t.style.minWidth='0';t.style.width=Math.round(total(cols))+'px'}
  function put(w){cols.forEach(function(c,i){if(w[i])c.style.width=Math.max(MIN,w[i])+'px'});fit()}
  var st=load(),w=st[k];if(w&&w.length===cols.length)put(w);else{put(def)}
  function persist(){var s=load();s[k]=cols.map(function(c){return Math.round(parseFloat(c.style.width))});save(s)}
  ths.forEach(function(th,i){
    if(i===ths.length-1&&false)return;
    var h=document.createElement('span');h.className='rz';h.title='Drag to resize · double-click to reset';th.appendChild(h);
    h.addEventListener('mousedown',function(e){e.preventDefault();e.stopPropagation();
      var x0=e.clientX,w0=parseFloat(cols[i].style.width)||def[i];document.body.classList.add('rzing');
      function mv(ev){cols[i].style.width=Math.max(MIN,w0+ev.clientX-x0)+'px';fit()}
      function up(){document.removeEventListener('mousemove',mv);document.removeEventListener('mouseup',up);document.body.classList.remove('rzing');persist()}
      document.addEventListener('mousemove',mv);document.addEventListener('mouseup',up)});
    h.addEventListener('dblclick',function(e){e.stopPropagation();cols[i].style.width=def[i]+'px';fit();persist()});
    h.addEventListener('click',function(e){e.stopPropagation()});
  });
});
})();

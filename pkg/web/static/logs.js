// Logs: filter, find, expandable output.
(function(){
var t=document.getElementById('logs');if(!t)return;
var rows=[].slice.call(t.querySelectorAll('tbody tr:not(.lov)')).filter(function(r){return r.dataset.a}),
fs=document.getElementById('lf'),q=document.getElementById('lq'),cnt=document.getElementById('lcnt');
function ov(r){var n=r.nextElementSibling;return n&&n.classList.contains('lov')?n:null}
function run(){var f=fs.value,s=q.value.toLowerCase(),n=0;
  rows.forEach(function(r){var a=r.dataset.a,ok=r.dataset.ok==='1',v=true;
    if(f==='ok')v=ok;else if(f==='bad')v=!ok;else if(f)v=a.indexOf(f)>=0;
    if(v&&s)v=r.textContent.toLowerCase().indexOf(s)>=0;
    r.hidden=!v;var o=ov(r);if(o&&!v)o.hidden=true;if(v)n++});
  cnt.textContent=n+' of '+rows.length+' items'}
fs.addEventListener('change',run);q.addEventListener('input',run);
t.addEventListener('click',function(e){var b=e.target.closest('.lo');if(!b)return;var o=ov(b.closest('tr'));if(o){o.hidden=!o.hidden;b.textContent=o.hidden?'output':'hide'}});
})();

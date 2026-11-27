// Config editor: line numbers, dirty state, cursor position, Ctrl+S = review, leave warning.
(function(){
var ta=document.getElementById('ctext');if(!ta)return;
var g=document.getElementById('cgut'),d=document.getElementById('cdirty'),sv=document.getElementById('csave'),rv=document.getElementById('crev'),
pos=document.getElementById('cpos'),ln=document.getElementById('clines'),f=document.getElementById('cfg'),orig=ta.value,go=false;
function gut(){var n=ta.value.split('\n').length;if(g.dataset.n!=n){var s='';for(var i=1;i<=n;i++)s+=i+'\n';g.textContent=s;g.dataset.n=n}ln.textContent=n+' lines';g.scrollTop=ta.scrollTop}
function cur(){var v=ta.value.slice(0,ta.selectionStart).split('\n');pos.textContent='Ln '+v.length+', Col '+(v[v.length-1].length+1)}
function chg(){var m=ta.value!==orig;d.hidden=!m;sv.disabled=!m;rv.disabled=!m;gut();cur()}
ta.addEventListener('input',chg);ta.addEventListener('scroll',function(){g.scrollTop=ta.scrollTop});
['keyup','click','focus'].forEach(function(e){ta.addEventListener(e,cur)});
ta.addEventListener('keydown',function(e){
  if((e.ctrlKey||e.metaKey)&&e.key==='s'){e.preventDefault();if(!sv.disabled)f.requestSubmit(sv);return}
  if(e.key==='Tab'){e.preventDefault();var s=ta.selectionStart;ta.setRangeText('  ',s,ta.selectionEnd,'end');chg()}});
rv.addEventListener('click',function(){if(confirm('Discard your edits?')){ta.value=orig;chg()}});
f.addEventListener('submit',function(){go=true});
addEventListener('beforeunload',function(e){if(!go&&ta.value!==orig){e.preventDefault();e.returnValue=''}});
chg();
})();

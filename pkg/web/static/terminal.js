// Root terminal: xterm.js in the page, long-poll output / POST input through the web service.
(function(){
var el=document.getElementById('term');if(!el)return;
var csrf=el.dataset.csrf,id=null,off=0,run=false,term,fit,stat=document.getElementById('tstat'),
 bn=document.getElementById('tnew'),bc=document.getElementById('tclose'),sz=document.getElementById('tsz');
function post(p,o){var b=new URLSearchParams(o||{});b.set('csrf',csrf);return fetch(p,{method:'POST',body:b,credentials:'same-origin'})}
function say(t){stat.hidden=!t;stat.textContent=t||''}
function mk(){
  if(term)term.dispose();
  term=new Terminal({fontFamily:'Consolas,"DejaVu Sans Mono",monospace',fontSize:13,cursorBlink:true,scrollback:5000,theme:{background:'#10131a',foreground:'#d7dae0',cursor:'#7ee2a0',selectionBackground:'#3a4468'}});
  fit=new FitAddon.FitAddon();term.loadAddon(fit);term.open(el);fit.fit();sz.textContent=term.cols+'x'+term.rows;
  term.onData(function(d){if(id)post('/term/in',{id:id,d:d})});
  term.onResize(function(s){sz.textContent=s.cols+'x'+s.rows;if(id)post('/term/resize',{id:id,cols:s.cols,rows:s.rows})});
  term.focus();
}
async function poll(my){
  var fails=0;
  while(run&&id===my){
    try{
      var r=await fetch('/term/out?id='+my+'&off='+off,{credentials:'same-origin'});
      if(r.status!==200){say(r.status===404?'session ended':'error '+r.status);break}
      var b=new Uint8Array(await r.arrayBuffer());if(b.length)term.write(b);
      off=parseInt(r.headers.get('X-Next')||off,10);fails=0;
      if(r.headers.get('X-Done')){say('session ended');break}
    }catch(e){if(++fails>5){say('connection lost');break}await new Promise(function(z){setTimeout(z,1000)})}
  }
  if(id===my){run=false;id=null;bc.disabled=true;bn.textContent='New session'}
}
async function open(){
  mk();say('connecting');
  var r=await post('/term/new',{cols:term.cols,rows:term.rows});
  if(!r.ok){say((await r.text()).trim()||'failed');return}
  var j=await r.json();id=j.id;off=0;run=true;say('');bc.disabled=false;bn.textContent='Restart';poll(id);term.focus();
}
bn.addEventListener('click',async function(){if(id){await post('/term/close',{id:id});run=false;id=null}open()});
bc.addEventListener('click',async function(){if(!id)return;var o=id;run=false;id=null;await post('/term/close',{id:o});bc.disabled=true;bn.textContent='New session';say('closed')});
addEventListener('resize',function(){if(fit)fit.fit()});
addEventListener('pagehide',function(){if(id&&navigator.sendBeacon){var b=new URLSearchParams({id:id,csrf:csrf});navigator.sendBeacon('/term/close',b)}});
open();
})();

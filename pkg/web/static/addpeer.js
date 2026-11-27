// "Add peer" as a floating, draggable window on the Peers page (no page change).
// Reuses the server form (GET /peers/new) and review (POST /peers/new); nothing is applied until Apply.
(function(){
var btn=document.querySelector('a.add');if(!btn)return;
var act='/peers/new';
var fw=null;
function esc(s){return String(s).replace(/[&<>"]/g,function(c){return{'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]})}
var ttl='add peer';
function build(){
  fw=document.createElement('div');fw.className='fw';fw.setAttribute('role','dialog');
  fw.innerHTML='<div class="wt"><span>'+ttl+'</span><b title="Close (Esc)">✕</b></div><div class="fwb"></div>';
  document.body.appendChild(fw);
  var wt=fw.querySelector('.wt'),x0,y0,l0,t0;
  fw.style.left=Math.max(20,(innerWidth-fw.offsetWidth)/2+60)+'px';fw.style.top='70px';fit();addEventListener('resize',fit);
  wt.addEventListener('mousedown',function(e){if(e.target.tagName==='B')return;e.preventDefault();
    x0=e.clientX;y0=e.clientY;l0=fw.offsetLeft;t0=fw.offsetTop;fw.classList.add('drag');
    function mv(ev){fw.style.left=Math.min(innerWidth-80,Math.max(-fw.offsetWidth+120,l0+ev.clientX-x0))+'px';fw.style.top=Math.min(innerHeight-40,Math.max(0,t0+ev.clientY-y0))+'px';fit()}
    function up(){document.removeEventListener('mousemove',mv);document.removeEventListener('mouseup',up);fw.classList.remove('drag')}
    document.addEventListener('mousemove',mv);document.addEventListener('mouseup',up)});
  fw.querySelector('b').onclick=close_;
  fw.addEventListener('mousedown',function(){fw.classList.add('top')});
}
function fit(){if(!fw)return;fw.style.maxHeight=Math.max(220,innerHeight-fw.offsetTop-12)+'px'}
function close_(){if(fw){fw.remove();fw=null}}
function body(){return fw.querySelector('.fwb')}
function grab(html){return new DOMParser().parseFromString(html,'text/html')}
function setForm(doc,keep){
  var f=doc.querySelector('form.frm');if(!f){body().innerHTML='<p class="err">Unexpected response.</p>';return}
  var err=doc.querySelector('main .err');
  body().innerHTML='';
  if(err){var p=document.createElement('p');p.className='err';p.textContent=err.textContent;body().appendChild(p)}
  var n=document.importNode(f,true);body().appendChild(n);
  var c=n.querySelector('a.btn');if(c)c.addEventListener('click',function(e){e.preventDefault();close_()});
  n.addEventListener('submit',function(e){e.preventDefault();send(n)});
  var first=n.querySelector('input[name=name]');if(first)first.focus();
}
function send(form){
  var b=form.querySelector('button.pri');b.disabled=true;b.textContent='Checking…';
  fetch(act,{method:'POST',credentials:'same-origin',body:new URLSearchParams(new FormData(form))})
   .then(function(r){return r.text()}).then(function(h){var doc=grab(h);
     var ap=doc.querySelector('form[action="/config/apply"]'),m=doc.querySelector('main');
     if(!ap&&!m.querySelector('pre.diff')){ // validation error or no change: show the form again with the message
       if(doc.querySelector('form.frm'))return setForm(doc);
       body().innerHTML=esc('')+m.innerHTML;return}
     review(doc,form)})
   .catch(function(e){b.disabled=false;b.textContent='Review';var p=document.createElement('p');p.className='err';p.textContent='Request failed: '+e;body().prepend(p)});
}
function review(doc,form){
  var m=doc.querySelector('main'),bar=m.querySelector('.bar');if(bar)bar.remove();
  body().innerHTML=m.innerHTML;fw.querySelector('.wt span').textContent=ttl+' · review';
  var cancel=body().querySelector('a.btn');
  if(cancel){var bk=document.createElement('button');bk.type='button';bk.className='btn';bk.textContent='Back';bk.onclick=function(){fw.querySelector('.wt span').textContent=ttl;body().innerHTML='';body().appendChild(form);form.querySelector('button.pri').disabled=false;form.querySelector('button.pri').textContent='Review'};
    var c2=document.createElement('button');c2.type='button';c2.className='btn';c2.textContent='Cancel';c2.onclick=close_;cancel.replaceWith(bk,c2)}
  var ap=body().querySelector('button[data-confirm]');
  if(ap)ap.addEventListener('click',function(e){if(!confirm(ap.getAttribute('data-confirm')))e.preventDefault()});
}
function open_(url,title,post){
  if(fw)close_();
  ttl=title;act=post;build();body().innerHTML='<p class="s">Loading…</p>';
  fetch(url,{credentials:'same-origin'}).then(function(r){return r.text()}).then(function(h){if(fw)setForm(grab(h))})
   .catch(function(){if(fw)body().innerHTML='<p class="err">Could not load the form.</p>'})}
btn.addEventListener('click',function(e){if(e.ctrlKey||e.metaKey||e.shiftKey)return;e.preventDefault();
  if(fw&&act==='/peers/new'){fw.classList.add('top');return}
  open_('/peers/new','add peer','/peers/new')});
window.bvEditPeer=function(name){open_('/peers/edit?name='+encodeURIComponent(name),'edit peer · '+name,'/peers/edit')};
document.addEventListener('keydown',function(e){if(e.key==='Escape'&&fw&&document.activeElement&&fw.contains(document.activeElement))close_()});
})();

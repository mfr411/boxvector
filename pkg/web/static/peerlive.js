(function(){
var tb=document.querySelector('#peers tbody');if(!tb)return;
var psum=document.getElementById('psum'),busy=false,fails=0;
function cells(r){return r.cells}
function apply(d){
  var rows={},known=0;
  [].forEach.call(tb.rows,function(r){if(r.dataset.n){rows[r.dataset.n]=r;known++}});
  if(d.peers.length!==known||d.peers.some(function(p){return !rows[p.n]})){location.reload();return}
  d.peers.forEach(function(p){
    var r=rows[p.n],c=r.cells;
    r.dataset.st=p.st;r.dataset.up=p.up?'1':'0';r.dataset.im=p.im;r.dataset.ex=p.ex;r.dataset.since=p.since;
    c[0].textContent=p.off?'X':(p.up?'R':'');
    var pill=c[4].querySelector('.st');if(pill){pill.textContent=p.st;pill.className='st'+(p.cl?' '+p.cl:'')}
    c[5].textContent=p.im<0?'-':p.im;c[6].textContent=p.ex<0?'-':p.ex;c[7].textContent=p.since;
  });
  var s=d.sum,n=function(v){return v<0?'-':String(v)};
  if(psum)psum.innerHTML='<b>'+s.Up+'</b> up<i></i><b>'+s.Down+'</b> down<i></i><b>'+s.R4+'</b> v4 routes<i></i><b>'+s.R6+'</b> v6 routes';
  document.dispatchEvent(new Event('bvrefilter'));
}
function poll(){
  if(busy||document.hidden)return;busy=true;
  fetch('/api/peers',{cache:'no-store',credentials:'same-origin'}).then(function(r){if(r.status===401||r.redirected){location='/login';throw 0}return r.json()}).then(function(d){fails=0;if(!d.err)apply(d)}).catch(function(){fails++}).then(function(){busy=false})
}
setInterval(poll,1000);
document.addEventListener('visibilitychange',poll);
})();

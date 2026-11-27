(function(){
var prev={},data=[],tb=document.querySelector('#ift tbody'),q=document.getElementById('ifq'),f=document.getElementById('iff'),cn=document.getElementById('ifn');
function bps(n){return n>=1e9?(n/1e9).toFixed(1)+' Gbps':n>=1e6?(n/1e6).toFixed(1)+' Mbps':n>=1e3?(n/1e3).toFixed(1)+' kbps':Math.round(n)+' bps'}
function spd(m){return !m?'-':m>=1000?(m/1000)+' Gbps':m+' Mbps'}
function esc(s){return String(s).replace(/[&<>"]/g,function(c){return{'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]})}
function draw(){
  var k=q.value.toLowerCase(),m=f.value,n=0,h='';
  data.forEach(function(x){
    if(m==='up'&&!x.up||m==='down'&&x.up)return;
    if(k&&(x.n+' '+x.type+' '+x.mac+' '+x.drv+' '+x.v4.join(' ')+' '+x.v6.join(' ')).toLowerCase().indexOf(k)<0)return;
    n++;var r=x.r||{tx:0,rx:0,txp:0,rxp:0};
    h+='<tr class="'+(x.dis?'dis':'')+'" title="'+esc(x.v4.concat(x.v6).join('\n'))+'"><td class="fl">'+(x.up?'R':x.dis?'X':'')+'</td><td class="nm'+(x.master?' sub':'')+'">'+esc(x.n)+'</td><td>'+esc(x.type)+'</td><td class="n">'+x.mtu+'</td><td class="n">'+bps(r.tx)+'</td><td class="n">'+bps(r.rx)+'</td><td class="n">'+r.txp+'</td><td class="n">'+r.rxp+'</td><td>'+esc(x.drv)+'</td><td class="n">'+spd(x.spd)+'</td><td class="mac">'+esc(x.mac)+'</td></tr>';
  });
  tb.innerHTML=h;cn.textContent=n+' items';
}
function poll(){
  fetch('/api/ifaces',{cache:'no-store',credentials:'same-origin'}).then(function(r){if(r.status===401||r.redirected){location='/login';throw 0}return r.json()}).then(function(a){
    var now=Date.now();
    a.forEach(function(x){var p=prev[x.n],dt=p?(now-p.t)/1e3:0,d=function(a,b){return Math.max(0,a-b)};
      x.r=p&&dt>.2?{tx:d(x.tx,p.tx)*8/dt,rx:d(x.rx,p.rx)*8/dt,txp:Math.round(d(x.txk,p.txk)/dt),rxp:Math.round(d(x.rxk,p.rxk)/dt)}:{tx:0,rx:0,txp:0,rxp:0};
      prev[x.n]={t:now,tx:x.tx,rx:x.rx,txk:x.txk,rxk:x.rxk}});
    data=a;draw();
  }).catch(function(){});
}
q.addEventListener('input',draw);f.addEventListener('change',draw);
poll();setInterval(poll,2000);
})();

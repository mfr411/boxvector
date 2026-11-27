(function(){
var prev={},data=[],sel='',tb=document.querySelector('#ift tbody'),q=document.getElementById('ifq'),f=document.getElementById('iff'),cn=document.getElementById('ifn'),ov=document.getElementById('ov'),dlg=document.getElementById('dlg'),win=document.getElementById('win');
function esc(s){return String(s).replace(/[&<>"]/g,function(c){return{'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]})}
function bps(n){var u=['bps','kbps','Mbps','Gbps','Tbps'],i=0;while(n>=1000&&i<4){n/=1000;i++}
  var v=i?(n>=100?n.toFixed(0):n>=10?n.toFixed(1):n.toFixed(2)):String(Math.round(n));
  return '<span class="bv">'+v+'</span><span class="bu">'+u[i]+'</span>'}
function pps(n){return n>=1e6?(n/1e6).toFixed(1)+' M':n>=1e3?(n/1e3).toFixed(1)+' k':String(n)}
function spd(m){return !m?'-':m>=1000?(m/1000)+' Gbps':m+' Mbps'}
var ICB='<svg viewBox="0 0 16 16"><path d="M8 4v4M3.5 12V8h9v4" fill="none" stroke="#6b7280" stroke-width="1.3"/><rect x="5.5" y="1.5" width="5" height="3" fill="#6b7280"/><rect x="1.5" y="11" width="4" height="3" fill="#6b7280"/><rect x="10.5" y="11" width="4" height="3" fill="#6b7280"/></svg>',
ICX='<svg viewBox="0 0 16 16"><circle cx="8" cy="8" r="5.5" fill="none" stroke="#6b7280" stroke-width="1.4"/><circle cx="8" cy="8" r="1.8" fill="#6b7280"/></svg>',
ICV='<svg viewBox="0 0 16 16"><rect x="1.5" y="2.5" width="13" height="11" rx="1.6" fill="#e8eefc" stroke="#2a5fd6" stroke-width="1.2"/><path d="M4.2 5.5 6 10.5 7.8 5.5M9.4 10.5V5.5h2.1" fill="none" stroke="#2a5fd6" stroke-width="1.3" stroke-linejoin="round"/></svg>';
function ice(up){return '<svg viewBox="0 0 16 16"><rect x="2" y="3.5" width="12" height="8" rx="1.2" fill="'+(up?'#2f9e44':'#9aa0ab')+'"/><rect x="4" y="5.3" width="8" height="3.2" fill="#1f2a22"/><path d="M5 11.5v2.5M8 11.5v2.5M11 11.5v2.5" stroke="'+(up?'#d9a400':'#b7bcc6')+'" stroke-width="1.4"/></svg>'}
function ico(x){return x.type==='VLAN'?ICV:x.type==='Bridge'?ICB:x.type==='Ethernet'?ice(x.up):ICX}
// bridge members sit under their master, like WinBox
function group(L){var names={};L.forEach(function(x){names[x.n]=1});var o=[];
  L.filter(function(x){return !x.master||!names[x.master]}).forEach(function(x){o.push(x);L.filter(function(c){return c.master===x.n}).forEach(function(c){o.push(c)})});return o}
function state(x){return x.up?'running':x.dis?'disabled':'no carrier'}
function draw(){
  var k=q.value.toLowerCase(),m=f.value,n=0,h='';
  group(data).forEach(function(x){
    if(m==='up'&&!x.up||m==='down'&&x.up)return;
    if(k&&(x.n+' '+x.type+' '+x.mac+' '+x.drv+' '+x.v4.join(' ')+' '+x.v6.join(' ')).toLowerCase().indexOf(k)<0)return;
    n++;var r=x.r||{tx:0,rx:0,txp:0,rxp:0};
    h+='<tr data-n="'+esc(x.n)+'" class="'+(x.up?'':'dis')+(x.n===sel?' sel':'')+'"><td class="fl">'+(x.dis?'X':x.up?'R':'')+'</td><td class="nm'+(x.master?' sub':'')+'"><span class="ei">'+ico(x)+'</span>'+esc(x.n)+'</td><td>'+esc(x.type)+'</td><td class="n">'+x.mtu+'</td><td class="n r">'+bps(r.tx)+'</td><td class="n r">'+bps(r.rx)+'</td><td class="n">'+pps(r.txp)+'</td><td class="n">'+pps(r.rxp)+'</td><td>'+esc(x.drv)+'</td><td class="n">'+spd(x.spd)+'</td><td class="mac">'+esc(x.mac)+'</td></tr>';
  });
  tb.innerHTML=h;cn.textContent=n+' items';
}
function rows(x){return [['Name',x.n],['Type',x.type],['Driver',x.drv],['Speed',spd(x.spd)],['MAC address',x.mac],['MTU',x.mtu],['Master',x.master||'-'],['Link',state(x)],['IPv4',x.v4.join(', ')||'-'],['IPv6',x.v6.join(', ')||'-'],['Rx / Tx bytes',x.rx+' / '+x.tx],['Rx / Tx packets',x.rxk+' / '+x.txk],['Rx / Tx errors',x.rxe+' / '+x.txe],['Rx / Tx drops',x.rxd+' / '+x.txd]]}
function gen(r){return '<div class="ifg">'+r.map(function(a){return '<span class="k">'+a[0]+'</span><span class="v">'+esc(a[1])+'</span>'}).join('')+'</div>'}
function ipl(x){return x.n+': <'+(x.dis?'':'UP,')+(x.up?'LOWER_UP':'NO-CARRIER')+'> mtu '+x.mtu+'\n    link/'+(x.type==='Loopback'?'loopback':'ether')+' '+x.mac+'\n'+x.v4.map(function(a){return '    inet '+a+'\n'}).join('')+x.v6.map(function(a){return '    inet6 '+a+'\n'}).join('')}
function open_(n){var x=data.filter(function(d){return d.n===n})[0];if(!x)return;
  dlg.innerHTML='<div class="wt">Interface &lt;'+esc(x.n)+'&gt;<b id="dx">✕</b></div><div class="bd"><div class="bl"><div class="dtabs"><span class="on" id="t0">General</span><span id="t1">ip link</span></div><div class="pn" id="pn">'+gen(rows(x))+'</div></div><div class="br"><button class="pb" id="dok">OK</button></div></div><div class="ds"><span>'+state(x)+'</span><span style="margin-left:auto">live from kernel · read-only</span></div>';
  ov.classList.add('on');win.classList.add('inactive');
  function tab(i){document.getElementById('t0').classList.toggle('on',i==0);document.getElementById('t1').classList.toggle('on',i==1);document.getElementById('pn').innerHTML=i==0?gen(rows(x)):'<pre class="mono">'+esc(ipl(x))+'</pre>'}
  document.getElementById('t0').onclick=function(){tab(0)};document.getElementById('t1').onclick=function(){tab(1)};
  document.getElementById('dx').onclick=document.getElementById('dok').onclick=close_}
function close_(){ov.classList.remove('on');win.classList.remove('inactive')}
document.addEventListener('keydown',function(e){if(e.key==='Escape')close_()});
ov.addEventListener('mousedown',function(e){if(e.target===ov)close_()});
tb.addEventListener('click',function(e){var tr=e.target.closest('tr');if(!tr)return;sel=tr.dataset.n;[].forEach.call(tb.children,function(r){r.classList.toggle('sel',r===tr)})});
tb.addEventListener('dblclick',function(e){var tr=e.target.closest('tr');if(tr)open_(tr.dataset.n)});
function poll(){
  fetch('/api/ifaces',{cache:'no-store',credentials:'same-origin'}).then(function(r){if(r.status===401||r.redirected){location='/login';throw 0}return r.json()}).then(function(a){
    var now=Date.now();
    a.forEach(function(x){var p=prev[x.n],dt=p?(now-p.t)/1e3:0,d=function(a,b){return Math.max(0,a-b)};
      x.r=p&&dt>.2?{tx:d(x.tx,p.tx)*8/dt,rx:d(x.rx,p.rx)*8/dt,txp:Math.round(d(x.txk,p.txk)/dt),rxp:Math.round(d(x.rxk,p.rxk)/dt)}:{tx:0,rx:0,txp:0,rxp:0};
      prev[x.n]={t:now,tx:x.tx,rx:x.rx,txk:x.txk,rxk:x.rxk}});
    data=a;if(!ov.classList.contains('on'))draw();
  }).catch(function(){});
}
q.addEventListener('input',draw);f.addEventListener('change',draw);
poll();setInterval(poll,2000);
})();

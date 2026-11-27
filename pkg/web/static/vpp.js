(function(){
var prev={};
function esc(s){return String(s).replace(/[&<>"]/g,function(c){return{'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]})}
function bps(n){var u=['bps','kbps','Mbps','Gbps','Tbps'],i=0;while(n>=1000&&i<4){n/=1000;i++}
  var v=i?(n>=100?n.toFixed(0):n>=10?n.toFixed(1):n.toFixed(2)):String(Math.round(n));
  return '<span class="bv">'+v+'</span><span class="bu">'+u[i]+'</span>'}
function pps(n){return n>=1e6?(n/1e6).toFixed(1)+' M':n>=1e3?(n/1e3).toFixed(1)+' k':String(n)}
function fill(id,h,empty,cols){document.querySelector('#'+id+' tbody').innerHTML=h||'<tr><td colspan="'+cols+'" style="color:#888">'+empty+'</td></tr>'}
function poll(){
  fetch('/api/vpp',{cache:'no-store',credentials:'same-origin'}).then(function(r){if(r.status===401||r.redirected){location='/login';throw 0}return r.json()}).then(function(d){
    var m=document.getElementById('vpm');
    if(!d.available){m.textContent='VPP is not installed on this host. Install it with: install.sh --mode all';return}
    if(!d.running){m.textContent=d.error||'VPP is not running';return}
    m.textContent=d.version+' · '+d.threads.length+' thread(s)';
    fill('vth',d.threads.map(function(t){return '<tr><td>'+t.id+'</td><td>'+esc(t.name)+'</td><td class="n">'+t.cpu+'</td></tr>'}).join(''),'no threads',3);
    var now=Date.now();
    fill('vif',d.ifaces.map(function(x){
      var p=prev[x.n],dt=p?(now-p.t)/1e3:0,dd=function(a,b){return Math.max(0,a-b)};
      var r=p&&dt>.2?{tx:dd(x.tx,p.tx)*8/dt,rx:dd(x.rx,p.rx)*8/dt,txp:Math.round(dd(x.txk,p.txk)/dt),rxp:Math.round(dd(x.rxk,p.rxk)/dt)}:{tx:0,rx:0,txp:0,rxp:0};
      prev[x.n]={t:now,tx:x.tx,rx:x.rx,txk:x.txk,rxk:x.rxk};
      return '<tr class="'+(x.up?'':'dis')+'"><td class="fl">'+(x.up?'R':'')+'</td><td class="nm">'+esc(x.n)+'</td><td class="n">'+esc(x.mtu)+'</td><td class="n r">'+bps(r.tx)+'</td><td class="n r">'+bps(r.rx)+'</td><td class="n">'+pps(r.txp)+'</td><td class="n">'+pps(r.rxp)+'</td><td class="n">'+x.drops+'</td><td class="n">'+x.rxe+'</td><td class="n">'+x.miss+'</td></tr>'}).join(''),'no interfaces',10);
    fill('vlc',d.lcp.map(function(x){return '<tr><td>'+esc(x.vpp)+'</td><td>'+esc(x.host)+'</td><td>'+esc(x.tap)+'</td><td>'+esc(x.type)+'</td></tr>'}).join(''),'no linux-cp pairs',4);
    fill('vbf',d.buffers.map(function(b){var u=b.total?(100*b.used/b.total).toFixed(1):'0.0';return '<tr><td>'+esc(b.name)+'</td><td class="n">'+b.numa+'</td><td class="n">'+b.size+'</td><td class="n">'+b.total+'</td><td class="n">'+b.avail+'</td><td class="n">'+b.used+'</td><td class="n">'+u+' %</td></tr>'}).join(''),'no buffer pools',7);
  }).catch(function(){});
}
poll();setInterval(poll,2000);
})();

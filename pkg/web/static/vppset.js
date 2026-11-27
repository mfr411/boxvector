// VPP settings (read-only): configured vs live values.
(function(){
function esc(s){return String(s).replace(/[&<>"]/g,function(c){return{'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]})}
function load(){
  fetch('/api/vppsettings',{cache:'no-store',credentials:'same-origin'}).then(function(r){return r.json()}).then(function(d){
    if(!d.available)return;
    var w=document.getElementById('vws');w.hidden=!d.warn.length;w.textContent=d.warn.join(' ');
    document.querySelector('#vst tbody').innerHTML=d.rows.map(function(x){
      return '<tr><td>'+esc(x.group)+'</td><td>'+esc(x.key)+'</td><td>'+esc(x.conf)+'</td><td>'+esc(x.live)+'</td></tr>'}).join('');
    document.getElementById('vpl').textContent=d.running?d.plugins.length+' loaded: '+d.plugins.join(', '):'VPP is not running';
  }).catch(function(){});
}
load();setInterval(load,5000);
})();

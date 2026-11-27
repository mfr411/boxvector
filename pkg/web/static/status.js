(function(){
var h=document.getElementById('s-host'),up=document.getElementById('s-up'),su=document.getElementById('s-su'),la=document.getElementById('s-la'),dt=document.getElementById('s-dt');
if(!up)return;var base=0,at=0;
function p2(n){return String(n).padStart(2,'0')}
function dur(s){s=Math.max(0,Math.floor(s));var d=Math.floor(s/86400);return(d?d+'d ':'')+p2(Math.floor(s%86400/3600))+':'+p2(Math.floor(s%3600/60))+':'+p2(s%60)}
function tick(){var d=new Date();dt.textContent=d.getFullYear()+'-'+p2(d.getMonth()+1)+'-'+p2(d.getDate())+' '+p2(d.getHours())+':'+p2(d.getMinutes())+':'+p2(d.getSeconds());if(at)up.textContent=dur(base+(Date.now()-at)/1e3)}
function poll(){fetch('/api/status',{cache:'no-store',credentials:'same-origin'}).then(function(r){return r.ok?r.json():null}).then(function(s){if(!s)return;base=s.up;at=Date.now();su.textContent=s.sess+'/'+s.tot;la.textContent=s.apply;if(s.host)h.textContent=s.host}).catch(function(){})}
poll();setInterval(poll,10000);tick();setInterval(tick,1000);
})();

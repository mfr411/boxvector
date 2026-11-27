// boxvector web UI: peer table filter and apply confirmation (no inline JS, CSP friendly).
document.addEventListener("DOMContentLoaded", function () {
  var f = document.getElementById("filter");
  if (f) {
    f.addEventListener("input", function () {
      var q = f.value.toLowerCase();
      document.querySelectorAll("#peers tbody tr").forEach(function (tr) {
        tr.style.display = tr.textContent.toLowerCase().indexOf(q) >= 0 ? "" : "none";
      });
    });
  }
  document.querySelectorAll("[data-confirm]").forEach(function (b) {
    b.addEventListener("click", function (e) {
      if (!confirm(b.getAttribute("data-confirm"))) e.preventDefault();
    });
  });
});

// Add peer: Form / Syntax tabs (works on the page and inside the popup)
window.bvMode=function(f,raw){var m=f.querySelector('input[name=mode]');if(!m)return;
  if(raw===undefined)raw=m.value==='raw';m.value=raw?'raw':'form';
  [].forEach.call(f.querySelectorAll('.mt'),function(x){x.classList.toggle('on',(x.dataset.m==='raw')===raw)});
  var p=f.querySelector('.rawp'),g=f.querySelector('.fgrp');if(!p||!g)return;p.hidden=!raw;g.hidden=raw;
  p.querySelector('textarea').disabled=!raw;
  [].forEach.call(g.querySelectorAll('input,select,textarea'),function(x){x.disabled=raw})};
document.addEventListener('click',function(e){var b=e.target.closest&&e.target.closest('.mt');if(!b)return;var f=b.closest('form');if(!f)return;
  bvMode(f,b.dataset.m==='raw');if(b.dataset.m==='raw')f.querySelector('.rawta').focus()});
document.addEventListener('DOMContentLoaded',function(){[].forEach.call(document.querySelectorAll('form.frm'),function(f){bvMode(f)})});

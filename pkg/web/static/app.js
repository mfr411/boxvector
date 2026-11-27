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

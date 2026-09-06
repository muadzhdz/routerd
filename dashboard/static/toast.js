// routerd dashboard — Cyber-Dark Glassmorphism Toast Notification
// Usage: toast("success" | "error" | "info", "Message text")

(function () {
  function getToast() {
    let t = document.getElementById("toast");
    if (!t) {
      t = document.createElement("div");
      t.id = "toast";
      document.body.appendChild(t);
    }
    return t;
  }

  let hideTimer = null;

  window.toast = function (type, msg) {
    const t = getToast();

    const icons = {
      success: `<svg width="16" height="16" fill="none" stroke="#10b981" stroke-width="2.5" viewBox="0 0 24 24"><polyline points="20 6 9 17 4 12"/></svg>`,
      error:   `<svg width="16" height="16" fill="none" stroke="#f43f5e" stroke-width="2.5" viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"/><line x1="15" y1="9" x2="9" y2="15"/><line x1="9" y1="9" x2="15" y2="15"/></svg>`,
      info:    `<svg width="16" height="16" fill="none" stroke="#06b6d4" stroke-width="2.5" viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>`,
    };

    const icon = icons[type] || icons.info;
    const toastClass = type === "ok" ? "success" : (type === "err" ? "error" : type);

    if (hideTimer) {
      clearTimeout(hideTimer);
      t.className = "";
      requestAnimationFrame(() => requestAnimationFrame(render));
    } else {
      render();
    }

    function render() {
      t.innerHTML = `
        <div style="display:flex;align-items:center;gap:10px;width:100%">
          <div style="flex-shrink:0;display:flex">${icon}</div>
          <span style="flex:1;line-height:1.4">${msg}</span>
        </div>
        <div class="toast-progress"></div>
      `;
      t.className = "show " + toastClass;
      hideTimer = setTimeout(() => {
        t.className = "";
        hideTimer = null;
      }, 3200);
    }
  };
})();

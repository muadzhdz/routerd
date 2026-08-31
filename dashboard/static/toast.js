// routerd dashboard — shared toast notification
// Style: portfolio glassmorphism, monochrome dark mode
// Usage: toast('success' | 'error' | 'info', 'message')

(function () {
  function getToast() {
    let t = document.getElementById('toast');
    if (!t) {
      t = document.createElement('div');
      t.id = 'toast';
      document.body.appendChild(t);
    }
    return t;
  }

  let hideTimer = null;

  window.toast = function (type, msg) {
    const t = getToast();

    // All types use the same monochrome icon — only border-left differs subtly
    const icons = {
      success: `<svg width="13" height="13" fill="none" stroke="currentColor" stroke-width="2.5" viewBox="0 0 24 24"><polyline points="20 6 9 17 4 12"/></svg>`,
      error:   `<svg width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"/><line x1="15" y1="9" x2="9" y2="15"/><line x1="9" y1="9" x2="15" y2="15"/></svg>`,
      info:    `<svg width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>`,
    };

    const icon = icons[type] || icons.info;

    if (hideTimer) {
      clearTimeout(hideTimer);
      t.className = '';
      requestAnimationFrame(() => requestAnimationFrame(() => render()));
    } else {
      render();
    }

    function render() {
      t.innerHTML = icon + msg;
      // All use 'ok' class for monochrome border — white for all types
      t.className = 'show ok';
      hideTimer = setTimeout(() => {
        t.className = '';
        hideTimer = null;
      }, 3200);
    }
  };
})();

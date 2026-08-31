// routerd dashboard — shared toast notification (portfolio style)
// Usage: toast('success'|'error'|'info', 'message')

(function () {
  // Inject the #toast element once
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

    // Map types to CSS class and icon SVG
    const map = {
      success: {
        cls: 'ok',
        icon: `<svg width="13" height="13" fill="none" stroke="currentColor" stroke-width="2.5" viewBox="0 0 24 24">
                 <polyline points="20 6 9 17 4 12"/>
               </svg>`,
      },
      error: {
        cls: 'err',
        icon: `<svg width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
                 <circle cx="12" cy="12" r="10"/>
                 <line x1="12" y1="8" x2="12" y2="12"/>
                 <line x1="12" y1="16" x2="12.01" y2="16"/>
               </svg>`,
      },
      info: {
        cls: 'info',
        icon: `<svg width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
                 <circle cx="12" cy="12" r="10"/>
                 <line x1="12" y1="8" x2="12" y2="12"/>
                 <line x1="12" y1="16" x2="12.01" y2="16"/>
               </svg>`,
      },
    };

    const cfg = map[type] || map.info;

    // Clear any pending hide
    if (hideTimer) {
      clearTimeout(hideTimer);
      t.className = '';
      // Brief gap so the slide-in animation re-triggers
      requestAnimationFrame(() => {
        requestAnimationFrame(() => render());
      });
    } else {
      render();
    }

    function render() {
      t.innerHTML = cfg.icon + msg;
      t.className = `show ${cfg.cls}`;
      hideTimer = setTimeout(() => {
        t.className = '';
        hideTimer = null;
      }, 3200);
    }
  };
})();

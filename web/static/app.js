// Relay dashboard JavaScript

// ── Dark mode ─────────────────────────────────────────────────────────────────
(function () {
  const DARK_KEY = 'relay_dark';
  const html = document.documentElement;

  function applyDark(on) {
    html.classList.toggle('dark', on);
    const darkIcon  = document.getElementById('dark-icon');
    const lightIcon = document.getElementById('light-icon');
    const label     = document.getElementById('dark-label');
    if (darkIcon)  darkIcon.style.display  = on ? 'none' : '';
    if (lightIcon) lightIcon.style.display = on ? '' : 'none';
    if (label)     label.textContent       = on ? 'Light Mode' : 'Dark Mode';
  }

  // Apply saved preference immediately (before paint).
  const saved = localStorage.getItem(DARK_KEY);
  const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
  applyDark(saved !== null ? saved === '1' : prefersDark);

  window.toggleDark = function () {
    const next = !html.classList.contains('dark');
    localStorage.setItem(DARK_KEY, next ? '1' : '0');
    applyDark(next);
  };
})();

// ── Dashboard live updates ────────────────────────────────────────────────────
document.addEventListener('DOMContentLoaded', () => {

  // Flash a monitor card when it receives a live WS update.
  document.body.addEventListener('htmx:wsAfterMessage', (e) => {
    const parser = new DOMParser();
    const doc = parser.parseFromString(e.detail.message, 'text/html');
    doc.querySelectorAll('[hx-swap-oob]').forEach(el => {
      const id = el.id;
      if (!id) return;
      requestAnimationFrame(() => {
        const live = document.getElementById(id);
        if (!live) return;
        live.classList.add('updated');
        setTimeout(() => live.classList.remove('updated'), 1200);
        // Re-bind tooltip listeners for replaced segments.
        live.querySelectorAll('.uptime-seg').forEach(bindSegTooltip);
      });
    });
  });

  // Auto-dismiss flash messages after 5 seconds.
  document.querySelectorAll('.flash').forEach(el => {
    setTimeout(() => {
      el.style.transition = 'opacity .4s';
      el.style.opacity = '0';
      setTimeout(() => el.remove(), 400);
    }, 5000);
  });

  // ── Uptime segment tooltips ───────────────────────────────────────────────
  const tooltip = document.createElement('div');
  tooltip.className = 'seg-tooltip';
  document.body.appendChild(tooltip);

  function bindSegTooltip(seg) {
    seg.addEventListener('mouseenter', e => {
      tooltip.textContent = seg.title;
      tooltip.style.display = 'block';
      positionTooltip(e);
    });
    seg.addEventListener('mousemove', positionTooltip);
    seg.addEventListener('mouseleave', () => { tooltip.style.display = 'none'; });
  }

  function positionTooltip(e) {
    tooltip.style.left = e.pageX + 12 + 'px';
    tooltip.style.top  = e.pageY - 36 + 'px';
  }

  document.querySelectorAll('.uptime-seg').forEach(bindSegTooltip);
});

// ── Monitor search & filter ───────────────────────────────────────────────────
window.filterMonitors = function (query) {
  const statusFilter = document.getElementById('status-filter');
  const status = statusFilter ? statusFilter.value : '';
  const q = query.toLowerCase().trim();

  document.querySelectorAll('.monitor-group').forEach(group => {
    let groupVisible = false;

    group.querySelectorAll('.monitor-card').forEach(card => {
      const name   = (card.querySelector('.monitor-name')?.textContent || '').toLowerCase();
      const target = (card.querySelector('.monitor-target')?.textContent || '').toLowerCase();
      const badge  = card.querySelector('.status-badge');
      const cardStatus = badge ? badge.className.replace(/.*status-badge-(\w+).*/, '$1') : '';

      const matchesQuery  = !q || name.includes(q) || target.includes(q);
      const matchesStatus = !status || cardStatus === status;
      const visible = matchesQuery && matchesStatus;

      card.style.display = visible ? '' : 'none';
      if (visible) groupVisible = true;
    });

    group.style.display = groupVisible ? '' : 'none';
  });
};

// ── Test monitor (dashboard card) ─────────────────────────────────────────────
window.testMonitor = function (id, btn) {
  btn.disabled = true;
  const resultEl = document.getElementById('test-' + id);

  fetch('/admin/monitors/' + id + '/test', { method: 'POST' })
    .then(r => r.json())
    .then(data => {
      btn.disabled = false;
      if (!resultEl) return;
      resultEl.style.display = '';
      const cls = data.status === 'up'
        ? 'flash flash-success'
        : data.status === 'degraded'
          ? 'flash flash-info'
          : 'flash flash-error';
      resultEl.className = 'test-result ' + cls;
      const latency = data.latency_ms ? ` <span style="font-family:monospace;margin-left:6px">${data.latency_ms}ms</span>` : '';
      resultEl.innerHTML = `<strong>${data.status.toUpperCase()}</strong> — ${data.detail || '(no detail)'}${latency}`;
      setTimeout(() => { resultEl.style.display = 'none'; }, 8000);
    })
    .catch(() => { btn.disabled = false; });
};

// ── Inline styles injected once ───────────────────────────────────────────────
const style = document.createElement('style');
style.textContent = `
  .monitor-card.updated {
    box-shadow: 0 0 0 2px var(--accent);
    transition: box-shadow 0.6s ease-out;
  }
  .seg-tooltip {
    position: absolute;
    display: none;
    background: #0f172a;
    color: #f8fafc;
    font-size: 12px;
    padding: 5px 10px;
    border-radius: 6px;
    pointer-events: none;
    z-index: 9999;
    white-space: nowrap;
    box-shadow: 0 4px 12px rgba(0,0,0,.3);
  }
`;
document.head.appendChild(style);

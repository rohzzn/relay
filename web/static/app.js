// Relay dashboard JavaScript
// Handles WebSocket live updates via HTMX WS extension.

document.addEventListener('DOMContentLoaded', () => {
  // HTMX WebSocket swap — the server sends HTML fragments with hx-swap-oob="true".
  // HTMX's WS extension handles this automatically when ws-connect is set on a
  // parent element. This file adds minor enhancements on top.

  // Flash a monitor card when it receives a live update.
  document.body.addEventListener('htmx:wsAfterMessage', (e) => {
    const parser = new DOMParser();
    const doc = parser.parseFromString(e.detail.message, 'text/html');
    doc.querySelectorAll('[hx-swap-oob]').forEach(el => {
      const id = el.id;
      if (!id) return;
      // After HTMX swaps the element, briefly highlight it.
      requestAnimationFrame(() => {
        const live = document.getElementById(id);
        if (!live) return;
        live.classList.add('updated');
        setTimeout(() => live.classList.remove('updated'), 1200);
      });
    });
  });

  // Auto-dismiss flash messages after 4 seconds.
  document.querySelectorAll('.flash-message').forEach(el => {
    setTimeout(() => el.remove(), 4000);
  });

  // Uptime segment tooltips: create a shared tooltip element.
  const tooltip = document.createElement('div');
  tooltip.className = 'seg-tooltip';
  document.body.appendChild(tooltip);

  document.querySelectorAll('.uptime-seg').forEach(seg => {
    seg.addEventListener('mouseenter', e => {
      tooltip.textContent = seg.title;
      tooltip.style.display = 'block';
      tooltip.style.left = e.pageX + 12 + 'px';
      tooltip.style.top  = e.pageY - 30 + 'px';
    });
    seg.addEventListener('mousemove', e => {
      tooltip.style.left = e.pageX + 12 + 'px';
      tooltip.style.top  = e.pageY - 30 + 'px';
    });
    seg.addEventListener('mouseleave', () => {
      tooltip.style.display = 'none';
    });
  });
});

// Inline styles for the update flash and tooltip (injected once).
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

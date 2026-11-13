// Argus UI Interactions (Vanilla JS, zero dependencies)
(function() {
  'use strict';

  // 1. Theme Management
  function initTheme() {
    const saved = localStorage.getItem('argus-theme');
    const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
    const theme = saved || (prefersDark ? 'dark' : 'light');
    document.documentElement.setAttribute('data-theme', theme);
  }

  function toggleTheme() {
    const current = document.documentElement.getAttribute('data-theme') || 'dark';
    const next = current === 'dark' ? 'light' : 'dark';
    document.documentElement.setAttribute('data-theme', next);
    localStorage.setItem('argus-theme', next);
  }

  // 2. TV Mode
  function initTVMode() {
    const urlParams = new URLSearchParams(window.location.search);
    if (urlParams.get('view') === 'tv') {
      document.body.classList.add('tv-mode');
    }
  }

  function toggleTVMode() {
    document.body.classList.toggle('tv-mode');
  }

  // 3. Search and Status Filter
  let activeFilter = 'all';
  let searchQuery = '';

  function applyFilters() {
    const cards = document.querySelectorAll('.card[data-system-id]');
    cards.forEach(card => {
      const status = card.getAttribute('data-status') || '';
      const name = (card.getAttribute('data-name') || '').toLowerCase();
      const id = (card.getAttribute('data-system-id') || '').toLowerCase();

      const matchesStatus = (activeFilter === 'all') || (status.toLowerCase() === activeFilter);
      const matchesSearch = !searchQuery || name.includes(searchQuery) || id.includes(searchQuery);

      if (matchesStatus && matchesSearch) {
        card.style.display = '';
      } else {
        card.style.display = 'none';
      }
    });
  }

  function setFilter(filter) {
    activeFilter = filter.toLowerCase();
    document.querySelectorAll('.chip').forEach(btn => {
      if (btn.getAttribute('data-filter') === activeFilter) {
        btn.classList.add('active');
      } else {
        btn.classList.remove('active');
      }
    });
    applyFilters();
  }

  // 4. Update Tab Title & Indicator on Stats Swap
  document.body.addEventListener('htmx:afterSwap', function(evt) {
    if (evt.detail.target.id === 'stats-container') {
      const downCountEl = document.getElementById('stat-down-val');
      const liveDot = document.getElementById('live-dot');
      if (liveDot) liveDot.classList.remove('disconnected');

      if (downCountEl) {
        const downs = parseInt(downCountEl.textContent.trim(), 10) || 0;
        if (downs > 0) {
          document.title = `(${downs}) Argus — Sistemas Fora`;
        } else {
          document.title = 'Argus — Operacional';
        }
      }
    }
    // Re-apply filters on updated cards
    if (evt.detail.target.classList.contains('card')) {
      applyFilters();
    }
  });

  // 5. Network / HTMX Error Handling
  document.body.addEventListener('htmx:responseError', function(evt) {
    const card = evt.detail.target;
    if (card && card.classList.contains('card')) {
      card.classList.add('stale');
      let warning = card.querySelector('.card__stale-warning');
      if (!warning) {
        warning = document.createElement('div');
        warning.className = 'card__stale-warning';
        warning.textContent = 'Sem resposta do servidor';
        card.prepend(warning);
      }
    }
    const liveDot = document.getElementById('live-dot');
    if (liveDot) liveDot.classList.add('disconnected');
  });

  document.body.addEventListener('htmx:sendError', function(evt) {
    const liveDot = document.getElementById('live-dot');
    if (liveDot) liveDot.classList.add('disconnected');
  });

  // 6. Keyboard Shortcuts
  window.addEventListener('keydown', function(e) {
    if (e.target.tagName === 'INPUT' || e.target.tagName === 'TEXTAREA') {
      if (e.key === 'Escape') {
        e.target.value = '';
        searchQuery = '';
        applyFilters();
        e.target.blur();
      }
      return;
    }
    if (e.key === '/') {
      e.preventDefault();
      const input = document.getElementById('search-input');
      if (input) input.focus();
    } else if (e.key === 't' || e.key === 'T') {
      toggleTheme();
    } else if (e.key === 'v' || e.key === 'V') {
      toggleTVMode();
    } else if (e.key === '1') {
      setFilter('all');
    } else if (e.key === '2') {
      setFilter('down');
    } else if (e.key === '3') {
      setFilter('degraded');
    } else if (e.key === '4') {
      setFilter('up');
    }
  });

  // Initialize
  window.addEventListener('DOMContentLoaded', function() {
    initTheme();
    initTVMode();

    const themeBtn = document.getElementById('theme-toggle-btn');
    if (themeBtn) themeBtn.addEventListener('click', toggleTheme);

    const tvBtn = document.getElementById('tv-toggle-btn');
    if (tvBtn) tvBtn.addEventListener('click', toggleTVMode);

    const searchInput = document.getElementById('search-input');
    if (searchInput) {
      searchInput.addEventListener('input', function(e) {
        searchQuery = e.target.value.trim().toLowerCase();
        applyFilters();
      });
    }

    document.querySelectorAll('.chip').forEach(btn => {
      btn.addEventListener('click', function() {
        setFilter(this.getAttribute('data-filter') || 'all');
      });
    });
  });
})();

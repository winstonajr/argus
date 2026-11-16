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
      document.documentElement.classList.add('tv-mode');
    }
  }

  function toggleTVMode() {
    document.documentElement.classList.toggle('tv-mode');
  }

  // 3. Search, Status and Group Filter
  let activeFilter = 'all';
  let activeGroup = 'all';
  let searchQuery = '';

  function applyFilters() {
    const cards = document.querySelectorAll('.card[data-system-id]');
    cards.forEach(card => {
      const status = card.getAttribute('data-status') || '';
      const group = (card.getAttribute('data-group') || '').toLowerCase();
      const name = (card.getAttribute('data-name') || '').toLowerCase();
      const id = (card.getAttribute('data-system-id') || '').toLowerCase();

      const matchesStatus = (activeFilter === 'all') || (status.toLowerCase() === activeFilter);
      const matchesGroup = (activeGroup === 'all') || (group === activeGroup);
      const matchesSearch = !searchQuery || name.includes(searchQuery) || id.includes(searchQuery);

      if (matchesStatus && matchesGroup && matchesSearch) {
        card.style.display = '';
      } else {
        card.style.display = 'none';
      }
    });
  }

  function initViewMode() {
    const btn = document.getElementById('view-toggle-btn');
    const isCompact = localStorage.getItem('argus-view') === 'compact';
    document.documentElement.classList.toggle('view-compact', isCompact);
    if (btn) btn.classList.toggle('active', isCompact);
  }

  function toggleViewMode() {
    const btn = document.getElementById('view-toggle-btn');
    const isCompact = document.documentElement.classList.toggle('view-compact');
    if (btn) btn.classList.toggle('active', isCompact);
    localStorage.setItem('argus-view', isCompact ? 'compact' : 'cards');
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

  // 4. Sound Alert (NOC)
  const prevStatuses = new Map();
  let soundEnabled = false;
  let audioCtx = null;

  function updateSoundButtonUI() {
    const btn = document.getElementById('sound-toggle-btn');
    if (!btn) return;
    const useEl = btn.querySelector('use');
    if (soundEnabled) {
      btn.classList.add('active');
      btn.setAttribute('title', 'Alerta sonoro (Ativo)');
      if (useEl) useEl.setAttribute('href', '#icon-bell');
    } else {
      btn.classList.remove('active');
      btn.setAttribute('title', 'Alerta sonoro (Silenciado)');
      if (useEl) useEl.setAttribute('href', '#icon-bell-off');
    }
  }

  function initSound() {
    soundEnabled = localStorage.getItem('argus-sound') === 'on';
    updateSoundButtonUI();
  }

  function toggleSound() {
    soundEnabled = !soundEnabled;
    localStorage.setItem('argus-sound', soundEnabled ? 'on' : 'off');
    updateSoundButtonUI();
    if (soundEnabled) {
      playBeep(520, 0.08);
    }
  }

  function playBeep(freq, dur) {
    try {
      const Ctx = window.AudioContext || window.webkitAudioContext;
      if (!Ctx) return;
      if (!audioCtx) audioCtx = new Ctx();
      if (audioCtx.state === 'suspended') audioCtx.resume();
      const osc = audioCtx.createOscillator();
      const gain = audioCtx.createGain();
      osc.type = 'sine';
      osc.frequency.value = freq;
      gain.gain.setValueAtTime(0.08, audioCtx.currentTime);
      gain.gain.exponentialRampToValueAtTime(0.001, audioCtx.currentTime + dur);
      osc.connect(gain);
      gain.connect(audioCtx.destination);
      osc.start();
      osc.stop(audioCtx.currentTime + dur);
    } catch (_) {
      // Ignore audio errors if blocked by browser policy
    }
  }

  function playDownAlert() {
    if (!soundEnabled) return;
    playBeep(440, 0.14);
    setTimeout(() => playBeep(330, 0.22), 160);
  }

  function checkTransitionAndNotify(card) {
    if (!card) return;
    const id = card.getAttribute('data-system-id');
    const status = (card.getAttribute('data-status') || '').toLowerCase();
    if (!id || !status) return;

    const prev = prevStatuses.get(id);
    if (prev && prev !== 'down' && prev !== 'unknown' && status === 'down') {
      playDownAlert();
    }
    prevStatuses.set(id, status);
  }

  function updateTabTitleFromStats() {
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

  // 5. Update Tab Title & Indicator on Stats/Card Swap
  document.body.addEventListener('htmx:afterSwap', function(evt) {
    if (evt.detail.target.id === 'stats-container') {
      updateTabTitleFromStats();
    }
    if (evt.detail.target.classList && evt.detail.target.classList.contains('card')) {
      checkTransitionAndNotify(evt.detail.target);
      applyFilters();
    }
  });

  // 6. SSE Live Updates with Fallback Polling
  let fallbackTimer = null;

  function startFallbackPolling() {
    if (fallbackTimer) return;
    fallbackTimer = setInterval(function() {
      if (window.htmx) {
        const stats = document.getElementById('stats-container');
        if (stats) htmx.ajax('GET', '/stats', { target: '#stats-container', swap: 'innerHTML' });
        document.querySelectorAll('.card[data-system-id]').forEach(card => {
          const id = card.getAttribute('data-system-id');
          if (id) htmx.ajax('GET', '/cards/' + encodeURIComponent(id), { target: '#card-' + id, swap: 'outerHTML' });
        });
      }
    }, 15000);
  }

  function stopFallbackPolling() {
    if (fallbackTimer) {
      clearInterval(fallbackTimer);
      fallbackTimer = null;
    }
  }

  function initSSE() {
    if (!document.getElementById('cards-grid')) {
      return;
    }
    if (typeof EventSource === 'undefined') {
      startFallbackPolling();
      return;
    }

    const es = new EventSource('/events');

    es.onopen = function() {
      stopFallbackPolling();
      const liveDot = document.getElementById('live-dot');
      if (liveDot) liveDot.classList.remove('disconnected');
    };

    es.addEventListener('card', function(e) {
      try {
        const payload = JSON.parse(e.data);
        const existing = document.getElementById('card-' + payload.id);
        if (!existing || !payload.html) return;

        const tpl = document.createElement('template');
        tpl.innerHTML = payload.html.trim();
        const nextCard = tpl.content.firstElementChild;
        if (!nextCard) return;

        existing.replaceWith(nextCard);
        if (window.htmx) htmx.process(nextCard);
        checkTransitionAndNotify(nextCard);
        applyFilters();
      } catch (_) {}
    });

    es.addEventListener('stats', function(e) {
      try {
        const payload = JSON.parse(e.data);
        const statsEl = document.getElementById('stats-container');
        if (!statsEl || !payload.html) return;
        statsEl.innerHTML = payload.html;
        updateTabTitleFromStats();
      } catch (_) {}
    });

    es.onerror = function() {
      const liveDot = document.getElementById('live-dot');
      if (liveDot) liveDot.classList.add('disconnected');
      startFallbackPolling();
    };
  }

  // 5. Network / HTMX Error Handling
  function markCardStale(card) {
    if (card && card.classList && card.classList.contains('card')) {
      card.classList.add('stale');
      let warning = card.querySelector('.card__stale-warning');
      if (!warning) {
        warning = document.createElement('div');
        warning.className = 'card__stale-warning';
        warning.textContent = 'Sem resposta';
        card.appendChild(warning);
      }
    }
  }

  document.body.addEventListener('htmx:responseError', function(evt) {
    markCardStale(evt.detail.target);
    const liveDot = document.getElementById('live-dot');
    if (liveDot) liveDot.classList.add('disconnected');
  });

  document.body.addEventListener('htmx:sendError', function(evt) {
    markCardStale(evt.detail.target);
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

  function seedInitialStatuses() {
    document.querySelectorAll('.card[data-system-id]').forEach(card => {
      const id = card.getAttribute('data-system-id');
      const status = (card.getAttribute('data-status') || '').toLowerCase();
      if (id && status) {
        prevStatuses.set(id, status);
      }
    });
  }

  // Initialize
  window.addEventListener('DOMContentLoaded', function() {
    initTheme();
    initTVMode();
    initViewMode();
    initSound();
    seedInitialStatuses();
    updateTabTitleFromStats();
    initSSE();

    const themeBtn = document.getElementById('theme-toggle-btn');
    if (themeBtn) themeBtn.addEventListener('click', toggleTheme);

    const viewBtn = document.getElementById('view-toggle-btn');
    if (viewBtn) viewBtn.addEventListener('click', toggleViewMode);

    const soundBtn = document.getElementById('sound-toggle-btn');
    if (soundBtn) soundBtn.addEventListener('click', toggleSound);

    const groupSelect = document.getElementById('group-filter');
    if (groupSelect) {
      groupSelect.addEventListener('change', function(e) {
        activeGroup = (e.target.value || 'all').toLowerCase();
        applyFilters();
      });
    }

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

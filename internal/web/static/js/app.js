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
    const isTV = document.documentElement.classList.toggle('tv-mode');
    try {
      const url = new URL(window.location.href);
      if (isTV) {
        url.searchParams.set('view', 'tv');
      } else {
        url.searchParams.delete('view');
      }
      window.history.replaceState(null, '', url.toString());
    } catch (_) {}
  }

  // 3. Search, Status and Group Filter
  let activeFilter = 'all';
  let activeGroup = 'all';
  let searchQuery = '';

  function syncFiltersToURL() {
    if (!document.getElementById('cards-grid')) return;
    try {
      const url = new URL(window.location.href);
      if (activeFilter && activeFilter !== 'all') {
        url.searchParams.set('status', activeFilter);
      } else {
        url.searchParams.delete('status');
      }
      const groupSelect = document.getElementById('group-filter');
      if (activeGroup && activeGroup !== 'all') {
        url.searchParams.set('group', groupSelect ? groupSelect.value : activeGroup);
      } else {
        url.searchParams.delete('group');
      }
      const searchInput = document.getElementById('search-input');
      const rawQuery = searchInput ? searchInput.value.trim() : searchQuery;
      if (rawQuery) {
        url.searchParams.set('q', rawQuery);
      } else {
        url.searchParams.delete('q');
      }
      window.history.replaceState(null, '', url.toString());
    } catch (_) {}
  }

  function initFiltersFromURL() {
    if (!document.getElementById('cards-grid')) return;
    const params = new URLSearchParams(window.location.search);

    const statusParam = (params.get('status') || '').toLowerCase();
    if (statusParam === 'all' || statusParam === 'down' || statusParam === 'degraded' || statusParam === 'up') {
      activeFilter = statusParam;
      document.querySelectorAll('.chip').forEach(btn => {
        btn.classList.toggle('active', btn.getAttribute('data-filter') === activeFilter);
      });
    }

    const groupParam = params.get('group');
    const groupSelect = document.getElementById('group-filter');
    if (groupParam && groupSelect) {
      const targetLower = groupParam.toLowerCase();
      for (let i = 0; i < groupSelect.options.length; i++) {
        if (groupSelect.options[i].value.toLowerCase() === targetLower) {
          groupSelect.selectedIndex = i;
          activeGroup = targetLower;
          break;
        }
      }
    }

    const qParam = params.get('q');
    const searchInput = document.getElementById('search-input');
    if (qParam && searchInput) {
      searchInput.value = qParam;
      searchQuery = qParam.trim().toLowerCase();
    }

    applyFilters();
  }

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
    syncFiltersToURL();
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

  // 5. Drag Update Queue & HTMX Swap Guard
  let isDraggingCard = false;
  const pendingCardUpdates = new Map();

  function deduplicateCards(grid) {
    if (!grid) return;
    const seen = new Set();
    grid.querySelectorAll('.card[data-system-id]').forEach(card => {
      const id = card.getAttribute('data-system-id');
      if (!id) return;
      if (seen.has(id)) {
        card.remove();
      } else {
        seen.add(id);
      }
    });
  }

  function applyCardHTML(id, html) {
    const grid = document.getElementById('cards-grid');
    if (!grid || !id || !html) return;

    const matches = [];
    grid.querySelectorAll('.card[data-system-id]').forEach(card => {
      if (card.getAttribute('data-system-id') === id) {
        matches.push(card);
      }
    });
    if (matches.length === 0) return;
    for (let i = 1; i < matches.length; i++) {
      matches[i].remove();
    }
    const existing = matches[0];

    const tpl = document.createElement('template');
    tpl.innerHTML = html.trim();
    const nextCard = tpl.content.firstElementChild;
    if (!nextCard) return;

    existing.replaceWith(nextCard);
    if (window.htmx) htmx.process(nextCard);
    checkTransitionAndNotify(nextCard);
    applyCurrentSort();
    applyFilters();
  }

  function flushPendingCardUpdates() {
    if (pendingCardUpdates.size === 0) return;
    const entries = Array.from(pendingCardUpdates.entries());
    pendingCardUpdates.clear();
    entries.forEach(([id, html]) => applyCardHTML(id, html));
  }

  document.body.addEventListener('htmx:beforeSwap', function(evt) {
    const target = evt.detail && evt.detail.target;
    if (isDraggingCard && target && target.classList && target.classList.contains('card')) {
      const id = target.getAttribute('data-system-id');
      const response = evt.detail.xhr ? evt.detail.xhr.responseText : '';
      if (id && response) {
        pendingCardUpdates.set(id, response);
      }
      evt.detail.shouldSwap = false;
    }
  });

  document.body.addEventListener('htmx:afterSwap', function(evt) {
    if (evt.detail.target.id === 'stats-container') {
      updateTabTitleFromStats();
    }
    if (evt.detail.target.classList && evt.detail.target.classList.contains('card')) {
      checkTransitionAndNotify(evt.detail.target);
      applyCurrentSort();
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
        if (isDraggingCard) return;
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
        if (!payload || !payload.id || !payload.html) return;
        if (isDraggingCard) {
          pendingCardUpdates.set(payload.id, payload.html);
          return;
        }
        applyCardHTML(payload.id, payload.html);
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
        syncFiltersToURL();
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

  // 7. Client-side Card Sorting & Drag & Drop Reordering
  const CARD_ORDER_KEY = 'argus-card-order';
  const SORT_MODE_KEY = 'argus-sort';
  const SEVERITY_WEIGHT = { down: 3, degraded: 2, unknown: 1, up: 0 };
  let sortMode = 'manual';

  function updateSortButtonUI() {
    const btn = document.getElementById('sort-toggle-btn');
    if (!btn) return;
    const isSeverity = sortMode === 'severity';
    btn.classList.toggle('active', isSeverity);
    btn.setAttribute(
      'title',
      isSeverity
        ? 'Ordem por prioridade ativa (críticos no topo) — clique para ordem manual/alfabética'
        : 'Ordenar por prioridade (críticos no topo)'
    );
  }

  function sortCardsBySeverity(grid) {
    if (!grid) return;
    deduplicateCards(grid);
    const cards = Array.from(grid.querySelectorAll('.card[data-system-id]'));
    cards.sort((a, b) => {
      const sa = SEVERITY_WEIGHT[(a.getAttribute('data-status') || '').toLowerCase()] || 0;
      const sb = SEVERITY_WEIGHT[(b.getAttribute('data-status') || '').toLowerCase()] || 0;
      if (sa !== sb) return sb - sa;
      const na = (a.getAttribute('data-name') || '').toLowerCase();
      const nb = (b.getAttribute('data-name') || '').toLowerCase();
      return na.localeCompare(nb);
    });
    const frag = document.createDocumentFragment();
    cards.forEach(c => frag.appendChild(c));
    grid.appendChild(frag);
  }

  function sortCardsAlphabetically(grid) {
    if (!grid) return;
    deduplicateCards(grid);
    const cards = Array.from(grid.querySelectorAll('.card[data-system-id]'));
    cards.sort((a, b) => {
      const na = (a.getAttribute('data-name') || '').toLowerCase();
      const nb = (b.getAttribute('data-name') || '').toLowerCase();
      if (na !== nb) return na.localeCompare(nb);
      return (a.getAttribute('data-system-id') || '').localeCompare(b.getAttribute('data-system-id') || '');
    });
    const frag = document.createDocumentFragment();
    cards.forEach(c => frag.appendChild(c));
    grid.appendChild(frag);
  }

  function restoreCardOrder() {
    const grid = document.getElementById('cards-grid');
    if (!grid) return;
    deduplicateCards(grid);

    let savedOrder;
    try {
      savedOrder = JSON.parse(localStorage.getItem(CARD_ORDER_KEY) || '[]');
    } catch (_) {
      return;
    }
    if (!Array.isArray(savedOrder) || savedOrder.length === 0) {
      sortCardsAlphabetically(grid);
      return;
    }

    const byId = new Map();
    grid.querySelectorAll('.card[data-system-id]').forEach(card => {
      byId.set(card.getAttribute('data-system-id'), card);
    });

    const frag = document.createDocumentFragment();
    savedOrder.forEach(id => {
      const card = byId.get(id);
      if (card) {
        frag.appendChild(card);
        byId.delete(id);
      }
    });
    byId.forEach(card => frag.appendChild(card));
    grid.appendChild(frag);
  }

  function applyCurrentSort() {
    const grid = document.getElementById('cards-grid');
    if (!grid) return;
    if (sortMode === 'severity') {
      sortCardsBySeverity(grid);
    }
  }

  function initSortMode() {
    sortMode = localStorage.getItem(SORT_MODE_KEY) === 'severity' ? 'severity' : 'manual';
    updateSortButtonUI();
    if (sortMode === 'severity') {
      sortCardsBySeverity(document.getElementById('cards-grid'));
    } else {
      restoreCardOrder();
    }
  }

  function toggleSortMode() {
    sortMode = sortMode === 'severity' ? 'manual' : 'severity';
    try {
      localStorage.setItem(SORT_MODE_KEY, sortMode);
    } catch (_) {}
    updateSortButtonUI();
    const grid = document.getElementById('cards-grid');
    if (sortMode === 'severity') {
      sortCardsBySeverity(grid);
    } else {
      restoreCardOrder();
    }
  }

  function saveCardOrder() {
    const grid = document.getElementById('cards-grid');
    if (!grid) return;
    deduplicateCards(grid);
    const order = Array.from(grid.querySelectorAll('.card[data-system-id]'))
      .map(card => card.getAttribute('data-system-id'))
      .filter(Boolean);
    try {
      localStorage.setItem(CARD_ORDER_KEY, JSON.stringify(order));
    } catch (_) {}
  }

  function initCardDragAndDrop() {
    const grid = document.getElementById('cards-grid');
    if (!grid) return;

    let draggedCard = null;
    let suppressClickUntil = 0;

    function cleanupDrag() {
      if (draggedCard) {
        draggedCard.classList.remove('dragging');
      }
      grid.querySelectorAll('.card.dragging').forEach(c => c.classList.remove('dragging'));
      if (isDraggingCard || draggedCard) {
        draggedCard = null;
        isDraggingCard = false;
        suppressClickUntil = Date.now() + 80;
        if (sortMode === 'severity') {
          sortMode = 'manual';
          try {
            localStorage.setItem(SORT_MODE_KEY, 'manual');
          } catch (_) {}
          updateSortButtonUI();
        }
        saveCardOrder();
        flushPendingCardUpdates();
      }
    }

    grid.addEventListener('dragstart', function(e) {
      const card = e.target.closest('.card[data-system-id]');
      if (!card || !grid.contains(card)) return;
      draggedCard = card;
      isDraggingCard = true;
      card.classList.add('dragging');
      if (e.dataTransfer) {
        e.dataTransfer.effectAllowed = 'move';
        e.dataTransfer.setData('text/plain', card.getAttribute('data-system-id') || '');
      }
    });

    grid.addEventListener('dragover', function(e) {
      if (!draggedCard || !grid.contains(draggedCard)) {
        if (isDraggingCard || draggedCard) cleanupDrag();
        return;
      }
      e.preventDefault();
      if (e.dataTransfer) e.dataTransfer.dropEffect = 'move';

      const targetCard = e.target.closest('.card[data-system-id]');
      if (!targetCard || targetCard === draggedCard || targetCard.parentNode !== grid) return;

      const isFollowing = (draggedCard.compareDocumentPosition(targetCard) & Node.DOCUMENT_POSITION_FOLLOWING) !== 0;
      const draggedRect = draggedCard.getBoundingClientRect();
      const targetRect = targetCard.getBoundingClientRect();
      const sameRow = Math.abs(draggedRect.top - targetRect.top) < targetRect.height * 0.5;
      if (sameRow) {
        const pastMidpoint = e.clientX >= targetRect.left + targetRect.width / 2;
        if (isFollowing && !pastMidpoint) return;
        if (!isFollowing && pastMidpoint) return;
      }

      if (isFollowing) {
        targetCard.after(draggedCard);
      } else {
        targetCard.before(draggedCard);
      }
    });

    document.addEventListener('drop', function(e) {
      if (!isDraggingCard && !draggedCard) return;
      if (grid.contains(e.target)) {
        e.preventDefault();
      }
      cleanupDrag();
    });

    document.addEventListener('dragend', function() {
      cleanupDrag();
    });

    grid.addEventListener('click', function(e) {
      if (Date.now() < suppressClickUntil) {
        e.preventDefault();
      }
    });
  }

  function initHistoryActions() {
    const maintBtn = document.getElementById('maintenance-toggle-btn');
    if (maintBtn) {
      maintBtn.addEventListener('click', async function() {
        const id = maintBtn.getAttribute('data-system-id');
        if (!id || maintBtn.disabled) return;
        maintBtn.disabled = true;
        try {
          const resp = await fetch('/api/systems/' + encodeURIComponent(id) + '/maintenance', {
            method: 'POST'
          });
          if (resp.ok) {
            const data = await resp.json();
            const active = Boolean(data.in_maintenance);
            maintBtn.classList.toggle('active', active);
            maintBtn.setAttribute('data-maintenance', active ? 'true' : 'false');
            maintBtn.textContent = active ? 'Desativar Manutenção' : 'Modo Manutenção';
          }
        } catch (_) {
        } finally {
          maintBtn.disabled = false;
        }
      });
    }

    const checkBtn = document.getElementById('check-now-btn');
    if (checkBtn) {
      checkBtn.addEventListener('click', async function() {
        const id = checkBtn.getAttribute('data-system-id');
        if (!id || checkBtn.disabled) return;
        checkBtn.disabled = true;
        const originalText = checkBtn.textContent;
        checkBtn.textContent = 'Verificando...';
        try {
          const resp = await fetch('/api/systems/' + encodeURIComponent(id) + '/check', {
            method: 'POST'
          });
          if (resp.status === 429) {
            checkBtn.textContent = 'Aguarde 3s...';
          } else if (resp.ok) {
            checkBtn.textContent = 'Agendado!';
            setTimeout(() => { window.location.reload(); }, 1500);
            return;
          } else {
            checkBtn.textContent = 'Erro';
          }
        } catch (_) {
          checkBtn.textContent = 'Erro';
        }
        setTimeout(() => {
          checkBtn.textContent = originalText;
          checkBtn.disabled = false;
        }, 1500);
      });
    }
  }

  // Initialize
  window.addEventListener('DOMContentLoaded', function() {
    initTheme();
    initTVMode();
    initViewMode();
    initSound();
    seedInitialStatuses();
    initSortMode();
    initCardDragAndDrop();
    initFiltersFromURL();
    initHistoryActions();
    updateTabTitleFromStats();
    initSSE();

    const themeBtn = document.getElementById('theme-toggle-btn');
    if (themeBtn) themeBtn.addEventListener('click', toggleTheme);

    const sortBtn = document.getElementById('sort-toggle-btn');
    if (sortBtn) sortBtn.addEventListener('click', toggleSortMode);

    const viewBtn = document.getElementById('view-toggle-btn');
    if (viewBtn) viewBtn.addEventListener('click', toggleViewMode);

    const soundBtn = document.getElementById('sound-toggle-btn');
    if (soundBtn) soundBtn.addEventListener('click', toggleSound);

    const groupSelect = document.getElementById('group-filter');
    if (groupSelect) {
      groupSelect.addEventListener('change', function(e) {
        activeGroup = (e.target.value || 'all').toLowerCase();
        applyFilters();
        syncFiltersToURL();
      });
    }

    const tvBtn = document.getElementById('tv-toggle-btn');
    if (tvBtn) tvBtn.addEventListener('click', toggleTVMode);

    const searchInput = document.getElementById('search-input');
    if (searchInput) {
      searchInput.addEventListener('input', function(e) {
        searchQuery = e.target.value.trim().toLowerCase();
        applyFilters();
        syncFiltersToURL();
      });
    }

    document.querySelectorAll('.chip').forEach(btn => {
      btn.addEventListener('click', function() {
        setFilter(this.getAttribute('data-filter') || 'all');
      });
    });
  });
})();

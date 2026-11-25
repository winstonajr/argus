(function() {
  'use strict';

  function dataset(key, fallback) {
    return (document.body && document.body.dataset && document.body.dataset[key]) || fallback;
  }

  function formatMsg(tpl, val) {
    return (tpl || '').replace('%d', String(val));
  }

  // 1. Toast Notification System
  function showToast(message, variant) {
    const container = document.getElementById('toast-container');
    if (!container || !message) return;
    const el = document.createElement('div');
    el.className = 'toast' + (variant ? ' toast--' + variant : '');
    el.textContent = message;
    container.appendChild(el);
    setTimeout(function() {
      el.remove();
    }, 3800);
  }

  // 2. Theme, TV Mode, View Mode & Language
  function getSavedThemeChoice() {
    try {
      return localStorage.getItem('argus-theme') || 'auto';
    } catch (_) {
      return 'auto';
    }
  }

  function syncThemeUI(theme) {
    const isLight = theme === 'light' || theme === 'latte';
    const btn = document.getElementById('theme-toggle-btn');
    if (btn) {
      const useEl = btn.querySelector('use');
      if (useEl) {
        useEl.setAttribute('href', isLight ? '#icon-sun' : '#icon-moon');
      }
    }
    const metaTheme = document.querySelector('meta[name="theme-color"]');
    if (metaTheme) {
      const bg = getComputedStyle(document.documentElement).getPropertyValue('--bg').trim();
      metaTheme.setAttribute('content', bg || (isLight ? '#F6F7F9' : '#0E1013'));
    }
    const choice = getSavedThemeChoice();
    document.querySelectorAll('.theme-menu__item[data-theme-choice]').forEach(function(item) {
      item.classList.toggle('active', item.getAttribute('data-theme-choice') === choice);
    });
  }

  function applyThemeChoice(choice) {
    const resolved = window.__argusResolveTheme ? window.__argusResolveTheme(choice) : (choice === 'auto' ? 'dark' : choice);
    try {
      if (choice === 'auto') {
        localStorage.removeItem('argus-theme');
      } else {
        localStorage.setItem('argus-theme', choice);
      }
    } catch (_) {}
    document.documentElement.setAttribute('data-theme', resolved);
    syncThemeUI(resolved);
    renderSeasonalParticles(resolved);
  }

  const SEASONAL_PARTICLES = {
    halloween: { icons: ['bat.svg', 'ghost.svg'], colors: ['#A855F7', '#F97316'], anim: 'seasonal-fall', sway: true, count: 14, durMin: 14, durMax: 24 },
    christmas: { icons: ['snowflake.svg'], colors: ['#A7F3D0', '#ECF5F0'], anim: 'seasonal-fall', sway: true, count: 18, durMin: 11, durMax: 22 },
    easter: { icons: ['egg.svg', 'flower.svg'], colors: ['#C084FC', '#5EEAD4', '#FDE047'], anim: 'seasonal-fall', sway: true, count: 12, durMin: 14, durMax: 24 },
    carnival: { icons: ['confetti.svg', 'mask.svg'], colors: ['#FACC15', '#2DD4BF', '#F43F5E', '#A855F7'], anim: 'seasonal-fall', sway: true, count: 18, durMin: 9, durMax: 17 },
    junina: { icons: ['lantern.svg', 'stars.svg'], colors: ['#FB923C', '#FACC15'], anim: 'seasonal-rise', sway: true, count: 14, durMin: 12, durMax: 21 },
    brazil: { icons: ['stars.svg'], colors: ['#FACC15', '#22C55E', '#38BDF8'], anim: 'seasonal-twinkle', sway: false, count: 15, durMin: 3, durMax: 6 },
    spring: { icons: ['flower.svg', 'leaf.svg'], colors: ['#F472B6', '#FACC15', '#34D399'], anim: 'seasonal-fall', sway: true, count: 15, durMin: 12, durMax: 21 },
    newyear: { icons: ['sparkles.svg', 'stars.svg'], colors: ['#EAB308', '#34D399', '#F8FAFC'], anim: 'seasonal-rise', sway: true, count: 16, durMin: 10, durMax: 18 }
  };

  function renderSeasonalParticles(theme) {
    const existing = document.getElementById('seasonal-particles');
    if (existing) existing.remove();
    const spec = SEASONAL_PARTICLES[theme];
    if (!spec || !document.body) return;
    if (window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches) return;

    const container = document.createElement('div');
    container.id = 'seasonal-particles';
    container.className = 'seasonal-particles' + (document.hidden ? ' is-paused' : '');
    container.setAttribute('aria-hidden', 'true');

    for (let i = 0; i < spec.count; i++) {
      const el = document.createElement('span');
      el.className = 'seasonal-particle';
      const inner = document.createElement('span');
      inner.className = 'seasonal-particle__inner';

      const icon = spec.icons[i % spec.icons.length];
      const color = spec.colors[i % spec.colors.length];
      const depth = i % 2 === 0 ? 0.78 : 1.15;
      const size = Math.round((13 + ((i * 7) % 10)) * depth);
      const left = (i * (100 / spec.count) + ((i * 13) % 7)) % 98;
      const dur = (spec.durMin + ((i * 1.7) % (spec.durMax - spec.durMin))) / (depth > 1 ? 1.08 : 0.92);
      const delay = -((i * 2.3) % dur);
      const drift = ((i % 2 === 0 ? 1 : -1) * (18 + ((i * 9) % 30))) + 'px';
      const swayAmp = (8 + ((i * 5) % 14)) + 'px';
      const rot = (14 + ((i * 19) % 28)) + 'deg';
      const swayDur = (3.2 + (i % 4) * 0.9).toFixed(1) + 's';

      el.style.left = left.toFixed(1) + '%';
      if (spec.anim === 'seasonal-twinkle') {
        el.style.top = (8 + ((i * 17) % 82)) + '%';
      }
      el.style.width = size + 'px';
      el.style.height = size + 'px';
      el.style.setProperty('--p-opacity', (depth > 1 ? 0.22 : 0.13).toFixed(2));
      el.style.setProperty('--p-drift', drift);
      el.style.animation = spec.anim + ' ' + dur.toFixed(1) + 's linear ' + delay.toFixed(1) + 's infinite';

      inner.style.backgroundColor = color;
      inner.style.webkitMaskImage = "url('/static/seasonal/" + icon + "')";
      inner.style.maskImage = "url('/static/seasonal/" + icon + "')";
      if (spec.sway) {
        inner.style.setProperty('--p-sway', swayAmp);
        inner.style.setProperty('--p-rot', rot);
        inner.style.animation = 'seasonal-sway ' + swayDur + ' ease-in-out ' + (-i * 0.6).toFixed(1) + 's infinite';
      }

      el.appendChild(inner);
      container.appendChild(el);
    }
    document.body.prepend(container);
  }

  document.addEventListener('visibilitychange', function() {
    const p = document.getElementById('seasonal-particles');
    if (p) p.classList.toggle('is-paused', document.hidden);
  });

  function toggleTheme() {
    const current = document.documentElement.getAttribute('data-theme') || 'dark';
    const next = current === 'light' ? 'dark' : 'light';
    applyThemeChoice(next);
  }

  function initThemePicker() {
    const picker = document.getElementById('theme-picker');
    const btn = document.getElementById('theme-toggle-btn');
    const menu = document.getElementById('theme-menu');
    if (!picker || !btn || !menu) return;

    function closeMenu() {
      menu.hidden = true;
      btn.setAttribute('aria-expanded', 'false');
    }

    btn.addEventListener('click', function(e) {
      e.stopPropagation();
      const willOpen = menu.hidden;
      menu.hidden = !willOpen;
      btn.setAttribute('aria-expanded', willOpen ? 'true' : 'false');
    });

    menu.addEventListener('click', function(e) {
      const item = e.target.closest('.theme-menu__item[data-theme-choice]');
      if (!item) return;
      applyThemeChoice(item.getAttribute('data-theme-choice'));
      closeMenu();
    });

    document.addEventListener('click', function(e) {
      if (!menu.hidden && !picker.contains(e.target)) {
        closeMenu();
      }
    });

    document.addEventListener('keydown', function(e) {
      if (e.key === 'Escape' && !menu.hidden) {
        closeMenu();
      }
    });
  }

  function initTVMode() {
    const btn = document.getElementById('tv-toggle-btn');
    if (!document.getElementById('cards-grid')) {
      document.documentElement.classList.remove('tv-mode');
      return;
    }
    const isTV = document.documentElement.classList.contains('tv-mode');
    if (btn) {
      btn.classList.toggle('active', isTV);
      btn.setAttribute('aria-pressed', isTV ? 'true' : 'false');
    }
  }

  function toggleTVMode() {
    const btn = document.getElementById('tv-toggle-btn');
    if (!document.getElementById('cards-grid')) {
      if (btn) {
        window.location.assign('/?view=tv');
      }
      return;
    }
    const isTV = document.documentElement.classList.toggle('tv-mode');
    if (btn) {
      btn.classList.toggle('active', isTV);
      btn.setAttribute('aria-pressed', isTV ? 'true' : 'false');
    }
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

  function initViewMode() {
    const btn = document.getElementById('view-toggle-btn');
    const isCompact = localStorage.getItem('argus-view') === 'compact';
    document.documentElement.classList.toggle('view-compact', isCompact);
    if (btn) {
      btn.classList.toggle('active', isCompact);
      btn.setAttribute('aria-pressed', isCompact ? 'true' : 'false');
    }
  }

  function toggleViewMode() {
    const btn = document.getElementById('view-toggle-btn');
    const isCompact = document.documentElement.classList.toggle('view-compact');
    if (btn) {
      btn.classList.toggle('active', isCompact);
      btn.setAttribute('aria-pressed', isCompact ? 'true' : 'false');
    }
    try {
      localStorage.setItem('argus-view', isCompact ? 'compact' : 'cards');
    } catch (_) {}
  }

  function toggleLanguage() {
    const currentLang = (document.documentElement.getAttribute('lang') || 'pt-BR').toLowerCase();
    const nextLang = currentLang.startsWith('en') ? 'pt-BR' : 'en';
    document.cookie = 'argus-lang=' + encodeURIComponent(nextLang) + '; path=/; max-age=31536000; SameSite=Lax';
    try {
      const url = new URL(window.location.href);
      url.searchParams.delete('lang');
      window.location.assign(url.toString());
    } catch (_) {
      window.location.reload();
    }
  }

  // 3. Search, Status & Group Filters + Chip Counters + Empty State
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

  function updateChipCounts() {
    const cards = document.querySelectorAll('.card[data-system-id]');
    if (cards.length === 0) return;
    let total = 0;
    let down = 0;
    let degraded = 0;
    let up = 0;
    cards.forEach(function(card) {
      total++;
      const st = (card.getAttribute('data-status') || '').toLowerCase();
      if (st === 'down') down++;
      else if (st === 'degraded') degraded++;
      else if (st === 'up') up++;
    });
    const setBadge = function(id, val) {
      const el = document.getElementById(id);
      if (el) el.textContent = String(val);
    };
    setBadge('chip-count-all', total);
    setBadge('chip-count-down', down);
    setBadge('chip-count-degraded', degraded);
    setBadge('chip-count-up', up);
  }

  function applyFilters() {
    const cards = document.querySelectorAll('.card[data-system-id]');
    let visibleCount = 0;
    cards.forEach(function(card) {
      const status = (card.getAttribute('data-status') || '').toLowerCase();
      const group = (card.getAttribute('data-group') || '').toLowerCase();
      const name = (card.getAttribute('data-name') || '').toLowerCase();
      const id = (card.getAttribute('data-system-id') || '').toLowerCase();

      const matchesStatus = (activeFilter === 'all') || (status === activeFilter);
      const matchesGroup = (activeGroup === 'all') || (group === activeGroup);
      const matchesSearch = !searchQuery || name.includes(searchQuery) || id.includes(searchQuery);

      if (matchesStatus && matchesGroup && matchesSearch) {
        card.style.display = '';
        visibleCount++;
      } else {
        card.style.display = 'none';
      }
    });

    const emptyEl = document.getElementById('empty-filter-state');
    if (emptyEl) {
      emptyEl.hidden = (cards.length === 0 || visibleCount > 0);
    }
    updateChipCounts();
  }

  function setFilter(filter) {
    activeFilter = (filter || 'all').toLowerCase();
    document.querySelectorAll('.chip').forEach(function(btn) {
      const isMatch = btn.getAttribute('data-filter') === activeFilter;
      btn.classList.toggle('active', isMatch);
      btn.setAttribute('aria-pressed', isMatch ? 'true' : 'false');
    });
    applyFilters();
    syncFiltersToURL();
  }

  function clearAllFilters() {
    activeFilter = 'all';
    activeGroup = 'all';
    searchQuery = '';
    const searchInput = document.getElementById('search-input');
    if (searchInput) searchInput.value = '';
    const groupSelect = document.getElementById('group-filter');
    if (groupSelect) groupSelect.selectedIndex = 0;
    setFilter('all');
  }

  function initFiltersFromURL() {
    if (!document.getElementById('cards-grid')) return;
    const params = new URLSearchParams(window.location.search);

    const statusParam = (params.get('status') || '').toLowerCase();
    if (statusParam === 'all' || statusParam === 'down' || statusParam === 'degraded' || statusParam === 'up') {
      activeFilter = statusParam;
      document.querySelectorAll('.chip').forEach(function(btn) {
        const isMatch = btn.getAttribute('data-filter') === activeFilter;
        btn.classList.toggle('active', isMatch);
        btn.setAttribute('aria-pressed', isMatch ? 'true' : 'false');
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

  function bindStatsBarFilterClicks() {
    const container = document.getElementById('stats-container');
    if (!container) return;
    function activate(target) {
      const triggerEl = target.closest('[data-filter-trigger]');
      if (!triggerEl) return;
      const targetFilter = triggerEl.getAttribute('data-filter-trigger');
      if (targetFilter) {
        setFilter(targetFilter);
      }
    }
    container.addEventListener('click', function(e) {
      activate(e.target);
    });
    container.addEventListener('keydown', function(e) {
      if (e.key === 'Enter' || e.key === ' ') {
        if (e.target.closest('[data-filter-trigger]')) {
          e.preventDefault();
          activate(e.target);
        }
      }
    });
  }

  // 4. Sound Alert & Dynamic Tab Title / Favicon
  const prevStatuses = new Map();
  let soundEnabled = false;
  let audioCtx = null;
  let baseFaviconHref = null;

  function updateSoundButtonUI() {
    const btn = document.getElementById('sound-toggle-btn');
    if (!btn) return;
    const useEl = btn.querySelector('use');
    const title = soundEnabled
      ? dataset('soundActive', 'Alerta sonoro (Ativo)')
      : dataset('soundMuted', 'Alerta sonoro (Silenciado)');
    btn.classList.toggle('active', soundEnabled);
    btn.setAttribute('aria-pressed', soundEnabled ? 'true' : 'false');
    btn.setAttribute('title', title);
    if (useEl) useEl.setAttribute('href', soundEnabled ? '#icon-bell' : '#icon-bell-off');
  }

  function initSound() {
    soundEnabled = localStorage.getItem('argus-sound') === 'on';
    updateSoundButtonUI();
  }

  function toggleSound() {
    soundEnabled = !soundEnabled;
    try {
      localStorage.setItem('argus-sound', soundEnabled ? 'on' : 'off');
    } catch (_) {}
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
    } catch (_) {}
  }

  function playDownAlert() {
    if (!soundEnabled) return;
    playBeep(440, 0.14);
    setTimeout(function() { playBeep(330, 0.22); }, 160);
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

  function updateDynamicFavicon(downs) {
    const link = document.getElementById('favicon-link');
    if (!link) return;
    if (!baseFaviconHref) {
      baseFaviconHref = link.getAttribute('href');
    }
    if (downs <= 0) {
      if (baseFaviconHref) link.setAttribute('href', baseFaviconHref);
      return;
    }
    try {
      const canvas = document.createElement('canvas');
      canvas.width = 32;
      canvas.height = 32;
      const ctx = canvas.getContext('2d');
      ctx.fillStyle = '#0E1013';
      ctx.beginPath();
      ctx.arc(16, 16, 15, 0, Math.PI * 2);
      ctx.fill();
      ctx.fillStyle = '#F0524F';
      ctx.beginPath();
      ctx.arc(16, 16, 12, 0, Math.PI * 2);
      ctx.fill();
      ctx.fillStyle = '#FFFFFF';
      ctx.font = 'bold 16px monospace';
      ctx.textAlign = 'center';
      ctx.textBaseline = 'middle';
      ctx.fillText(downs > 9 ? '!' : String(downs), 16, 17);
      link.setAttribute('href', canvas.toDataURL('image/png'));
    } catch (_) {}
  }

  const santaState = {
    active: false,
    rafId: null,
    resizeHandler: null,
    canvas: null,
    stage: null
  };

  function createSantaSleighMarkup() {
    return (
      '<div class="santa-sleigh-runner">' +
        '<div class="santa-sleigh-bob">' +
          '<span class="santa-sleigh-trail" aria-hidden="true"></span>' +
          '<span class="santa-sleigh-bubble mono">HO HO HO!</span>' +
          '<img class="santa-sleigh-img" src="/static/seasonal/santa-sleigh.svg" alt="" aria-hidden="true">' +
        '</div>' +
      '</div>'
    );
  }

  function startSantaEasterEgg() {
    if (santaState.active || !document.body) return;
    if (window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches) return;
    santaState.active = true;

    const canvas = document.createElement('canvas');
    canvas.id = 'santa-snow-canvas';
    canvas.setAttribute('aria-hidden', 'true');
    document.body.appendChild(canvas);
    santaState.canvas = canvas;

    const stage = document.createElement('div');
    stage.id = 'santa-sleigh-stage';
    stage.setAttribute('aria-hidden', 'true');
    stage.innerHTML = createSantaSleighMarkup();
    document.body.appendChild(stage);
    santaState.stage = stage;

    const ctx = canvas.getContext('2d');
    if (!ctx) return;

    function resize() {
      canvas.width = window.innerWidth;
      canvas.height = window.innerHeight;
    }
    resize();
    santaState.resizeHandler = resize;
    window.addEventListener('resize', resize);

    const flakeImg = new Image();
    flakeImg.src = '/static/seasonal/snowflake.svg';

    const flakes = [];
    const flakeCount = 55;
    for (let i = 0; i < flakeCount; i++) {
      flakes.push({
        x: Math.random() * canvas.width,
        y: Math.random() * canvas.height,
        size: 10 + Math.random() * 12,
        speedY: 0.7 + Math.random() * 1.3,
        swayAmp: 0.3 + Math.random() * 0.7,
        swaySpeed: 0.01 + Math.random() * 0.02,
        phase: Math.random() * Math.PI * 2,
        rot: Math.random() * Math.PI * 2,
        rotSpeed: (Math.random() - 0.5) * 0.02,
        opacity: 0.35 + Math.random() * 0.5
      });
    }

    function drawFallbackFlake(x, y, r, opacity) {
      ctx.strokeStyle = 'rgba(255, 255, 255, ' + opacity.toFixed(2) + ')';
      ctx.lineWidth = 1.2;
      ctx.beginPath();
      for (let a = 0; a < 3; a++) {
        const ang = (a * Math.PI) / 3;
        const dx = Math.cos(ang) * r;
        const dy = Math.sin(ang) * r;
        ctx.moveTo(x - dx, y - dy);
        ctx.lineTo(x + dx, y + dy);
      }
      ctx.stroke();
    }

    function tick() {
      if (!santaState.active) return;
      ctx.clearRect(0, 0, canvas.width, canvas.height);

      for (let i = 0; i < flakes.length; i++) {
        const f = flakes[i];
        f.phase += f.swaySpeed;
        f.rot += f.rotSpeed;
        f.y += f.speedY;
        f.x += Math.sin(f.phase) * f.swayAmp;

        if (f.y > canvas.height + 16) {
          f.y = -16;
          f.x = Math.random() * canvas.width;
        }
        if (f.x > canvas.width + 16) f.x = -16;
        if (f.x < -16) f.x = canvas.width + 16;

        if (flakeImg.complete && flakeImg.naturalWidth > 0) {
          ctx.save();
          ctx.globalAlpha = f.opacity;
          ctx.translate(f.x, f.y);
          ctx.rotate(f.rot);
          ctx.filter = 'invert(1)';
          ctx.drawImage(flakeImg, -f.size / 2, -f.size / 2, f.size, f.size);
          ctx.restore();
        } else {
          drawFallbackFlake(f.x, f.y, f.size * 0.45, f.opacity);
        }
      }

      santaState.rafId = window.requestAnimationFrame(tick);
    }

    santaState.rafId = window.requestAnimationFrame(tick);
  }

  function stopSantaEasterEgg() {
    if (!santaState.active) return;
    santaState.active = false;

    if (santaState.rafId !== null) {
      window.cancelAnimationFrame(santaState.rafId);
      santaState.rafId = null;
    }
    if (santaState.resizeHandler) {
      window.removeEventListener('resize', santaState.resizeHandler);
      santaState.resizeHandler = null;
    }
    if (santaState.canvas) {
      santaState.canvas.remove();
      santaState.canvas = null;
    }
    if (santaState.stage) {
      santaState.stage.remove();
      santaState.stage = null;
    }
  }

  function updateSantaEasterEgg(downs, total) {
    const enabled = dataset('santaEasterEgg', 'false') === 'true';
    const shouldActivate = enabled && total > 0 && downs === total;
    if (shouldActivate) {
      startSantaEasterEgg();
    } else {
      stopSantaEasterEgg();
    }
  }

  function updateTabTitleFromStats() {
    const downCountEl = document.getElementById('stat-down-val');
    const totalCountEl = document.getElementById('stat-total-val');
    const liveDot = document.getElementById('live-dot');
    if (liveDot) liveDot.classList.remove('disconnected');

    if (downCountEl) {
      const downs = parseInt(downCountEl.textContent.trim(), 10) || 0;
      const total = totalCountEl ? (parseInt(totalCountEl.textContent.trim(), 10) || 0) : 0;
      if (downs > 0) {
        document.title = formatMsg(dataset('tabDownFmt', '(%d) Argus — Sistemas Fora'), downs);
      } else {
        document.title = dataset('tabOk', 'Argus — Operacional');
      }
      updateDynamicFavicon(downs);
      updateSantaEasterEgg(downs, total);
    }
  }

  // 5. Stale Card Detection
  function checkStaleCards() {
    const now = Date.now();
    document.querySelectorAll('.card[data-system-id]').forEach(function(card) {
      const lastMs = parseInt(card.getAttribute('data-last-check-ms') || '0', 10);
      const intervalMs = parseInt(card.getAttribute('data-interval-ms') || '30000', 10);
      if (lastMs > 0 && now - lastMs > Math.max(intervalMs * 3, 90000)) {
        markCardStale(card);
      }
    });
  }

  function markCardStale(card) {
    if (card && card.classList && card.classList.contains('card')) {
      card.classList.add('stale');
      let warning = card.querySelector('.card__stale-warning');
      if (!warning) {
        warning = document.createElement('div');
        warning.className = 'card__stale-warning';
        warning.textContent = dataset('noResponse', 'Sem resposta');
        card.appendChild(warning);
      }
    }
  }

  // 6. Batched Card Updates & Drag Guard
  let isDraggingCard = false;
  const pendingCardUpdates = new Map();
  let rafScheduled = false;

  function deduplicateCards(grid) {
    if (!grid) return;
    const seen = new Set();
    grid.querySelectorAll('.card[data-system-id]').forEach(function(card) {
      const id = card.getAttribute('data-system-id');
      if (!id) return;
      if (seen.has(id)) {
        card.remove();
      } else {
        seen.add(id);
      }
    });
  }

  function applyCardHTMLImmediate(id, html) {
    const grid = document.getElementById('cards-grid');
    if (!grid || !id || !html) return;

    const matches = [];
    grid.querySelectorAll('.card[data-system-id]').forEach(function(card) {
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

    nextCard.classList.add('card--pulsed');
    existing.replaceWith(nextCard);
    checkTransitionAndNotify(nextCard);
  }

  function flushPendingCardUpdates() {
    rafScheduled = false;
    if (isDraggingCard || pendingCardUpdates.size === 0) return;
    const entries = Array.from(pendingCardUpdates.entries());
    pendingCardUpdates.clear();
    entries.forEach(function(entry) {
      applyCardHTMLImmediate(entry[0], entry[1]);
    });
    applyCurrentSort();
    applyFilters();
  }

  function queueCardUpdate(id, html) {
    if (!id || !html) return;
    pendingCardUpdates.set(id, html);
    if (!isDraggingCard && !rafScheduled) {
      rafScheduled = true;
      window.requestAnimationFrame(flushPendingCardUpdates);
    }
  }

  // 7. SSE Live Updates + Single-Request Fallback Polling + History Live Refresh
  let fallbackTimer = null;
  let waitingManualCheck = false;

  async function pollCardsSnapshot() {
    if (isDraggingCard) return;
    try {
      const resp = await fetch('/api/cards', { cache: 'no-store' });
      if (!resp.ok) return;
      const data = await resp.json();
      if (data.stats_html) {
        const statsEl = document.getElementById('stats-container');
        if (statsEl) {
          statsEl.innerHTML = data.stats_html;
          updateTabTitleFromStats();
        }
      }
      if (Array.isArray(data.cards)) {
        data.cards.forEach(function(c) {
          if (c && c.id && c.html) {
            queueCardUpdate(c.id, c.html);
          }
        });
      }
    } catch (_) {
      const liveDot = document.getElementById('live-dot');
      if (liveDot) liveDot.classList.add('disconnected');
    }
  }

  function startFallbackPolling() {
    if (fallbackTimer) return;
    fallbackTimer = setInterval(pollCardsSnapshot, 15000);
  }

  function stopFallbackPolling() {
    if (fallbackTimer) {
      clearInterval(fallbackTimer);
      fallbackTimer = null;
    }
  }

  async function refreshHistoryPageInPlace(notifyDone) {
    const histPage = document.getElementById('history-page');
    if (!histPage) return;
    try {
      const resp = await fetch(window.location.href, { cache: 'no-store' });
      if (!resp.ok) return;
      const html = await resp.text();
      const doc = new DOMParser().parseFromString(html, 'text/html');
      const next = doc.getElementById('history-page');
      if (next) {
        histPage.innerHTML = next.innerHTML;
        initHistoryActions();
        if (notifyDone) {
          showToast(dataset('toastCheckDone', 'Verificação concluída'), 'success');
        }
      }
    } catch (_) {}
  }

  function initSSE() {
    const hasGrid = Boolean(document.getElementById('cards-grid'));
    const histPage = document.getElementById('history-page');
    const histSystemId = histPage ? histPage.getAttribute('data-system-id') : null;

    if (!hasGrid && !histSystemId) return;

    if (typeof EventSource === 'undefined') {
      if (hasGrid) startFallbackPolling();
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
        if (!payload || !payload.id) return;
        if (hasGrid && payload.html) {
          queueCardUpdate(payload.id, payload.html);
        }
        if (histSystemId && payload.id === histSystemId) {
          const notify = waitingManualCheck;
          waitingManualCheck = false;
          refreshHistoryPageInPlace(notify);
        }
      } catch (_) {}
    });

    es.addEventListener('stats', function(e) {
      if (!hasGrid) return;
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
      if (hasGrid) startFallbackPolling();
    };
  }

  // 8. Keyboard Shortcuts & Help Modal
  let lastFocusedBeforeModal = null;

  function toggleShortcutsModal(forceOpen) {
    const modal = document.getElementById('shortcuts-modal');
    if (!modal) return;
    const nextHidden = typeof forceOpen === 'boolean' ? !forceOpen : !modal.hidden;
    if (!nextHidden && modal.hidden) {
      lastFocusedBeforeModal = document.activeElement;
      modal.hidden = false;
      const closeBtn = document.getElementById('shortcuts-close-btn');
      if (closeBtn) closeBtn.focus();
    } else if (nextHidden && !modal.hidden) {
      modal.hidden = true;
      if (lastFocusedBeforeModal && typeof lastFocusedBeforeModal.focus === 'function') {
        lastFocusedBeforeModal.focus();
      }
      lastFocusedBeforeModal = null;
    }
  }

  window.addEventListener('keydown', function(e) {
    if (e.ctrlKey || e.metaKey || e.altKey) {
      return;
    }
    if (e.target.tagName === 'INPUT' || e.target.tagName === 'TEXTAREA' || e.target.tagName === 'SELECT' || e.target.isContentEditable) {
      if (e.key === 'Escape') {
        if (e.target.id === 'search-input') {
          e.target.value = '';
          searchQuery = '';
          applyFilters();
          syncFiltersToURL();
        }
        e.target.blur();
      }
      return;
    }
    if (e.key === 'Escape') {
      const modal = document.getElementById('shortcuts-modal');
      if (modal && !modal.hidden) {
        toggleShortcutsModal(false);
        return;
      }
      if (document.documentElement.classList.contains('tv-mode') && document.getElementById('tv-toggle-btn')) {
        toggleTVMode();
      }
      return;
    }
    if (e.key === '?') {
      e.preventDefault();
      toggleShortcutsModal();
    } else if (e.key === '/') {
      const input = document.getElementById('search-input');
      if (input) {
        e.preventDefault();
        input.focus();
      }
    } else if (e.key === 't' || e.key === 'T') {
      toggleTheme();
    } else if (e.key === 'v' || e.key === 'V') {
      if (document.getElementById('tv-toggle-btn')) {
        toggleTVMode();
      }
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
    document.querySelectorAll('.card[data-system-id]').forEach(function(card) {
      const id = card.getAttribute('data-system-id');
      const status = (card.getAttribute('data-status') || '').toLowerCase();
      if (id && status) {
        prevStatuses.set(id, status);
      }
    });
  }

  // 9. Client-side Card Sorting & Drag & Drop Reordering
  const CARD_ORDER_KEY = 'argus-card-order';
  const SORT_MODE_KEY = 'argus-sort';
  const SEVERITY_WEIGHT = { down: 3, degraded: 2, unknown: 1, up: 0 };
  let sortMode = 'manual';

  function updateSortButtonUI() {
    const btn = document.getElementById('sort-toggle-btn');
    if (!btn) return;
    const isSeverity = sortMode === 'severity';
    btn.classList.toggle('active', isSeverity);
    btn.setAttribute('aria-pressed', isSeverity ? 'true' : 'false');
    btn.setAttribute(
      'title',
      isSeverity
        ? dataset('sortActive', 'Ordem por prioridade ativa')
        : dataset('sortPriority', 'Ordenar por prioridade (críticos no topo)')
    );
  }

  function sortCardsBySeverity(grid) {
    if (!grid) return;
    deduplicateCards(grid);
    const cards = Array.from(grid.querySelectorAll('.card[data-system-id]'));
    cards.sort(function(a, b) {
      const sa = SEVERITY_WEIGHT[(a.getAttribute('data-status') || '').toLowerCase()] || 0;
      const sb = SEVERITY_WEIGHT[(b.getAttribute('data-status') || '').toLowerCase()] || 0;
      if (sa !== sb) return sb - sa;
      const na = (a.getAttribute('data-name') || '').toLowerCase();
      const nb = (b.getAttribute('data-name') || '').toLowerCase();
      return na.localeCompare(nb);
    });
    const frag = document.createDocumentFragment();
    cards.forEach(function(c) { frag.appendChild(c); });
    grid.appendChild(frag);
  }

  function sortCardsAlphabetically(grid) {
    if (!grid) return;
    deduplicateCards(grid);
    const cards = Array.from(grid.querySelectorAll('.card[data-system-id]'));
    cards.sort(function(a, b) {
      const na = (a.getAttribute('data-name') || '').toLowerCase();
      const nb = (b.getAttribute('data-name') || '').toLowerCase();
      if (na !== nb) return na.localeCompare(nb);
      return (a.getAttribute('data-system-id') || '').localeCompare(b.getAttribute('data-system-id') || '');
    });
    const frag = document.createDocumentFragment();
    cards.forEach(function(c) { frag.appendChild(c); });
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
    grid.querySelectorAll('.card[data-system-id]').forEach(function(card) {
      byId.set(card.getAttribute('data-system-id'), card);
    });

    const frag = document.createDocumentFragment();
    savedOrder.forEach(function(id) {
      const card = byId.get(id);
      if (card) {
        frag.appendChild(card);
        byId.delete(id);
      }
    });
    byId.forEach(function(card) { frag.appendChild(card); });
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
      .map(function(card) { return card.getAttribute('data-system-id'); })
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
      grid.querySelectorAll('.card.dragging').forEach(function(c) { c.classList.remove('dragging'); });
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

    document.addEventListener('dragend', cleanupDrag);

    grid.addEventListener('click', function(e) {
      if (Date.now() < suppressClickUntil) {
        e.preventDefault();
      }
    });
  }

  // 10. History Page Actions (Maintenance, Check Now, Failed Checks Pagination)
  function initHistoryActions() {
    const maintBtn = document.getElementById('maintenance-toggle-btn');
    if (maintBtn && !maintBtn.dataset.bound) {
      maintBtn.dataset.bound = '1';
      maintBtn.addEventListener('click', async function() {
        const id = maintBtn.getAttribute('data-system-id');
        if (!id || maintBtn.disabled) return;
        maintBtn.disabled = true;
        const durSelect = document.getElementById('maintenance-duration');
        const durVal = durSelect ? durSelect.value : '';
        let url = '/api/systems/' + encodeURIComponent(id) + '/maintenance';
        if (durVal) {
          url += '?duration=' + encodeURIComponent(durVal);
        }
        try {
          const resp = await fetch(url, { method: 'POST' });
          if (resp.ok) {
            const data = await resp.json();
            const active = Boolean(data.in_maintenance);
            maintBtn.classList.toggle('active', active);
            maintBtn.setAttribute('data-maintenance', active ? 'true' : 'false');
            maintBtn.textContent = active
              ? dataset('maintDisable', 'Desativar Manutenção')
              : dataset('maintEnable', 'Modo Manutenção');
            showToast(
              active
                ? dataset('toastMaintOn', 'Modo manutenção ativado')
                : dataset('toastMaintOff', 'Modo manutenção desativado'),
              'success'
            );
          } else {
            showToast('Erro HTTP ' + resp.status, 'error');
          }
        } catch (_) {
          showToast('Erro de rede', 'error');
        } finally {
          maintBtn.disabled = false;
        }
      });
    }

    const checkBtn = document.getElementById('check-now-btn');
    if (checkBtn && !checkBtn.dataset.bound) {
      checkBtn.dataset.bound = '1';
      checkBtn.addEventListener('click', async function() {
        const id = checkBtn.getAttribute('data-system-id');
        if (!id || checkBtn.disabled) return;
        checkBtn.disabled = true;
        const originalText = checkBtn.textContent;
        checkBtn.textContent = dataset('checking', 'Verificando...');
        try {
          const resp = await fetch('/api/systems/' + encodeURIComponent(id) + '/check', {
            method: 'POST'
          });
          if (resp.status === 429) {
            showToast(dataset('toastRateLimit', 'Aguarde alguns segundos antes de tentar novamente'), 'error');
          } else if (resp.ok) {
            checkBtn.textContent = dataset('scheduled', 'Agendado!');
            waitingManualCheck = true;
            setTimeout(function() {
              if (waitingManualCheck) {
                waitingManualCheck = false;
                refreshHistoryPageInPlace(true).finally(function() {
                  const curBtn = document.getElementById('check-now-btn');
                  if (curBtn) {
                    curBtn.textContent = originalText;
                    curBtn.disabled = false;
                  }
                });
              }
            }, 1800);
            return;
          } else {
            showToast('Erro HTTP ' + resp.status, 'error');
          }
        } catch (_) {
          showToast('Erro de rede', 'error');
        }
        setTimeout(function() {
          checkBtn.textContent = originalText;
          checkBtn.disabled = false;
        }, 1200);
      });
    }

    const loadMoreBtn = document.getElementById('load-more-failed-btn');
    if (loadMoreBtn && !loadMoreBtn.dataset.bound) {
      loadMoreBtn.dataset.bound = '1';
      loadMoreBtn.addEventListener('click', async function() {
        const id = loadMoreBtn.getAttribute('data-system-id');
        const period = loadMoreBtn.getAttribute('data-period') || '24h';
        const offset = parseInt(loadMoreBtn.getAttribute('data-offset') || '50', 10);
        if (!id || loadMoreBtn.disabled) return;
        loadMoreBtn.disabled = true;
        try {
          const resp = await fetch(
            '/api/systems/' + encodeURIComponent(id) + '/failed-checks?period=' + encodeURIComponent(period) + '&offset=' + offset
          );
          if (resp.ok) {
            const data = await resp.json();
            const tbody = document.getElementById('failed-checks-tbody');
            if (tbody && Array.isArray(data.items)) {
              data.items.forEach(function(f) {
                const tr = document.createElement('tr');
                const codeOrLat = f.StatusCode ? String(f.StatusCode) : (f.LatencyMs ? f.LatencyMs + ' ms' : '—');
                tr.innerHTML =
                  '<td class="mono text-xs"></td>' +
                  '<td><span class="check-row__badge"></span></td>' +
                  '<td><span class="mono text-down"></span></td>' +
                  '<td class="mono"></td>' +
                  '<td class="text-muted"></td>';
                const tds = tr.querySelectorAll('td');
                tds[0].textContent = f.CheckedAtFormatted || '';
                tds[1].firstChild.textContent = f.Kind || '';
                tds[2].firstChild.textContent = f.ErrorCat || '';
                tds[3].textContent = codeOrLat;
                tds[4].textContent = f.ErrorMsg || '';
                tbody.appendChild(tr);
              });
            }
            if (data.has_more) {
              loadMoreBtn.setAttribute('data-offset', String(offset + 50));
              loadMoreBtn.disabled = false;
            } else {
              loadMoreBtn.parentElement.remove();
            }
            return;
          }
        } catch (_) {}
        loadMoreBtn.disabled = false;
      });
    }
  }

  function initPublicStatusAutoRefresh() {
    const statusPage = document.getElementById('status-page');
    if (!statusPage) return;

    setInterval(async function() {
      try {
        const resp = await fetch(window.location.href, { cache: 'no-store' });
        if (!resp.ok) return;
        const html = await resp.text();
        const doc = new DOMParser().parseFromString(html, 'text/html');
        const next = doc.getElementById('status-page');
        const cur = document.getElementById('status-page');
        if (next && cur) {
          cur.innerHTML = next.innerHTML;
        }
        const liveDot = document.getElementById('live-dot');
        if (liveDot) liveDot.classList.remove('disconnected');
      } catch (_) {
        const liveDot = document.getElementById('live-dot');
        if (liveDot) liveDot.classList.add('disconnected');
      }
    }, 15000);
  }

  function initTestAlertButton() {
    const btn = document.getElementById('test-alert-btn');
    if (!btn) return;

    btn.addEventListener('click', async function() {
      if (btn.disabled) return;
      btn.disabled = true;

      try {
        const resp = await fetch('/api/alerts/test', { method: 'POST' });
        const data = await resp.json().catch(function() { return {}; });
        if (resp.ok && data.ok) {
          btn.classList.add('active');
          showToast(formatMsg(dataset('toastTestSent', 'Teste enviado (%d canal(is))'), data.channels || 1), 'success');
        } else {
          showToast(data.error || dataset('toastTestFailed', 'Falha ao enviar teste'), 'error');
        }
      } catch (_) {
        showToast(dataset('toastTestFailed', 'Falha ao enviar teste'), 'error');
      }

      setTimeout(function() {
        btn.classList.remove('active');
        btn.disabled = false;
      }, 2000);
    });
  }

  function initAutoHideScrollbar() {
    const scrollTimers = new WeakMap();

    function markScrolling(target) {
      if (!target || !target.classList) return;
      target.classList.add('is-scrolling');
      const prevTimer = scrollTimers.get(target);
      if (prevTimer) clearTimeout(prevTimer);
      scrollTimers.set(target, setTimeout(function() {
        target.classList.remove('is-scrolling');
      }, 900));
    }

    window.addEventListener('scroll', function() {
      markScrolling(document.documentElement);
    }, { passive: true });

    document.addEventListener('scroll', function(e) {
      if (e.target && e.target !== document && e.target !== document.documentElement) {
        markScrolling(e.target);
      }
    }, { capture: true, passive: true });
  }

  window.addEventListener('DOMContentLoaded', function() {
    const initialTheme = document.documentElement.getAttribute('data-theme') || 'dark';
    syncThemeUI(initialTheme);
    renderSeasonalParticles(initialTheme);
    initAutoHideScrollbar();
    initTVMode();
    initViewMode();
    initSound();
    seedInitialStatuses();
    initSortMode();
    initCardDragAndDrop();
    initFiltersFromURL();
    bindStatsBarFilterClicks();
    initHistoryActions();
    initPublicStatusAutoRefresh();
    initTestAlertButton();
    updateTabTitleFromStats();
    initSSE();
    setInterval(checkStaleCards, 10000);

    initThemePicker();

    const sortBtn = document.getElementById('sort-toggle-btn');
    if (sortBtn) sortBtn.addEventListener('click', toggleSortMode);

    const viewBtn = document.getElementById('view-toggle-btn');
    if (viewBtn) viewBtn.addEventListener('click', toggleViewMode);

    const soundBtn = document.getElementById('sound-toggle-btn');
    if (soundBtn) soundBtn.addEventListener('click', toggleSound);

    const tvBtn = document.getElementById('tv-toggle-btn');
    if (tvBtn) tvBtn.addEventListener('click', toggleTVMode);

    const tvExitBtn = document.getElementById('tv-exit-btn');
    if (tvExitBtn) tvExitBtn.addEventListener('click', toggleTVMode);

    const langBtn = document.getElementById('lang-toggle-btn');
    if (langBtn) langBtn.addEventListener('click', toggleLanguage);

    const shortcutsBtn = document.getElementById('shortcuts-btn');
    if (shortcutsBtn) shortcutsBtn.addEventListener('click', function() { toggleShortcutsModal(true); });

    const shortcutsCloseBtn = document.getElementById('shortcuts-close-btn');
    if (shortcutsCloseBtn) shortcutsCloseBtn.addEventListener('click', function() { toggleShortcutsModal(false); });

    const shortcutsModal = document.getElementById('shortcuts-modal');
    if (shortcutsModal) {
      shortcutsModal.addEventListener('click', function(e) {
        if (e.target === shortcutsModal) toggleShortcutsModal(false);
      });
    }

    const clearFiltersBtn = document.getElementById('clear-filters-btn');
    if (clearFiltersBtn) clearFiltersBtn.addEventListener('click', clearAllFilters);

    const groupSelect = document.getElementById('group-filter');
    if (groupSelect) {
      groupSelect.addEventListener('change', function(e) {
        activeGroup = (e.target.value || 'all').toLowerCase();
        applyFilters();
        syncFiltersToURL();
      });
    }

    const searchInput = document.getElementById('search-input');
    if (searchInput) {
      searchInput.addEventListener('input', function(e) {
        searchQuery = e.target.value.trim().toLowerCase();
        applyFilters();
        syncFiltersToURL();
      });
    }

    document.querySelectorAll('.chip').forEach(function(btn) {
      btn.addEventListener('click', function() {
        setFilter(this.getAttribute('data-filter') || 'all');
      });
    });
  });
})();

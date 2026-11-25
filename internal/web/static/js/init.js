(function() {
  var SEASONAL = { carnival: 1, easter: 1, junina: 1, brazil: 1, spring: 1, halloween: 1, christmas: 1, newyear: 1 };

  function easterSunday(year) {
    var a = year % 19;
    var b = Math.floor(year / 100);
    var c = year % 100;
    var d = Math.floor(b / 4);
    var e = b % 4;
    var f = Math.floor((b + 8) / 25);
    var g = Math.floor((b - f + 1) / 3);
    var h = (19 * a + b - d - g + 15) % 30;
    var i = Math.floor(c / 4);
    var k = c % 4;
    var l = (32 + 2 * e + 2 * i - h - k) % 7;
    var m = Math.floor((a + 11 * h + 22 * l) / 451);
    var month = Math.floor((h + l - 7 * m + 114) / 31);
    var day = ((h + l - 7 * m + 114) % 31) + 1;
    return new Date(year, month - 1, day);
  }

  function resolveSeasonalTheme(now) {
    var m = now.getMonth() + 1;
    var d = now.getDate();
    if ((m === 12 && d >= 30) || (m === 1 && d <= 5)) return 'newyear';
    if (m === 12 && d <= 29) return 'christmas';
    if (m === 6 && d >= 12) return 'junina';
    if (m === 9 && d >= 1 && d <= 9) return 'brazil';
    if (m === 9 && d >= 21 && d <= 30) return 'spring';
    if ((m === 10 && d >= 25) || (m === 11 && d <= 2)) return 'halloween';

    var easter = easterSunday(now.getFullYear());
    var carnivalTue = new Date(easter.getFullYear(), easter.getMonth(), easter.getDate() - 47);
    var cStart = new Date(carnivalTue.getFullYear(), carnivalTue.getMonth(), carnivalTue.getDate() - 4);
    var cEnd = new Date(carnivalTue.getFullYear(), carnivalTue.getMonth(), carnivalTue.getDate() + 1, 23, 59, 59);
    if (now >= cStart && now <= cEnd) return 'carnival';

    var eStart = new Date(easter.getFullYear(), easter.getMonth(), easter.getDate() - 3);
    var eEnd = new Date(easter.getFullYear(), easter.getMonth(), easter.getDate(), 23, 59, 59);
    if (now >= eStart && now <= eEnd) return 'easter';
    return '';
  }

  function resolveTheme(choice) {
    var seasonalEnabled = document.documentElement.getAttribute('data-seasonal-themes') !== 'false';
    if (choice && SEASONAL[choice]) {
      choice = 'auto';
    }
    if (!choice || choice === 'auto') {
      if (seasonalEnabled) {
        var seasonal = resolveSeasonalTheme(new Date());
        if (seasonal) return seasonal;
      }
      var prefersDark = window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches;
      return prefersDark ? 'dark' : 'light';
    }
    return choice;
  }

  window.__argusResolveTheme = resolveTheme;

  var savedTheme = localStorage.getItem('argus-theme');
  if (savedTheme && SEASONAL[savedTheme]) {
    try { localStorage.removeItem('argus-theme'); } catch (_) {}
    savedTheme = null;
  }
  document.documentElement.setAttribute('data-theme', resolveTheme(savedTheme));
  var params = new URLSearchParams(window.location.search);
  var validSizes = { md: 1, lg: 1, xl: 1, xxl: 1 };
  var urlSize = (params.get('size') || '').toLowerCase();
  var cardSize = validSizes[urlSize] ? urlSize : '';
  if (!cardSize) {
    try {
      var savedSize = (localStorage.getItem('argus-card-size') || '').toLowerCase();
      if (validSizes[savedSize]) cardSize = savedSize;
    } catch (_) {}
  } else {
    try {
      if (cardSize === 'md') localStorage.removeItem('argus-card-size');
      else localStorage.setItem('argus-card-size', cardSize);
    } catch (_) {}
  }
  if (cardSize && cardSize !== 'md') {
    document.documentElement.setAttribute('data-card-size', cardSize);
  }
  if (window.location.pathname === '/' && params.get('view') === 'tv') {
    document.documentElement.classList.add('tv-mode');
  }
  if (localStorage.getItem('argus-view') === 'compact') {
    document.documentElement.classList.add('view-compact');
  }
})();

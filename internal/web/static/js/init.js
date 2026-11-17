(function() {
  var savedTheme = localStorage.getItem('argus-theme');
  var prefersDark = window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches;
  document.documentElement.setAttribute('data-theme', savedTheme || (prefersDark ? 'dark' : 'light'));
  if (new URLSearchParams(window.location.search).get('view') === 'tv') {
    document.documentElement.classList.add('tv-mode');
  }
  if (localStorage.getItem('argus-view') === 'compact') {
    document.documentElement.classList.add('view-compact');
  }
})();

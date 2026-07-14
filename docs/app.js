// OpenSave website — small progressive enhancements. No trackers, no analytics.

(function () {
  'use strict';

  // Mobile nav toggle
  var toggle = document.getElementById('nav-toggle');
  var links = document.getElementById('nav-links');
  if (toggle && links) {
    toggle.addEventListener('click', function () {
      links.classList.toggle('open');
    });
    links.addEventListener('click', function (e) {
      if (e.target.tagName === 'A') links.classList.remove('open');
    });
  }

  // Scroll-reveal for cards and sections
  var revealables = document.querySelectorAll(
    '.feature-card, .why-card, .how-step, .dl-card, .stat, .arch-panel, .oss-panel, .faq-item'
  );
  revealables.forEach(function (el) { el.classList.add('reveal'); });

  if ('IntersectionObserver' in window) {
    var io = new IntersectionObserver(
      function (entries) {
        entries.forEach(function (entry) {
          if (entry.isIntersecting) {
            entry.target.classList.add('visible');
            io.unobserve(entry.target);
          }
        });
      },
      { threshold: 0.12, rootMargin: '0px 0px -40px 0px' }
    );
    revealables.forEach(function (el) { io.observe(el); });
  } else {
    revealables.forEach(function (el) { el.classList.add('visible'); });
  }

  // Point Windows/Linux download buttons at the exact latest release assets
  // (best-effort; falls back to the releases page if the API is unavailable).
  var API = 'https://api.github.com/repos/sivadaboi/OpenSave/releases/latest';
  fetch(API)
    .then(function (r) { return r.ok ? r.json() : null; })
    .then(function (rel) {
      if (!rel || !rel.assets) return;
      var find = function (test) {
        var a = rel.assets.filter(function (x) { return test(x.name.toLowerCase()); })[0];
        return a && a.browser_download_url;
      };
      var winUrl = find(function (n) { return n.indexOf('setup') !== -1 && n.slice(-4) === '.exe'; }) ||
                   find(function (n) { return n.slice(-4) === '.exe'; });
      var linuxUrl = find(function (n) { return n.indexOf('linux') !== -1 && n.indexOf('.tar') !== -1; });

      document.querySelectorAll('a.btn-primary').forEach(function (a) {
        var t = (a.textContent || '').toLowerCase();
        if (winUrl && t.indexOf('windows') !== -1) a.href = winUrl;
        if (linuxUrl && t.indexOf('linux') !== -1) a.href = linuxUrl;
      });

      // Show the live version in the hero eyebrow — but only once a 2.x
      // (Go rewrite) release exists; older tags belong to the JS app.
      var major = parseInt(String(rel.tag_name || '').replace(/^v/, ''), 10);
      var eyebrow = document.querySelector('.hero-eyebrow');
      if (eyebrow && rel.tag_name && major >= 2) {
        eyebrow.childNodes.forEach(function (n) {
          if (n.nodeType === 3 && n.textContent.indexOf('v2.0') !== -1) {
            n.textContent = n.textContent.replace('v2.0', rel.tag_name);
          }
        });
      }
    })
    .catch(function () { /* releases page fallback already in place */ });
})();

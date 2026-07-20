// OpenSave website — small progressive enhancements. No trackers, no analytics.

(function () {
  'use strict';

  // ---- Mobile nav toggle ----
  var toggle = document.getElementById('nav-toggle');
  var links = document.getElementById('nav-links');
  if (toggle && links) {
    toggle.addEventListener('click', function () { links.classList.toggle('open'); });
    links.addEventListener('click', function (e) {
      if (e.target.tagName === 'A') links.classList.remove('open');
    });
  }

  // ---- Scroll-reveal ----
  var revealables = document.querySelectorAll(
    '.feature-card, .why-card, .how-step, .dl-card, .stat, .arch-panel, .oss-panel, .faq-item, .shot-card'
  );
  revealables.forEach(function (el) { el.classList.add('reveal'); });
  if ('IntersectionObserver' in window) {
    var io = new IntersectionObserver(
      function (entries) {
        entries.forEach(function (entry) {
          if (entry.isIntersecting) { entry.target.classList.add('visible'); io.unobserve(entry.target); }
        });
      },
      { threshold: 0.1, rootMargin: '0px 0px -40px 0px' }
    );
    revealables.forEach(function (el) { io.observe(el); });
  } else {
    revealables.forEach(function (el) { el.classList.add('visible'); });
  }

  // ---- Screenshot lightbox ----
  var lb = document.getElementById('lightbox');
  var lbImg = document.getElementById('lightbox-img');
  var lbClose = document.getElementById('lightbox-close');
  if (lb && lbImg) {
    document.querySelectorAll('[data-zoom] img').forEach(function (img) {
      img.addEventListener('click', function () {
        lbImg.src = img.currentSrc || img.src;
        lbImg.alt = img.alt || '';
        lb.classList.add('open');
        lb.setAttribute('aria-hidden', 'false');
      });
    });
    var close = function () {
      lb.classList.remove('open');
      lb.setAttribute('aria-hidden', 'true');
      lbImg.src = '';
    };
    lb.addEventListener('click', function (e) { if (e.target === lb || e.target === lbImg) close(); });
    if (lbClose) lbClose.addEventListener('click', close);
    document.addEventListener('keydown', function (e) { if (e.key === 'Escape') close(); });
  }

  // ---- Point download buttons at the exact latest release assets ----
  // Best-effort; falls back to the releases page (already the href) on failure.
  var API = 'https://api.github.com/repos/sivadaboi/OpenSave/releases/latest';
  fetch(API)
    .then(function (r) { return r.ok ? r.json() : null; })
    .then(function (rel) {
      if (!rel || !rel.assets) return;
      var find = function (test) {
        var a = rel.assets.filter(function (x) { return test(x.name.toLowerCase()); })[0];
        return a && a.browser_download_url;
      };
      var isExe = function (n) { return n.slice(-4) === '.exe' && n.indexOf('cli') === -1 && n.indexOf('relay') === -1; };
      // Windows installer (NSIS) vs portable exe.
      var winInstaller = find(function (n) { return (n.indexOf('setup') !== -1 || n.indexOf('installer') !== -1) && isExe(n); });
      var winPortable = find(function (n) { return isExe(n) && n.indexOf('setup') === -1 && n.indexOf('installer') === -1; });
      // Linux: the app tarball (not the bare cli/relay binaries).
      var linuxUrl =
        find(function (n) { return n.indexOf('linux') !== -1 && n.indexOf('.tar') !== -1; }) ||
        find(function (n) { return n.indexOf('.tar.gz') !== -1; });

      var map = {
        'windows': winInstaller || winPortable,   // generic CTAs -> installer
        'windows-installer': winInstaller,
        'windows-portable': winPortable,
        'linux': linuxUrl
      };
      document.querySelectorAll('a[data-dl]').forEach(function (a) {
        var url = map[a.getAttribute('data-dl')];
        if (url) a.href = url;
      });

      // Show the live version in the hero eyebrow — only for 2.x+ (Go) tags.
      var tag = String(rel.tag_name || '');
      var major = parseInt(tag.replace(/^v/, ''), 10);
      var eyebrow = document.querySelector('.hero-eyebrow');
      if (eyebrow && tag && major >= 2) {
        eyebrow.childNodes.forEach(function (n) {
          if (n.nodeType === 3 && /v2\.\d/.test(n.textContent)) {
            n.textContent = n.textContent.replace(/v2\.\d[\w.-]*/, tag);
          }
        });
      }
    })
    .catch(function () { /* releases-page fallback already in the href */ });
})();

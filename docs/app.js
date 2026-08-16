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
    '.feature-card, .why-card, .how-step, .dl-card, .stat, .arch-panel, .oss-panel, .faq-item, .shot-card, .cluster, '
    // Card grids on the CLI page. Deliberately not .guide-section:
    // revealing prose gates the text a reader came for behind an
    // animation, and hides the whole document if it ever fails.
    + '.cli-fact, .cli-ref, .docs-card'
  );
  revealables.forEach(function (el) { el.classList.add('reveal'); });

  // Stagger siblings so a grid lands in sequence rather than as one slab.
  // Index resets per parent, and is capped so a long list never leaves the
  // last item waiting a second and a half to appear.
  (function stagger() {
    var seen = {};
    var key = 0;
    revealables.forEach(function (el) {
      var p = el.parentNode;
      if (!p.__osKey) { p.__osKey = ++key; seen[p.__osKey] = 0; }
      var i = seen[p.__osKey]++;
      el.style.setProperty('--i', Math.min(i, 5));
    });
  })();
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

  // Safety net. IntersectionObserver can be present and still never deliver
  // a callback — seen in embedded webviews and some privacy browsers. The
  // 'IntersectionObserver' in window check above does not catch that, and
  // because .reveal starts at opacity 0 the failure hides the page's content
  // permanently rather than merely skipping an animation. If nothing has been
  // revealed shortly after load, assume the observer is not coming and show
  // everything.
  setTimeout(function () {
    if (!document.querySelector('.reveal.visible')) {
      revealables.forEach(function (el) { el.classList.add('visible'); });
    }
  }, 1500);

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
      // removeAttribute, not src = '': an empty src resolves against the
      // document and fetches the page again.
      lbImg.removeAttribute('src');
    };
    lb.addEventListener('click', function (e) { if (e.target === lb || e.target === lbImg) close(); });
    if (lbClose) lbClose.addEventListener('click', close);
    document.addEventListener('keydown', function (e) { if (e.key === 'Escape') close(); });
  }

  // ---- Point download buttons at the exact latest release assets ----
  // Best-effort; falls back to the releases page (already the href) on failure.
  var API = 'https://api.github.com/repos/Liquid-co/OpenSave/releases/latest';
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
      // Steam Deck: the Flatpak, and only the Flatpak. Without this the Deck
      // button was the one download that dropped you on the raw asset list,
      // where "opensave-linux-amd64.tar.gz" reads like the Steam Deck build
      // and isn't — stock SteamOS ships no WebKitGTK, so the tarball's app
      // won't start. People picked it and reported OpenSave as broken.
      var flatpakUrl = find(function (n) { return n.slice(-8) === '.flatpak'; });

      var map = {
        'windows': winInstaller || winPortable,   // generic CTAs -> installer
        'windows-installer': winInstaller,
        'windows-portable': winPortable,
        'linux': linuxUrl,
        'flatpak': flatpakUrl
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

  // ---------------------------------------------------------------
  // Everything below is progressive enhancement: the page is complete
  // and usable with none of it running.
  // ---------------------------------------------------------------

  var reduced = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;

  // ---- Live relay status -----------------------------------------
  // The relay serves /health with Access-Control-Allow-Origin: *, so this
  // is a plain cross-origin GET. After two outages, a status that is
  // actually measured beats a claim that it is up.
  (function relayStatus() {
    var box = document.getElementById('relay-live');
    if (!box) return;

    var set = function (state, label) {
      box.setAttribute('data-state', state);
      box.querySelector('.relay-label').textContent = label;
    };
    var plural = function (n, one, many) { return n + ' ' + (n === 1 ? one : many); };
    var uptime = function (sec) {
      var d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600);
      if (d) return plural(d, 'day', 'days') + (h ? ' ' + h + 'h' : '');
      var m = Math.floor((sec % 3600) / 60);
      return h ? h + 'h ' + m + 'm' : plural(m, 'minute', 'minutes');
    };

    var ctrl = typeof AbortController === 'function' ? new AbortController() : null;
    var timer = setTimeout(function () { ctrl && ctrl.abort(); }, 6000);

    fetch('https://relay.opensave.org/health', ctrl ? { signal: ctrl.signal } : undefined)
      .then(function (r) { return r.ok ? r.json() : Promise.reject(); })
      .then(function (h) {
        clearTimeout(timer);
        if (!h || h.status !== 'ok') return Promise.reject();
        set('up', 'Relay online');
        var stats = box.querySelector('.relay-stats');
        var put = function (k, v) {
          var el = stats.querySelector('[data-k="' + k + '"]');
          if (el) el.textContent = v;
        };
        // Concurrency figures (clients, rooms) are deliberately not shown:
        // they measure this instant, and on a young project a quiet moment
        // reads as nobody using it. These three only move in one direction.
        put('uptime', h.uptime != null ? uptime(h.uptime) : '—');
        var msgs = h.totalMessages != null ? h.totalMessages : 0;
        var msgEl = stats.querySelector('[data-k="messages"]');
        if (msgEl && window.__osCountUp) window.__osCountUp(msgEl, msgs);
        else put('messages', msgs.toLocaleString());
        put('dropped', (h.droppedMessages != null ? h.droppedMessages : 0).toLocaleString());
        stats.hidden = false;
      })
      .catch(function () {
        clearTimeout(timer);
        // Unreachable from here is not the same as down — a blocked
        // request or an offline visitor looks identical from the browser.
        set('unknown', 'Relay status unavailable');
      });
  })();

  // ---- The sync diagram, animated --------------------------------
  // Watch -> snapshot -> delta -> travel -> arrive. The static diagram
  // shows the topology; this shows what actually moves through it.
  (function syncDiagram() {
    var svg = document.querySelector('.arch-diagram svg');
    if (!svg || reduced) return;

    var NS = 'http://www.w3.org/2000/svg';
    var lan = svg.querySelector('#w-lan');
    var wanOut = svg.querySelector('#w-wan-out');
    var wanIn = svg.querySelector('#w-wan-in');
    if (!lan || !wanOut || !wanIn) return;

    var packet = document.createElementNS(NS, 'circle');
    packet.setAttribute('r', '5.5');
    packet.setAttribute('class', 'pkt');
    packet.setAttribute('opacity', '0');
    svg.appendChild(packet);

    var stage = function (id) { return svg.querySelector('[data-stage="' + id + '"]'); };
    var lit = function (el, on) { el && el.classList.toggle('lit', !!on); };

    // A <line> has no getPointAtLength, so give it the same interface.
    var walker = function (el) {
      if (el.getTotalLength) return el;
      return null;
    };
    var lineWalk = function (el, t) {
      var x1 = +el.getAttribute('x1'), x2 = +el.getAttribute('x2');
      var y1 = +el.getAttribute('y1'), y2 = +el.getAttribute('y2');
      return { x: x1 + (x2 - x1) * t, y: y1 + (y2 - y1) * t };
    };

    var travel = function (el, reverse, ms) {
      return new Promise(function (done) {
        var isPath = !!el.getTotalLength;
        var len = isPath ? el.getTotalLength() : 1;
        var t0 = null;
        // Place it before revealing it. Showing it first leaves the packet
        // parked at 0,0 — a stray dot in the corner of the diagram — for as
        // long as it takes the first frame to arrive, and permanently if
        // frames never come (a backgrounded tab, or a non-compositing view).
        var origin = isPath ? el.getPointAtLength(reverse ? len : 0) : lineWalk(el, reverse ? 1 : 0);
        packet.setAttribute('cx', origin.x);
        packet.setAttribute('cy', origin.y);
        packet.setAttribute('opacity', '1');
        var step = function (ts) {
          if (t0 === null) t0 = ts;
          var t = Math.min(1, (ts - t0) / ms);
          var u = reverse ? 1 - t : t;
          var p = isPath ? el.getPointAtLength(len * u) : lineWalk(el, u);
          packet.setAttribute('cx', p.x);
          packet.setAttribute('cy', p.y);
          if (t < 1) requestAnimationFrame(step);
          else { packet.setAttribute('opacity', '0'); done(); }
        };
        requestAnimationFrame(step);
      });
    };

    var wait = function (ms) { return new Promise(function (r) { setTimeout(r, ms); }); };

    var pipeline = function (dev, order) {
      // light watcher -> snapshots -> delta (or the reverse on arrival)
      var seq = order === 'in' ? [2, 1, 0] : [0, 1, 2];
      return seq.reduce(function (chain, i) {
        return chain.then(function () {
          var el = stage(dev + i);
          lit(el, true);
          return wait(210).then(function () { lit(el, false); });
        });
      }, Promise.resolve());
    };

    var running = false;
    var loop = function () {
      if (running) return;
      running = true;
      var viaLan = true;
      var cycle = function () {
        if (!inView) { running = false; return; }
        pipeline('a', 'out')
          .then(function () {
            return viaLan
              ? travel(lan, false, 900)
              : travel(wanOut, false, 620)
                  .then(function () { return wait(120); })
                  .then(function () { return travel(wanIn, true, 620); });
          })
          .then(function () { return pipeline('b', 'in'); })
          .then(function () { return wait(1100); })
          .then(function () { viaLan = !viaLan; cycle(); });
      };
      cycle();
    };

    var inView = false;
    var observed = false;
    // Observe the wrapping element rather than the <svg>: an observer aimed
    // at an SVG root is unreliable across engines.
    var watched = svg.closest ? (svg.closest('.arch-diagram') || svg.parentNode) : svg.parentNode;
    if ('IntersectionObserver' in window && watched) {
      new IntersectionObserver(function (es) {
        observed = true;
        es.forEach(function (e) {
          inView = e.isIntersecting;
          if (inView) loop();
        });
      }, { threshold: 0.2 }).observe(watched);
    } else {
      inView = true; loop();
    }

    // Same failure mode as the scroll-reveal above: the observer can exist
    // and never call back. Without this the diagram just sits still forever.
    setTimeout(function () {
      if (!observed) {
        inView = true;
        // Re-check on scroll instead, so it still stops when off-screen.
        window.addEventListener('scroll', function () {
          var r = watched.getBoundingClientRect();
          inView = r.top < window.innerHeight && r.bottom > 0;
          if (inView) loop();
        }, { passive: true });
        loop();
      }
    }, 1500);
  })();

  // ---- FAQ panels open and close instead of snapping -------------
  // <details> has no native transition: the panel appears in one frame.
  // Height cannot be transitioned from a fixed value to auto either, so
  // both heights are measured and the element is animated between them.
  (function faqReveal() {
    var items = document.querySelectorAll('details.faq-item');
    if (!items.length) return;
    // Element.animate keeps this to one call and cleans up after itself.
    if (!items[0].animate) return;

    items.forEach(function (d) {
      var summary = d.querySelector('summary');
      if (!summary) return;
      var anim = null;

      summary.addEventListener('click', function (e) {
        // Under reduced motion, let the browser do its instant thing.
        if (reduced) return;
        e.preventDefault();
        if (anim) { anim.cancel(); anim = null; }

        var startH = d.offsetHeight;
        var endH;
        var closing = d.open;

        if (closing) {
          // Measure the collapsed height without letting it paint: toggling
          // open forces layout, and layout is not paint, so nothing flashes.
          d.open = false;
          endH = d.offsetHeight;
          d.open = true;          // keep the answer visible while it shrinks
          d.classList.add('closing');   // but flip the icon straight away
        } else {
          d.open = true;
          endH = d.offsetHeight;
        }

        d.style.overflow = 'hidden';
        anim = d.animate(
          [{ height: startH + 'px' }, { height: endH + 'px' }],
          { duration: 230, easing: 'cubic-bezier(0.2, 0, 0.2, 1)' }
        );

        // Settling must not depend on the animation finishing. A
        // backgrounded tab can suspend it indefinitely, and an interrupted
        // one never fires onfinish at all — either would strand the panel
        // with overflow:hidden and the wrong open state, i.e. an answer
        // that will not close. Whichever comes first wins; settle guards
        // against running twice.
        var settled = false;
        var settle = function () {
          if (settled) return;
          settled = true;
          // Cancel before clearing. A running animation keeps applying its
          // own height, so clearing the inline styles alone leaves the
          // element pinned to the start keyframe — an open panel stuck at
          // its collapsed height. The settled flag absorbs the re-entry
          // from oncancel.
          var a = anim;
          anim = null;
          if (a) { try { a.cancel(); } catch (err) {} }
          d.style.overflow = '';
          d.style.height = '';
          d.classList.remove('closing');
          if (closing) d.open = false;
        };
        anim.onfinish = settle;
        anim.oncancel = settle;
        setTimeout(settle, 400);
      });
    });
  })();

  // ---- Navbar reacts to scroll -----------------------------------
  // A fixed bar over a page that scrolls under it should say so.
  (function navOnScroll() {
    var bar = document.querySelector('.navbar');
    if (!bar) return;
    var apply = function () { bar.classList.toggle('scrolled', window.scrollY > 12); };
    window.addEventListener('scroll', apply, { passive: true });
    apply();
  })();

  // ---- Which section am I in ------------------------------------
  // Marks the nav link for whichever section currently occupies the
  // middle of the viewport, so the bar tracks position instead of
  // sitting inert for the whole page.
  (function scrollSpy() {
    var links = [].slice.call(document.querySelectorAll('.nav-links > a[href^="#"]'));
    if (!links.length) return;
    var targets = links
      .map(function (a) {
        var el = document.querySelector(a.getAttribute('href'));
        return el ? { a: a, el: el } : null;
      })
      .filter(Boolean);
    if (!targets.length) return;

    var current = null;
    var update = function () {
      var line = window.scrollY + window.innerHeight * 0.35;
      var best = null;
      targets.forEach(function (t) {
        if (t.el.offsetTop <= line) best = t;
      });
      if (best === current) return;
      if (current) current.a.classList.remove('here');
      if (best) best.a.classList.add('here');
      current = best;
    };
    window.addEventListener('scroll', update, { passive: true });
    window.addEventListener('resize', update);
    update();
  })();

  // ---- Numbers count up when they arrive -------------------------
  // Only for figures already on the page; nothing invented. Falls
  // straight to the final value under reduced motion, and a timer
  // guarantees the real number lands even if frames never come.
  (function countUp() {
    var animate = function (el, to) {
      var text = el.textContent;
      var settle = function () { el.textContent = to.toLocaleString(); };
      if (reduced) { settle(); return; }
      var dur = 900, t0 = null;
      var step = function (ts) {
        if (t0 === null) t0 = ts;
        var p = Math.min(1, (ts - t0) / dur);
        var eased = 1 - Math.pow(1 - p, 3);
        el.textContent = Math.round(to * eased).toLocaleString();
        if (p < 1) requestAnimationFrame(step);
      };
      requestAnimationFrame(step);
      setTimeout(settle, dur + 400);
    };
    window.__osCountUp = animate;
  })();

  // ---- Promote the visitor's own platform ------------------------
  // Reordering the download cards would move a target under a cursor
  // already heading for it, so this only marks the matching one.
  (function ownPlatform() {
    var ua = navigator.userAgent || '';
    var os = /Windows/i.test(ua) ? 'windows'
           : /Linux|X11|CrOS/i.test(ua) ? 'linux'
           : /Mac/i.test(ua) ? 'mac' : null;
    if (!os) return;
    document.querySelectorAll('[data-platform]').forEach(function (card) {
      if (card.getAttribute('data-platform') === os) {
        card.classList.add('is-yours');
        var tag = card.querySelector('.dl-os');
        if (tag) tag.textContent = tag.textContent + ' · your system';
      }
    });
  })();

  // ---- Copy buttons on every command block -----------------------
  (function copyable() {
    // .guide-code was missing: a step-by-step guide whose commands cannot
    // be copied is the one place a copy button actually matters.
    var blocks = document.querySelectorAll('.terminal-body, pre.cmd, .cli-cmd, .guide-code');
    blocks.forEach(function (block) {
      if (block.querySelector('.copy-btn')) return;
      var btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'copy-btn';
      btn.textContent = 'Copy';
      btn.setAttribute('aria-label', 'Copy command');
      btn.addEventListener('click', function () {
        // Strip the shell prompt so the paste is runnable as-is.
        var text = block.innerText.replace(/^\s*[$#]\s+/gm, '').trim();
        var ok = function () {
          btn.textContent = 'Copied';
          setTimeout(function () { btn.textContent = 'Copy'; }, 1400);
        };
        if (navigator.clipboard && navigator.clipboard.writeText) {
          navigator.clipboard.writeText(text).then(ok, function () { legacy(text, ok); });
        } else { legacy(text, ok); }
      });
      block.classList.add('has-copy');
      block.appendChild(btn);
    });
    function legacy(text, ok) {
      try {
        var ta = document.createElement('textarea');
        ta.value = text; ta.setAttribute('readonly', '');
        ta.style.cssText = 'position:fixed;top:-1000px;opacity:0';
        document.body.appendChild(ta); ta.select();
        document.execCommand('copy'); document.body.removeChild(ta); ok();
      } catch (e) { /* leave the label alone; the text is selectable */ }
    }
  })();
})();

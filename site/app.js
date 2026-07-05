// Progressive enhancement marker — entrance choreography and OS tabs are gated on this.
document.documentElement.classList.add('js-loaded');

var REDUCED_MOTION = matchMedia('(prefers-reduced-motion: reduce)');

// Theme toggle — persists to localStorage, follows OS changes until the user picks.
(function(){
  var t = document.querySelector('[data-theme-toggle]'), r = document.documentElement;
  if(!t) return;
  t.hidden = false;
  var sun = '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="5"/><path d="M12 1v2M12 21v2M4.22 4.22l1.42 1.42M18.36 18.36l1.42 1.42M1 12h2M21 12h2M4.22 19.78l1.42-1.42M18.36 5.64l1.42-1.42"/></svg>';
  var moon = '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>';
  function current(){ return r.getAttribute('data-theme') === 'light' ? 'light' : 'dark'; }
  function render(){
    var d = current();
    t.innerHTML = d === 'dark' ? sun : moon;
    t.setAttribute('aria-label', d === 'dark' ? 'Switch to light mode' : 'Switch to dark mode');
  }
  function set(d, persist){
    r.setAttribute('data-theme', d);
    if(persist){ try{ localStorage.setItem('theme', d); }catch(e){} }
    render();
    document.dispatchEvent(new CustomEvent('themechange'));
  }
  render();
  t.addEventListener('click', function(){ set(current() === 'dark' ? 'light' : 'dark', true); });
  matchMedia('(prefers-color-scheme: light)').addEventListener('change', function(e){
    var stored = null;
    try{ stored = localStorage.getItem('theme'); }catch(err){}
    if(!stored) set(e.matches ? 'light' : 'dark', false);
  });
})();

// Header scrolled state
(function(){
  var h = document.getElementById('header');
  if(!h) return;
  var on = function(){ h.classList.toggle('is-scrolled', window.scrollY > 8); };
  on(); addEventListener('scroll', on, {passive:true});
})();

// Scrollspy — highlights the nav link of the section in view.
(function(){
  if(!('IntersectionObserver' in window)) return;
  var links = document.querySelectorAll('.nav a[href^="#"]');
  var map = {};
  links.forEach(function(a){ map[a.getAttribute('href').slice(1)] = a; });
  var io = new IntersectionObserver(function(entries){
    entries.forEach(function(en){
      var a = map[en.target.id];
      if(!a) return;
      if(en.isIntersecting){
        links.forEach(function(link){ link.classList.remove('is-active'); });
        a.classList.add('is-active');
      } else {
        a.classList.remove('is-active');
      }
    });
  }, {rootMargin:'-40% 0px -55% 0px'});
  Object.keys(map).forEach(function(id){
    var s = document.getElementById(id);
    if(s) io.observe(s);
  });
})();

// Copy buttons — announces success to assistive tech, only reports real success.
document.querySelectorAll('.copy-btn').forEach(function(btn){
  var wrap = btn.parentElement;
  var status = document.createElement('span');
  status.className = 'sr-only';
  status.setAttribute('aria-live', 'polite');
  wrap.appendChild(status);
  btn.addEventListener('click', function(){
    var code = wrap.querySelector('code').textContent;
    var done = function(){
      var o = btn.textContent;
      btn.textContent = 'Copied ✓';
      status.textContent = 'Command copied to clipboard';
      wrap.classList.remove('is-flashing');
      void wrap.offsetWidth;
      wrap.classList.add('is-flashing');
      setTimeout(function(){ btn.textContent = o; status.textContent = ''; }, 1500);
    };
    if(navigator.clipboard && navigator.clipboard.writeText){
      navigator.clipboard.writeText(code).then(done).catch(function(){ copyFallback(code, done); });
    } else copyFallback(code, done);
  });
});
function copyFallback(text, cb){
  var ta = document.createElement('textarea');
  ta.value = text; ta.style.position = 'fixed'; ta.style.opacity = '0';
  document.body.appendChild(ta); ta.select();
  try{ if(document.execCommand('copy')) cb(); }catch(e){}
  document.body.removeChild(ta);
}

// OS switch — plain toggle buttons (aria-pressed); without JS both blocks stay visible.
(function(){
  var tabs = document.querySelectorAll('.os-tab');
  var blocks = document.querySelectorAll('[data-os-block]');
  if(!tabs.length) return;
  function select(os){
    tabs.forEach(function(t){
      var a = t.dataset.os === os;
      t.classList.toggle('is-active', a);
      t.setAttribute('aria-pressed', a);
    });
    blocks.forEach(function(b){ b.classList.toggle('is-hidden', b.dataset.osBlock !== os); });
  }
  tabs.forEach(function(tab){ tab.addEventListener('click', function(){ select(tab.dataset.os); }); });
  var platform = (navigator.userAgentData && navigator.userAgentData.platform) || navigator.platform || '';
  select(/win/i.test(platform) || /windows/i.test(navigator.userAgent) ? 'win' : 'unix');
})();

// Scroll reveal — staggered per sibling group via --i.
(function(){
  var els = document.querySelectorAll('.section__head, .fgroup, .audience-card, .contract, .showcase, .install-step, .step-card, .cta-band');
  var perParent = new Map();
  els.forEach(function(e){
    var n = perParent.get(e.parentElement) || 0;
    e.style.setProperty('--i', n);
    perParent.set(e.parentElement, n + 1);
    e.classList.add('reveal');
  });
  if(!('IntersectionObserver' in window)){ els.forEach(function(e){ e.classList.add('is-in'); }); return; }
  var io = new IntersectionObserver(function(ents){
    ents.forEach(function(en){
      if(en.isIntersecting){ en.target.classList.add('is-in'); io.unobserve(en.target); }
    });
  }, {threshold:.15, rootMargin:'0px 0px -8% 0px'});
  els.forEach(function(e){ io.observe(e); });
})();

// Pointer-tracked spotlight on glow cards (hover-gated, no idle cost).
// Rect is cached per hover to avoid layout thrash on every pointermove.
(function(){
  if(!matchMedia('(pointer: fine)').matches) return;
  document.querySelectorAll('.glow-card').forEach(function(card){
    var r;
    card.addEventListener('pointerenter', function(){
      r = card.getBoundingClientRect();
    });
    card.addEventListener('pointermove', function(e){
      if(!r) r = card.getBoundingClientRect();
      card.style.setProperty('--mx', (e.clientX - r.left) + 'px');
      card.style.setProperty('--my', (e.clientY - r.top) + 'px');
    }, {passive:true});
  });
})();

// Hero background: ambient node-graph canvas.
// Nodes drift, close pairs connect, and every few seconds a pulse travels
// along one edge — the graph answering a query. Paused when off-screen or
// tab-hidden; reduced motion gets a single static frame.
(function(){
  var canvas = document.querySelector('.hero__net');
  var hero = document.querySelector('.hero');
  if(!canvas || !hero || !canvas.getContext) return;
  var ctx = canvas.getContext('2d');
  var dpr = Math.min(devicePixelRatio || 1, 2);
  var W = 0, H = 0, nodes = [], hubs = [];
  var pulse = null, nextPulseAt = 0;
  var running = false, visible = true, raf = 0;
  var LINK_DIST = 130;
  var colors = {primary:'#3ee6b0', accent:'#59d0ff'};

  function readColors(){
    var cs = getComputedStyle(document.documentElement);
    colors.primary = cs.getPropertyValue('--color-primary').trim() || colors.primary;
    colors.accent = cs.getPropertyValue('--color-accent-2').trim() || colors.accent;
  }

  function build(){
    var rect = hero.getBoundingClientRect();
    W = rect.width; H = rect.height;
    canvas.width = W * dpr; canvas.height = H * dpr;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    var count = Math.min(40, Math.round(W * H / 28000));
    nodes = [];
    for(var i = 0; i < count; i++){
      nodes.push({
        x: Math.random() * W, y: Math.random() * H,
        vx: (Math.random() - .5) * .3, vy: (Math.random() - .5) * .3,
        r: 1.5 + Math.random(), a: .35 + Math.random() * .25
      });
    }
    hubs = [];
    for(var h = 0; h < Math.min(4, count); h++){
      var n = nodes[Math.floor(Math.random() * count)];
      n.r = 3.5; n.a = .7; hubs.push(n);
    }
    pulse = null;
  }

  function startPulse(now){
    if(!nodes.length) return;
    // pick a connected pair, prefer starting from a hub
    var from = hubs.length ? hubs[Math.floor(Math.random() * hubs.length)] : nodes[0];
    var best = null, bestD = LINK_DIST;
    for(var i = 0; i < nodes.length; i++){
      var n = nodes[i];
      if(n === from) continue;
      var d = Math.hypot(n.x - from.x, n.y - from.y);
      if(d < bestD){ best = n; bestD = d; }
    }
    if(best) pulse = {from:from, to:best, t0:now, dur:600};
    nextPulseAt = now + 4000 + Math.random() * 2000;
  }

  function frame(now){
    ctx.clearRect(0, 0, W, H);
    var i, j, n;
    for(i = 0; i < nodes.length; i++){
      n = nodes[i];
      n.x += n.vx; n.y += n.vy;
      if(n.x < 0) n.x += W; if(n.x > W) n.x -= W;
      if(n.y < 0) n.y += H; if(n.y > H) n.y -= H;
    }
    ctx.lineWidth = 1;
    for(i = 0; i < nodes.length; i++){
      for(j = i + 1; j < nodes.length; j++){
        var a = nodes[i], b = nodes[j];
        var dx = a.x - b.x, dy = a.y - b.y;
        var d = Math.sqrt(dx * dx + dy * dy);
        if(d < LINK_DIST){
          ctx.globalAlpha = (1 - d / LINK_DIST) * .28;
          ctx.strokeStyle = colors.primary;
          ctx.beginPath(); ctx.moveTo(a.x, a.y); ctx.lineTo(b.x, b.y); ctx.stroke();
        }
      }
    }
    for(i = 0; i < nodes.length; i++){
      n = nodes[i];
      ctx.globalAlpha = n.a;
      ctx.fillStyle = colors.primary;
      ctx.shadowBlur = n.r > 3 ? 8 : 0;
      ctx.shadowColor = colors.primary;
      ctx.beginPath(); ctx.arc(n.x, n.y, n.r, 0, Math.PI * 2); ctx.fill();
      ctx.shadowBlur = 0;
    }
    if(now !== undefined){
      if(pulse){
        var p = (now - pulse.t0) / pulse.dur;
        if(p >= 1) pulse = null;
        else{
          var e = p < .5 ? 2 * p * p : 1 - Math.pow(-2 * p + 2, 2) / 2; // ease-in-out
          var px = pulse.from.x + (pulse.to.x - pulse.from.x) * e;
          var py = pulse.from.y + (pulse.to.y - pulse.from.y) * e;
          ctx.globalAlpha = .9;
          ctx.fillStyle = colors.accent;
          ctx.shadowBlur = 10; ctx.shadowColor = colors.accent;
          ctx.beginPath(); ctx.arc(px, py, 2.4, 0, Math.PI * 2); ctx.fill();
          ctx.shadowBlur = 0;
        }
      } else if(now > nextPulseAt){
        startPulse(now);
      }
    }
    ctx.globalAlpha = 1;
  }

  function loop(now){
    if(!running) return;
    frame(now);
    raf = requestAnimationFrame(loop);
  }
  function setRunning(on){
    if(on === running) return;
    running = on;
    if(on){ raf = requestAnimationFrame(loop); }
    else cancelAnimationFrame(raf);
  }
  function evaluate(){
    setRunning(visible && !document.hidden && !REDUCED_MOTION.matches);
    if(REDUCED_MOTION.matches) frame(); // one static frame
  }

  readColors();
  build();
  if(REDUCED_MOTION.matches){ frame(); }
  if('IntersectionObserver' in window){
    new IntersectionObserver(function(en){
      visible = en[0].isIntersecting;
      evaluate();
    }).observe(hero);
  }
  document.addEventListener('visibilitychange', evaluate);
  REDUCED_MOTION.addEventListener('change', evaluate);
  document.addEventListener('themechange', function(){ readColors(); if(!running) frame(); });
  var resizeTimer;
  addEventListener('resize', function(){
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(function(){ build(); if(!running) frame(); }, 200);
  }, {passive:true});
  evaluate();
})();

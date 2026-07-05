// Theme toggle (no localStorage in sandboxed iframe)
(function(){
  const t=document.querySelector('[data-theme-toggle]'),r=document.documentElement;
  let d=matchMedia('(prefers-color-scheme:light)').matches?'light':'dark';
  r.setAttribute('data-theme',d);
  const sun='<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="5"/><path d="M12 1v2M12 21v2M4.22 4.22l1.42 1.42M18.36 18.36l1.42 1.42M1 12h2M21 12h2M4.22 19.78l1.42-1.42M18.36 5.64l1.42-1.42"/></svg>';
  const moon='<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>';
  function render(){t.innerHTML=d==='dark'?sun:moon;t.setAttribute('aria-label','Zu '+(d==='dark'?'hellem':'dunklem')+' Modus wechseln');}
  render();
  t.addEventListener('click',()=>{d=d==='dark'?'light':'dark';r.setAttribute('data-theme',d);render();});
})();

// Header scrolled state
(function(){
  const h=document.getElementById('header');
  const on=()=>h.classList.toggle('is-scrolled',window.scrollY>8);
  on();addEventListener('scroll',on,{passive:true});
})();

// Copy buttons
document.querySelectorAll('.copy-btn').forEach(btn=>{
  btn.addEventListener('click',()=>{
    const code=btn.parentElement.querySelector('code').innerText;
    const done=()=>{const o=btn.textContent;btn.textContent='Kopiert ✓';setTimeout(()=>btn.textContent=o,1500);};
    if(navigator.clipboard&&navigator.clipboard.writeText){navigator.clipboard.writeText(code).then(done).catch(()=>fallback(code,done));}
    else fallback(code,done);
  });
});
function fallback(text,cb){const ta=document.createElement('textarea');ta.value=text;ta.style.position='fixed';ta.style.opacity='0';document.body.appendChild(ta);ta.select();try{document.execCommand('copy');cb();}catch(e){}document.body.removeChild(ta);}

// OS tabs
(function(){
  const tabs=document.querySelectorAll('.os-tab');
  const blocks=document.querySelectorAll('[data-os-block]');
  tabs.forEach(tab=>tab.addEventListener('click',()=>{
    const os=tab.dataset.os;
    tabs.forEach(t=>{const a=t===tab;t.classList.toggle('is-active',a);t.setAttribute('aria-selected',a);});
    blocks.forEach(b=>b.classList.toggle('is-hidden',b.dataset.osBlock!==os));
  }));
})();

// Scroll reveal
(function(){
  const els=document.querySelectorAll('.section__head, .fgroup, .audience-card, .contract, .showcase, .install-step, .lang-cloud');
  els.forEach(e=>e.classList.add('reveal'));
  if(!('IntersectionObserver'in window)){els.forEach(e=>e.classList.add('is-in'));return;}
  const io=new IntersectionObserver((ents)=>{ents.forEach(en=>{if(en.isIntersecting){en.target.classList.add('is-in');io.unobserve(en.target);}});},{threshold:.12});
  els.forEach(e=>io.observe(e));
})();

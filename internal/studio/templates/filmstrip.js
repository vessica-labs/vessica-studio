  // ---- filmstrip: slide thumbnails while editing (click to jump, drag to reorder) ----
  const filmList=document.getElementById('filmList');
  const filmResize=new ResizeObserver(()=>filmList.style.setProperty('--film-preview-scale',Math.max(0,(filmList.clientWidth-2-2*parseFloat(getComputedStyle(filmList).paddingLeft))/1280)));
  filmResize.observe(filmList);
  // Each thumbnail renders in its own shadow root (with the page's stylesheets cloned in)
  // so slide ids and scripts never collide with the live slide in the document.
  let filmStyles=null;
  function filmStyleNodes(){
    if(!filmStyles){filmStyles=document.createDocumentFragment();document.querySelectorAll('head style,head link[rel="stylesheet"]').forEach(n=>filmStyles.appendChild(n.cloneNode(true)));}
    return filmStyles.cloneNode(true);
  }
  function miniClone(s){
    const host=document.createElement('div');host.className='fmini';
    const root=host.attachShadow({mode:'open'});
    root.appendChild(filmStyleNodes());
    const c=s.cloneNode(true);c.classList.add('active','mini');
    if(window.__vhydrateAssets)window.__vhydrateAssets(c,'low');
    c.removeAttribute('data-vstd');c.querySelectorAll('[contenteditable]').forEach(el=>el.removeAttribute('contenteditable'));
    c.querySelectorAll('video').forEach(v=>{v.removeAttribute('autoplay');v.muted=true;});
    // keep the slide's own inline style (themes put backgrounds there), then pin the thumbnail geometry
    c.style.cssText=(c.getAttribute('style')||'')+';display:block;width:1280px;height:720px;position:absolute;top:0;left:0;transform:scale(var(--film-preview-scale, .136));transform-origin:top left;pointer-events:none';
    root.appendChild(c);
    return host;
  }
  function filmThumb(s,i){
    const t=document.createElement('div');t.className='fthumb';t.dataset.i=i;t.dataset.vstd=s.dataset.vstd||'';t.tabIndex=0;
    const parked=s.hasAttribute('data-parked'),title=s.querySelector('.s-title,h1,h2')?.textContent.trim()||s.dataset.sec||'Slide '+(i+1);
    t.setAttribute('role','button');t.setAttribute('aria-label',parked?'Unused slide: '+title:'Slide '+dispNum(i));
    if(s.hasAttribute('data-hidden'))t.classList.add('hiddenSlide');
    if(parked)t.classList.add('parkedSlide');
    t.appendChild(miniClone(s));
    const n=document.createElement('span');n.className='fn';n.textContent=parked?'Unused':dispNum(i);t.appendChild(n);
    t.addEventListener('click',()=>show(i));
    t.addEventListener('keydown',e=>{if(e.key==='Enter'||e.key===' '){e.preventDefault();show(i);}});
    if(canEditDeck()&&s.dataset.vstd){
      t.draggable=true;
      t.addEventListener('dragstart',e=>{dragSrc=s.dataset.vstd;ovSel.clear();t.classList.add('dragging');try{e.dataTransfer.effectAllowed='move';e.dataTransfer.setData('text/plain',dragSrc);}catch(_){}});
      t.addEventListener('dragend',()=>{t.classList.remove('dragging');filmList.querySelectorAll('.dropbefore,.dropafter').forEach(x=>x.classList.remove('dropbefore','dropafter'));});
      t.addEventListener('dragover',e=>{e.preventDefault();try{e.dataTransfer.dropEffect='move';}catch(_){}const r=t.getBoundingClientRect();const before=e.clientY<r.top+r.height/2;t.classList.toggle('dropbefore',before);t.classList.toggle('dropafter',!before);});
      t.addEventListener('dragleave',()=>t.classList.remove('dropbefore','dropafter'));
      t.addEventListener('drop',e=>{
        e.preventDefault();t.classList.remove('dropbefore','dropafter');
        if(!dragSrc||dragSrc===s.dataset.vstd)return;
        if(s.hasAttribute('data-parked')){dropSel({type:'park'},{stay:true,index:cur});return;}
        const r=t.getBoundingClientRect(),before=e.clientY<r.top+r.height/2;
        const rest=slides.filter(x=>x.dataset.vstd!==dragSrc&&!x.hasAttribute('data-parked'));
        const restAll=slides.filter(x=>x.dataset.vstd!==dragSrc);
        let k=rest.indexOf(s);if(before)k--;
        if(k<0)dropSel({type:'front'},{stay:true,index:0});
        else dropSel({type:'after',id:rest[k].dataset.vstd},{stay:true,index:restAll.indexOf(rest[k])+1});
      });
    }
    return t;
  }
  function buildFilm(){
    if(!editMode||!filmList)return;
    filmList.innerHTML='';
    slides.forEach((s,i)=>{if(!s.hasAttribute('data-parked'))filmList.appendChild(filmThumb(s,i));});
    if(slides.some(s=>s.hasAttribute('data-parked'))){
      const h=document.createElement('div');h.className='funused';h.textContent='Unused';filmList.appendChild(h);
      slides.forEach((s,i)=>{if(s.hasAttribute('data-parked'))filmList.appendChild(filmThumb(s,i));});
    }
    filmSetCur();
  }
  function refreshThumb(i){
    if(!editMode||!filmList)return;
    const t=filmList.querySelector('.fthumb[data-i="'+i+'"]');if(!t||!slides[i])return;
    const old=t.querySelector('.fmini');if(old)old.replaceWith(miniClone(slides[i]));
    t.classList.toggle('hiddenSlide',slides[i].hasAttribute('data-hidden'));
  }
  function filmSetCur(){
    if(!filmList)return;
    let curT=null;
    filmList.querySelectorAll('.fthumb').forEach(t=>{const on=parseInt(t.dataset.i)===cur;t.classList.toggle('cur',on);if(on)curT=t;});
    if(curT&&editMode)curT.scrollIntoView({block:'nearest'});
  }
  function toggleFilm(){
    const off=document.body.classList.toggle('nofilm');
    const b=document.getElementById('filmToggle');
    if(b){b.dataset.tip=off?'Show slide panel':'Hide slide panel';b.setAttribute('aria-label',b.dataset.tip);}
    try{localStorage.setItem('vstd_nofilm',off?'1':'');}catch(_){}
    fit();
  }
  try{if(localStorage.getItem('vstd_nofilm')==='1')toggleFilm();}catch(_){}

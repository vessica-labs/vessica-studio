  function canEditSlide() {
    return editMode && canEditDeck() && location.protocol.startsWith('http') && !!slides[cur]?.dataset.vstd;
  }

  const deleteDialog = document.createElement('dialog');
  deleteDialog.setAttribute('aria-labelledby','delete-slide-title');
  deleteDialog.innerHTML = '<h2 id="delete-slide-title"></h2><p>The slide and its companion will be removed.</p><button type="button" autofocus>Cancel</button> <button type="button" value="delete">Delete slide</button>';
  deleteDialog.querySelectorAll('button').forEach(button=>button.addEventListener('click',()=>deleteDialog.close(button.value)));
  document.body.appendChild(deleteDialog);
  let deletingSlide = false;
  async function deleteSlide() {
    if (!canEditSlide() || deletingSlide) return;
    if (slides.length === 1) { toast('A presentation must have at least one slide'); return; }
    const sec = slides[cur];
    const id = sec.dataset.vstd;
    const title = sec.querySelector('.s-title,h1,h2')?.textContent.trim();
    deletingSlide = true;
    try {
      deleteDialog.querySelector('h2').textContent = title ? 'Delete slide “' + title + '”?' : 'Delete this slide?';
      deleteDialog.returnValue = '';
      const confirmed = await new Promise(resolve=>{
        deleteDialog.addEventListener('close',()=>resolve(deleteDialog.returnValue === 'delete'),{once:true});
        deleteDialog.showModal();
      });
      if (!confirmed || !sec.isConnected || !canEditSlide()) return;
      stopTextEdit();
      if (autosaveInFlight) await autosaveInFlight;
      const out = await saveDirty();
      if (out.errors.length || out.conflicts.length) {
        reportSave(out);
        return;
      }
      const hash = window.VSTD.hashes?.[id];
      const response = await selfFetch('/api/deck/' + window.VSTD.deck + '/slide/' + id, {
        method: 'DELETE',
        headers: hash ? {'If-Match': '"' + hash + '"'} : {},
      });
      if (!response.ok) throw Error((await response.json().catch(()=>({}))).error || 'HTTP ' + response.status);
      const active = slides[cur];
      const index = slides.indexOf(sec);
      if (index !== -1) {
        slides.splice(index, 1);
        sec.remove();
        delete __baseline[id];
        delete undoStacks[id];
        delete redoStacks[id];
        dirtyIds.delete(id);
        window.VSTD.slides = slides.map(slide=>slide.dataset.vstd);
        if (window.VSTD.hashes) delete window.VSTD.hashes[id];
        const unchanged = slides.filter(slide=>!slideDirty(slide));
        renumberPills();
        unchanged.forEach(slide=>{__baseline[slide.dataset.vstd] = cleanSlideHTML(slide);});
        buildAgenda();
        buildFilm();
        if (overview.classList.contains('open')) buildOv();
        show(active === sec ? Math.min(index, slides.length - 1) : slides.indexOf(active));
      }
      toast('Slide deleted');
    } catch (error) {
      toast('Delete failed: ' + error.message, 6500);
    } finally {
      deletingSlide = false;
    }
  }

  const imagePicker = document.createElement('input');
  imagePicker.type = 'file';
  imagePicker.accept = 'image/png,image/jpeg,image/gif,image/webp';
  imagePicker.hidden = true;
  document.body.appendChild(imagePicker);
  let imagePickerSlide;
  function chooseImage() {
    if (!canEditSlide()) return;
    imagePickerSlide = slides[cur].dataset.vstd;
    imagePicker.click();
  }
  imagePicker.addEventListener('change', ()=>{
    const file = imagePicker.files[0];
    imagePicker.value = '';
    if (file && canEditSlide()) void uploadSlideImage(file, imagePickerSlide);
  });

  const isSlideImage = file=>/^image\/(png|jpe?g|gif|webp)$/i.test(file.type) || /\.(png|jpe?g|gif|webp)$/i.test(file.name);
  addEventListener('dragover', event=>{
    if (!canEditSlide() || !event.dataTransfer?.types.includes('Files')) return;
    event.preventDefault();
    document.body.classList.add('vdrop');
  });
  addEventListener('dragleave', event=>{
    if (!event.relatedTarget) document.body.classList.remove('vdrop');
  });
  addEventListener('drop', event=>{
    document.body.classList.remove('vdrop');
    if (!canEditSlide() || !event.dataTransfer?.files.length) return;
    event.preventDefault();
    event.stopPropagation();
    const file = [...event.dataTransfer.files].find(f=>isSlideImage(f) || /^video\//.test(f.type) || /\.(mp4|mov|webm|m4v)$/i.test(f.name));
    if (!file) { toast('Choose a PNG, JPEG, GIF or WebP image, or a video'); return; }
    if (!isSlideImage(file)) {
      if (file.size > 2 * 1024 ** 3) { toast('Video too large (2 GB max)'); return; }
      document.dispatchEvent(new CustomEvent('vstd:upload-video',{detail:file}));
      return;
    }
    const rect = frame.getBoundingClientRect();
    const point = event.target.closest('#frame')
      ? {x:(event.clientX - rect.left) / scale, y:(event.clientY - rect.top) / scale}
      : undefined;
    void uploadSlideImage(file, slides[cur].dataset.vstd, point);
  }, true);

  addEventListener('paste', event=>{
    if (!canEditSlide() || event.target.closest?.('input,textarea,select,[contenteditable="true"]')) return;
    const image = [...(event.clipboardData?.items || [])].find(item=>item.kind === 'file' && /^image\//.test(item.type));
    const file = image?.getAsFile();
    if (!file) return;
    event.preventDefault();
    event.stopPropagation();
    void uploadSlideImage(file, slides[cur].dataset.vstd);
  }, true);

  async function uploadSlideImage(file, id, point) {
    if (!isSlideImage(file)) { toast('Choose a PNG, JPEG, GIF or WebP image'); return; }
    if (file.size > 16 * 1024 ** 2) { toast('Image too large (16 MB max)'); return; }
    let name = file.name;
    if (!/\.(png|jpe?g|gif|webp)$/i.test(name)) name = 'image.' + file.type.split('/')[1];
    toast('Uploading image…', 30000);
    try {
      const query = new URLSearchParams({filename:name, deck:window.VSTD.deck});
      const response = await fetch('/api/asset/image?' + query, {
        method:'POST', headers:{'Content-Type':'application/octet-stream'}, body:file,
      });
      const result = await response.json().catch(()=>({}));
      if (!response.ok || !result.asset) throw Error(result.error || (response.status === 413 ? 'Image exceeds the upload limit' : 'HTTP ' + response.status));
      const sec = slides.find(slide=>slide.dataset.vstd === id);
      if (!sec) throw Error('The destination slide was deleted');
      const [width, height] = result.asset.size.split('x').map(Number);
      const ratio = Math.min(560 / width, 400 / height, 1);
      const w = Math.max(1, Math.round(width * ratio)), h = Math.max(1, Math.round(height * ratio));
      const x = Math.round(Math.min(1280 - w, Math.max(0, (point?.x ?? 640) - w / 2)));
      const y = Math.round(Math.min(720 - h, Math.max(0, (point?.y ?? 360) - h / 2)));
      const img = document.createElement('img');
      img.src = '/library/' + result.asset.file;
      img.alt = result.asset.id;
      img.draggable = false;
      img.dataset.vstdId = crypto.randomUUID();
      img.style.cssText = 'position:absolute;z-index:10;object-fit:cover;left:' + x + 'px;top:' + y + 'px;width:' + w + 'px;height:' + h + 'px';
      pushUndo(sec);
      sec.appendChild(img);
      if (editMode && sec === slides[cur]) { stopTextEdit(); selectEl(img); }
      refreshThumb(slides.indexOf(sec));
      if (autosaveInFlight) await autosaveInFlight;
      const out = await saveSlide(sec);
      if (out.status !== 'ok') throw Error(out.status === 'conflict' ? 'The slide changed elsewhere' : 'The slide save is pending');
      dirtyCheck();
      toast('Image added');
    } catch (error) {
      toast('Image could not be saved: ' + error.message, 6500);
    }
  }

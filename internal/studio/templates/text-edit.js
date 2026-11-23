  let savedRange=null;
  function activeRange(allowCaret=false){
    if(!editingText||!selEl)return null;
    const sel=getSelection();
    const inside=range=>range&&(allowCaret||!range.collapsed)&&selEl.contains(range.commonAncestorContainer);
    const current=sel.rangeCount?sel.getRangeAt(0):null;
    if(inside(current))return current;
    if(inside(savedRange)){
      sel.removeAllRanges();sel.addRange(savedRange);return savedRange;
    }
    return null;
  }
  function toggleStyle(prop,onVal,offVal,isOn){
    keepOrig(selEl);
    selEl.style[prop]=isOn?offVal:onVal;
    updateSelbox();
  }
  function fmt(cmd){
    if(!selEl)return;
    const cs=getComputedStyle(selEl);
    const range=activeRange();
    try{document.execCommand('styleWithCSS',false,true);}catch(_){}
    if(cmd==='bold'){
      if(range)document.execCommand('bold');
      else toggleStyle('fontWeight','700','400',parseInt(cs.fontWeight)>=600);
    }else if(cmd==='italic'){
      if(range)document.execCommand('italic');
      else toggleStyle('fontStyle','italic','normal',cs.fontStyle==='italic');
    }else if(cmd==='under'){
      if(range)document.execCommand('underline');
      else toggleStyle('textDecoration','underline','none',cs.textDecorationLine.includes('underline'));
    }else if(cmd==='fminus'||cmd==='fplus'){
      keepOrig(selEl);
      const cur=parseFloat(cs.fontSize)||19;
      selEl.style.fontSize=Math.max(8,cur+(cmd==='fplus'?2:-2))+'px';
    }
    updateSelbox();
  }

  function syncListControls(enabled){
    editTools.querySelectorAll('[data-list]').forEach(button=>{button.disabled=!enabled;});
  }

  function canFormatList(el){
    return editingText&&el?.matches('[contenteditable="true"]')&&!el.matches('ul,ol,table,button,audio,iframe');
  }

  function listEditingRange(range){
    if(!selEl.matches('p,span,h1,h2,h3,h4,h5,h6,label'))return range;

    // These text elements cannot contain a list in valid HTML. Keep the
    // object's attributes and move its contents to a block that can.
    const original=selEl;
    const typography=['fontSize','fontWeight','fontStyle','fontFamily','lineHeight','letterSpacing','color'];
    const originalStyle=getComputedStyle(original);
    const values=typography.map(property=>[property,originalStyle[property]]);
    const box=document.createElement('div');
    for(const attr of original.attributes)box.setAttribute(attr.name,attr.value);
    const {startContainer,startOffset,endContainer,endOffset}=range;
    box.append(...original.childNodes);
    original.replaceWith(box);
    const boxStyle=getComputedStyle(box);
    for(const [property,value] of values){
      if(boxStyle[property]!==value)box.style[property]=value;
    }
    selectEl(box);
    const replacement=document.createRange();
    replacement.setStart(startContainer===original?box:startContainer,startOffset);
    replacement.setEnd(endContainer===original?box:endContainer,endOffset);
    return replacement;
  }

  function formatList(command){
    if(!canFormatList(selEl))return;
    const range=activeRange(true);
    if(!range)return;
    const editRange=listEditingRange(range);
    selEl.focus();
    const selection=getSelection();
    selection.removeAllRanges();
    selection.addRange(editRange);
    savedRange=null;
    if(document.execCommand(command)){
      selEl.setAttribute('data-vstd-text-lists','');
      updateSelbox();
    }
  }

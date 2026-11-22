// Collect adjacent inline text in one shape. Layout children that paint their
// own objects are still visited by the main capture walk.
function pptxInlineText(node, allowBlocks) {
  if (node.nodeType === Node.TEXT_NODE) return true;
  if (!(node instanceof HTMLElement)) return false;
  const cs = getComputedStyle(node);
  if (cs.display === "none" || node.tagName === "BR") return true;
  if (node.matches('img,video,canvas,input,select,textarea,iframe,script,style,.notes,[data-edit="text"]')) return false;
  const block = allowBlocks && cs.display === "block" && node.matches("div,p,span");
  if (!block && cs.display !== "inline" && cs.display !== "contents") return false;
  return cs.position === "static" && cs.cssFloat === "none" && cs.transform === "none" &&
    cs.backgroundColor === "rgba(0, 0, 0, 0)" && cs.backgroundImage === "none" &&
    cs.maskImage === "none" && cs.boxShadow === "none" &&
    [cs.borderTopWidth, cs.borderRightWidth, cs.borderBottomWidth, cs.borderLeftWidth].every((width) => width === "0px") &&
    [...node.childNodes].every((child) => child.nodeType === Node.COMMENT_NODE || pptxInlineText(child, allowBlocks));
}

function pptxTextBox(el, nodes, sr, { geom, rotation, fontFamily, opacity }) {
  const cs = getComputedStyle(el);
  const scale = 1280 / sr.width;
  const length = (value) => parseFloat(value) || 0;
  const lineHeight = (style) => length(style.lineHeight) * scale;
  const runStyle = (style, parent, underlined) => ({
    font_family: fontFamily(style),
    font_size: (length(style.fontSize) || 16) * scale,
    bold: parseInt(style.fontWeight) >= 600,
    italic: style.fontStyle !== "normal",
    underline: underlined,
    color: style.color,
    opacity: opacity(parent),
    letter_spacing: length(style.letterSpacing) * scale,
  });
  const range = document.createRange();
  const paragraphs = [];
  const rects = [];
  let leading = 0;
  let runs = [];
  let runRects = [];
  let collapsibleTail = false;
  function finish(style, force) {
    if (collapsibleTail) {
      const last = runs.at(-1);
      last.text = last.text.replace(/ $/, "");
      if (!last.text) runs.pop();
    }
    const visibleRects = runRects.filter((rect) => rect.width > 0.01 && rect.height > 0.01);
    if (visibleRects.length && runs.length) rects.push(...visibleRects);
    if (force || runs.length) {
      const height = lineHeight(style);
      if (!rects.length) leading += height || length(style.fontSize) * scale * 1.2;
      paragraphs.push({ runs, line_height: height });
    }
    runs = [];
    runRects = [];
    collapsibleTail = false;
  }
  function collect(node, style, blockStyle, underlined) {
    if (style.display === "none" || style.visibility !== "visible" || Number(style.opacity) === 0) return;
    underlined ||= style.textDecorationLine.includes("underline");
    if (node.nodeType === Node.TEXT_NODE) {
      const collapse = /^(normal|nowrap|pre-line)$/.test(style.whiteSpace);
      const preserveBreaks = style.whiteSpace !== "normal" && style.whiteSpace !== "nowrap";
      const parts = preserveBreaks ? node.data.split("\n") : [node.data];
      let offset = 0;
      for (const [index, part] of parts.entries()) {
        if (index) finish(blockStyle, true);
        let text = part;
        if (collapse) {
          text = text.replace(/[\t\n\f\r ]+/g, " ");
          if (!runs.length || runs.at(-1).text.endsWith(" ")) text = text.replace(/^ /, "");
        }
        if (style.textTransform === "uppercase") text = text.toUpperCase();
        if (style.textTransform === "lowercase") text = text.toLowerCase();
        if (text) {
          runs.push({ text, ...runStyle(style, node.parentElement, underlined) });
          range.setStart(node, offset);
          range.setEnd(node, offset + part.length);
          runRects.push(...range.getClientRects());
          collapsibleTail = collapse && text.endsWith(" ");
        }
        offset += part.length + 1;
      }
    } else if (node.tagName === "BR") {
      finish(blockStyle, true);
    } else {
      const block = style.display === "block";
      if (block) finish(blockStyle, false);
      const prior = paragraphs.length;
      for (const child of node.childNodes) {
        collect(child, child.nodeType === Node.ELEMENT_NODE ? getComputedStyle(child) : style, block ? style : blockStyle, underlined);
      }
      if (block) finish(style, paragraphs.length === prior);
    }
  }
  const underlined = cs.textDecorationLine.includes("underline");
  for (const node of nodes) collect(node, node.nodeType === Node.ELEMENT_NODE ? getComputedStyle(node) : cs, cs, underlined);
  finish(cs, false);
  if (!rects.length) return null;

  const left = Math.min(...rects.map((r) => r.left));
  const right = Math.max(...rects.map((r) => r.right));
  const top = Math.min(...rects.map((r) => r.top));
  const bottom = Math.max(...rects.map((r) => r.bottom));
  const box = el.getBoundingClientRect();
  const contentLeft = box.left + length(cs.borderLeftWidth) + length(cs.paddingLeft);
  const contentRight = box.right - length(cs.borderRightWidth) - length(cs.paddingRight);
  let align = cs.textAlign.replace(/^-webkit-/, "");
  if (align === "start" || align === "end") align = (align === "start") === (cs.direction === "ltr") ? "left" : "right";
  if (align === "match-parent") align = "left";
  const nowrap = cs.whiteSpace === "nowrap" || cs.whiteSpace === "pre";
  const x = align === "left" ? left : Math.min(left, contentLeft);
  const g = geom({ left: x, top, width: Math.max(contentRight, right) - x, height: bottom - top }, sr);
  const rot = rotation(cs);
  if (Math.abs(rot) > 0.1) {
    // The browser gives a screen-aligned rectangle for rotated text.
    const rotated = geom({ left, top, width: right - left, height: bottom - top }, sr);
    const swap = Math.abs(rot % 180) > 45;
    g.w = (swap ? rotated.h : rotated.w) + 2;
    g.h = swap ? rotated.w : rotated.h;
    g.x = rotated.x + (rotated.w - g.w) / 2;
    g.y = rotated.y + (rotated.h - g.h) / 2;
    align = "left";
  }
  g.y -= leading;
  const defaults = runStyle(cs, el, underlined);
  const ending = Math.max(0, paragraphs.length - 1 - paragraphs.findLastIndex((p) => p.runs.length));
  return {
    kind: "text",
    name: (el.className || el.tagName).toString().slice(0, 80),
    ...g,
    ...defaults,
    h: Math.max(g.h + leading + ending * (lineHeight(cs) || defaults.font_size * 1.2), defaults.font_size * 1.35),
    rotation: rot,
    paragraphs,
    nowrap: nowrap || Math.abs(rot) > 0.1,
    align,
    measured: true,
  };
}

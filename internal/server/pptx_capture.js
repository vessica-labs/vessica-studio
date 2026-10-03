(async () => {
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
  if (document.fonts && document.fonts.ready) await document.fonts.ready;
  const images = [...document.images];
  await Promise.race([
    Promise.all(
      images.map((i) =>
        i.complete ? Promise.resolve() : i.decode().catch(() => {}),
      ),
    ),
    sleep(2500),
  ]);
  await sleep(250);
  const out = { title: document.title || "Vessica deck", slides: [] };
  const visible = (el, cs, r) =>
    cs.display !== "none" &&
    cs.visibility !== "hidden" &&
    parseFloat(cs.opacity || 1) > 0;
  const opaque = (c) =>
    c && c !== "transparent" && !/rgba\([^)]*,\s*0(?:\.0+)?\s*\)/.test(c);
  const opacity = (el) => {
    let o = 1,
      n = el;
    while (n && n.nodeType === 1) {
      o *= parseFloat(getComputedStyle(n).opacity || 1);
      n = n.parentElement;
    }
    return o;
  };
  function geom(r, sr) {
    const sx = 1280 / sr.width,
      sy = 720 / sr.height;
    return {
      x: (r.left - sr.left) * sx,
      y: (r.top - sr.top) * sy,
      w: r.width * sx,
      h: r.height * sy,
    };
  }
  function rotation(cs) {
    const m = (cs.transform || "").match(/^matrix\(([^)]+)\)$/);
    if (!m) return 0;
    const p = m[1].split(",").map(Number);
    return (Math.atan2(p[1], p[0]) * 180) / Math.PI;
  }
  function svgRotation(el) {
    const m = el.getCTM && el.getCTM();
    return m ? (Math.atan2(m.b, m.a) * 180) / Math.PI : 0;
  }
  function paintOpacity(color, alpha) {
    if (alpha >= 0.999 || color === "none") return color;
    const components = color.match(/[\d.]+/g);
    if (!components || components.length < 3) return color;
    return "rgba(" + components.slice(0, 3).join(",") + "," + alpha + ")";
  }
  function svgGradient(el) {
    const ref = getComputedStyle(el).fill.match(/url\(["']?#([^"')]+)["']?\)/),
      paint = ref && el.ownerSVGElement.querySelector("#" + CSS.escape(ref[1]));
    if (!paint || paint.localName !== "linearGradient") return {};
    const stops = [...paint.querySelectorAll("stop")].map((stop) => ({
      color: getComputedStyle(stop).stopColor,
      opacity:
        parseFloat(getComputedStyle(stop).stopOpacity) *
        parseFloat(getComputedStyle(el).fillOpacity),
      offset: stop.offset.baseVal,
    }));
    const x = paint.x2.baseVal.value - paint.x1.baseVal.value,
      y = paint.y2.baseVal.value - paint.y1.baseVal.value;
    return {
      gradient_stops: stops,
      gradient_angle: ((Math.atan2(y, x) * 180) / Math.PI + 360) % 360,
    };
  }
  function svgColor(el, prop) {
    let v = getComputedStyle(el)[prop] || "";
    const m = v.match(/url\(["']?#([^"')]+)["']?\)/);
    if (m) {
      const paint =
        el.ownerSVGElement &&
        el.ownerSVGElement.querySelector("#" + CSS.escape(m[1]));
      const stop =
        paint &&
        paint.querySelector(prop === "stroke" ? "stop:last-child" : "stop");
      if (stop)
        v =
          getComputedStyle(stop).stopColor ||
          stop.getAttribute("stop-color") ||
          v;
    }
    return v;
  }
  function styleBase(el, cs, r, sr) {
    const g = geom(r, sr),
      rot = rotation(cs);
    if (Math.abs(rot) > 0.1) {
      const cx = g.x + g.w / 2,
        cy = g.y + g.h / 2;
      g.w = (el.offsetWidth * 1280) / sr.width;
      g.h = (el.offsetHeight * 720) / sr.height;
      g.x = cx - g.w / 2;
      g.y = cy - g.h / 2;
    }
    const widths = ["Top", "Right", "Bottom", "Left"].map(
      (side) => parseFloat(cs["border" + side + "Width"]) || 0,
    );
    const uniform =
      widths.every((v) => v === widths[0]) &&
      ["Right", "Bottom", "Left"].every(
        (side) => cs["border" + side + "Color"] === cs.borderTopColor,
      );
    return {
      ...g,
      rotation: rot,
      opacity: opacity(el),
      fill: cs.backgroundColor,
      gradient: [],
      stroke: cs.borderTopColor,
      stroke_width: uniform ? widths[0] : 0,
    };
  }
  function borderElements(el, cs, r, sr, elements) {
    const sides = ["Top", "Right", "Bottom", "Left"],
      widths = sides.map(
        (side) => parseFloat(cs["border" + side + "Width"]) || 0,
      );
    if (
      widths.every((v) => v === widths[0]) &&
      sides.every((side) => cs["border" + side + "Color"] === cs.borderTopColor)
    )
      return;
    const g = styleBase(el, cs, r, sr),
      [t, right, b, l] = widths.map((v) => (v * 1280) / sr.width),
      w = g.w,
      h = g.h;
    const polygons = [
      [
        [0, 0],
        [w, 0],
        [w - right, t],
        [l, t],
      ],
      [
        [w, 0],
        [w, h],
        [w - right, h - b],
        [w - right, t],
      ],
      [
        [w, h],
        [0, h],
        [l, h - b],
        [w - right, h - b],
      ],
      [
        [0, h],
        [0, 0],
        [l, t],
        [l, h - b],
      ],
    ];
    const angle = (g.rotation * Math.PI) / 180,
      cos = Math.cos(angle),
      sin = Math.sin(angle);
    for (let i = 0; i < 4; i++) {
      const color = cs["border" + sides[i] + "Color"];
      if (!widths[i] || !opaque(color)) continue;
      const points = polygons[i].map(([x, y]) => ({
        x: g.x + w / 2 + (x - w / 2) * cos - (y - h / 2) * sin,
        y: g.y + h / 2 + (x - w / 2) * sin + (y - h / 2) * cos,
      }));
      elements.push({
        kind: "path",
        name: "CSS " + sides[i] + " border",
        points,
        closed: true,
        fill: color,
        opacity: g.opacity,
      });
    }
  }
  // Materialize generated CSS objects so the browser measures their true
  // location, including arrows, bullets and small chart labels.
  function materializePseudos() {
    const rule = document.createElement("style");
    rule.textContent =
      "[data-pptx-pseudos]::before,[data-pptx-pseudos]::after{content:none!important}";
    document.head.append(rule);
    for (const el of [...document.querySelectorAll(".slide,.slide *")]) {
      if (el.matches("svg,svg *,script,style,video,img,.notes,.notes *"))
        continue;
      const pseudos = ["::before", "::after"]
        .map((pseudo) => ({ pseudo, cs: getComputedStyle(el, pseudo) }))
        .filter(
          ({ cs }) =>
            cs.content !== "none" &&
            cs.content !== "normal" &&
            cs.display !== "none",
        );
      if (!pseudos.length) continue;
      const clones = pseudos.map(({ pseudo, cs }) => {
        const clone = document.createElement("span");
        for (const prop of cs)
          clone.style.setProperty(prop, cs.getPropertyValue(prop));
        clone.dataset.pptxPseudo = pseudo;
        clone.style.setProperty("animation", "none");
        clone.style.setProperty("transition", "none");
        try {
          clone.textContent = JSON.parse(cs.content);
        } catch (_) {
          clone.textContent = "";
        }
        return { pseudo, clone };
      });
      el.setAttribute("data-pptx-pseudos", "");
      for (const { pseudo, clone } of clones) {
        if (pseudo === "::before") el.prepend(clone);
        else el.append(clone);
      }
    }
  }
  materializePseudos();
  function point(svg, x, y, sr) {
    const p = svg.ownerSVGElement.createSVGPoint();
    p.x = x;
    p.y = y;
    const q = p.matrixTransform(svg.getScreenCTM());
    return {
      x: ((q.x - sr.left) * 1280) / sr.width,
      y: ((q.y - sr.top) * 720) / sr.height,
    };
  }
  // Computed style preserves percentage radii; resolve them against each axis.
  function cornerRadii(cs, width, height) {
    return ["TopLeft", "TopRight", "BottomRight", "BottomLeft"].map((corner) => {
      const values = cs["border" + corner + "Radius"].split(/\s+/);
      const resolve = (value, size) => (parseFloat(value) || 0) * (value.endsWith("%") ? size / 100 : 1);
      return { x: resolve(values[0], width), y: resolve(values[1] || values[0], height) };
    });
  }
  async function pictureData(img, r, cs) {
    try {
      if (img.decode)
        await Promise.race([img.decode().catch(() => {}), sleep(1200)]);
      const nw = img.naturalWidth || img.videoWidth,
        nh = img.naturalHeight || img.videoHeight;
      if (!nw || !nh) return "";
      const scale = 1;
      const w = Math.max(1, Math.round(r.width * scale)),
        h = Math.max(1, Math.round(r.height * scale));
      const c = document.createElement("canvas");
      c.width = w;
      c.height = h;
      const x = c.getContext("2d");
      const fit = cs.objectFit || "fill",
        src = nw / nh,
        dst = w / h;
      let sx = 0,
        sy = 0,
        sw = nw,
        sh = nh,
        dx = 0,
        dy = 0,
        dw = w,
        dh = h;
      if (fit === "cover") {
        if (src > dst) {
          sw = nh * dst;
          sx = (nw - sw) / 2;
        } else {
          sh = nw / dst;
          sy = (nh - sh) / 2;
        }
      } else if (fit === "contain") {
        if (src > dst) {
          dh = w / src;
          dy = (h - dh) / 2;
        } else {
          dw = h * src;
          dx = (w - dw) / 2;
        }
      }
      const pos = (cs.objectPosition || "50% 50%").split(/\s+/);
      const fraction = (v, def) =>
        v && v.endsWith("%") ? parseFloat(v) / 100 : def;
      if (fit === "cover") {
        sx = (nw - sw) * fraction(pos[0], 0.5);
        sy = (nh - sh) * fraction(pos[1], 0.5);
      }
      const radii = cornerRadii(cs, w, h);
      if (radii.some((radius) => radius.x > 0 || radius.y > 0)) {
        x.beginPath();
        x.roundRect(0, 0, w, h, radii);
        x.clip();
      }
      if (img instanceof Element) {
        for (
          let parent = img.parentElement;
          parent && !parent.classList.contains("vstd-page");
          parent = parent.parentElement
        ) {
          const ps = getComputedStyle(parent);
          if (ps.overflow === "hidden" || ps.overflow === "clip") {
            const pr = parent.getBoundingClientRect();
            const bl = parseFloat(ps.borderLeftWidth) || 0,
              br = parseFloat(ps.borderRightWidth) || 0,
              bt = parseFloat(ps.borderTopWidth) || 0,
              bb = parseFloat(ps.borderBottomWidth) || 0;
            const parentRadii = cornerRadii(ps, pr.width, pr.height).map((radius, i) => ({
              x: Math.max(0, radius.x - ([bl, br, br, bl][i])) * scale,
              y: Math.max(0, radius.y - ([bt, bt, bb, bb][i])) * scale,
            }));
            x.beginPath();
            x.roundRect(
              (pr.left + bl - r.left) * scale,
              (pr.top + bt - r.top) * scale,
              Math.max(0, pr.width - bl - br) * scale,
              Math.max(0, pr.height - bt - bb) * scale,
              parentRadii,
            );
            x.clip();
          }
        }
      }
      x.drawImage(img, sx, sy, sw, sh, dx, dy, dw, dh);
      return c.toDataURL("image/png");
    } catch (_) {
      return "";
    }
  }
  // Resolve the font actually available to Chromium, rather than exporting a
  // missing first choice and asking Office to choose a different fallback.
  const fontCache = new Map();
  function fontFamily(cs) {
    if (fontCache.has(cs.fontFamily)) return fontCache.get(cs.fontFamily);
    const c = document.createElement("canvas").getContext("2d"),
      sample = "mmmmmmmmmmWWWWiiii0123456789";
    const measure = (f) => {
      c.font = "72px " + f;
      return c.measureText(sample).width;
    };
    let chosen = "Arial";
    for (const f of cs.fontFamily.split(",")) {
      const name = f.trim();
      if (/^(sans-serif|serif|monospace)$/.test(name)) {
        chosen = {
          serif: "Times New Roman",
          "sans-serif": "Arial",
          monospace: "Courier New",
        }[name];
        break;
      }
      if (
        measure(name + ",monospace") !== measure("monospace") ||
        measure(name + ",serif") !== measure("serif")
      ) {
        chosen = name.replace(/^['"]|['"]$/g, "");
        break;
      }
    }
    fontCache.set(cs.fontFamily, chosen);
    return chosen;
  }
  // Capture each browser-laid-out line and inline style independently. Range
  // geometry includes the real padding, flex alignment and browser wrapping.
  function captureText(node, sr, elements) {
    const el = node.parentElement,
      cs = getComputedStyle(el),
      raw = node.nodeValue,
      range = document.createRange();
    const rot = rotation(cs);
    if (Math.abs(rot) > 0.1) {
      range.selectNodeContents(node);
      const r = range.getBoundingClientRect(),
        g = geom(r, sr),
        cx = g.x + g.w / 2,
        cy = g.y + g.h / 2;
      if (Math.abs(rot % 180) > 45) {
        const t = g.w;
        g.w = g.h;
        g.h = t;
      }
      g.x = cx - g.w / 2;
      g.y = cy - g.h / 2;
      elements.push({
        kind: "text",
        ...g,
        w: g.w + 2,
        rotation: rot,
        text:
          cs.textTransform === "uppercase"
            ? raw.trim().toUpperCase()
            : raw.trim(),
        measured: true,
        align: "left",
        valign: "top",
        nowrap: true,
        font_family: fontFamily(cs),
        font_size: parseFloat(cs.fontSize) || 16,
        bold: parseInt(cs.fontWeight) >= 600,
        color: cs.color,
        opacity: opacity(el),
        letter_spacing: parseFloat(cs.letterSpacing) || 0,
      });
      return;
    }
    const lines = [];
    let line = null;
    for (let i = 0; i < raw.length;) {
      const n = raw.codePointAt(i) > 65535 ? 2 : 1;
      range.setStart(node, i);
      range.setEnd(node, i + n);
      const r = range.getBoundingClientRect();
      let t = raw.slice(i, i + n);
      i += n;
      if (r.width < 0.01 || r.height < 0.01) continue;
      if (cs.whiteSpace === "normal" || cs.whiteSpace === "nowrap")
        t = t.replace(/\s/g, " ");
      if (cs.textTransform === "uppercase") t = t.toUpperCase();
      else if (cs.textTransform === "lowercase") t = t.toLowerCase();
      if (!line || Math.abs(line.top - r.top) > 1) {
        line = {
          text: "",
          left: r.left,
          right: r.right,
          top: r.top,
          bottom: r.bottom,
        };
        lines.push(line);
      }
      line.text += t;
      line.left = Math.min(line.left, r.left);
      line.right = Math.max(line.right, r.right);
      line.bottom = Math.max(line.bottom, r.bottom);
    }
    for (const l of lines) {
      if (!l.text.trim()) continue;
      const g = geom(
        {
          left: l.left,
          top: l.top,
          width: l.right - l.left,
          height: l.bottom - l.top,
        },
        sr,
      );
      const size = ((parseFloat(cs.fontSize) || 16) * 1280) / sr.width;
      elements.push({
        kind: "text",
        name: (el.className || el.tagName).toString().slice(0, 80),
        ...g,
        w: g.w + 2,
        h: Math.max(g.h, size * 1.35),
        text: l.text,
        nowrap: true,
        font_family: fontFamily(cs),
        font_size: size,
        bold: parseInt(cs.fontWeight) >= 600,
        italic: cs.fontStyle === "italic",
        color: cs.color,
        opacity: opacity(el),
        letter_spacing: parseFloat(cs.letterSpacing) || 0,
        align: "left",
        valign: "top",
        measured: true,
      });
    }
  }
  function svgPath(n, sr) {
    const tokens =
      (n.getAttribute("d") || "").match(
        /[a-zA-Z]|[-+]?(?:\d*\.\d+|\d+\.?\d*)(?:[eE][-+]?\d+)?/g,
      ) || [];
    const result = [];
    let i = 0,
      cmd = "",
      x = 0,
      y = 0,
      mx = 0,
      my = 0,
      cx = 0,
      cy = 0,
      prev = "";
    const counts = { M: 2, L: 2, H: 1, V: 1, C: 6, S: 4, Q: 4, T: 2, A: 7 };
    const add = (kind, pts = []) =>
      result.push({ kind, points: pts.map((p) => point(n, p[0], p[1], sr)) });
    while (i < tokens.length) {
      if (/[a-zA-Z]/.test(tokens[i])) cmd = tokens[i++];
      const c = cmd.toUpperCase(),
        rel = cmd !== c,
        ox = x,
        oy = y;
      if (c === "Z") {
        add("close");
        x = mx;
        y = my;
        prev = c;
        cmd = "";
        continue;
      }
      const count = counts[c];
      if (!count || i + count > tokens.length) break;
      const v = tokens.slice(i, i + count).map(Number);
      if (v.some(Number.isNaN)) break;
      i += count;
      const xy = (a, b) => [a + (rel ? ox : 0), b + (rel ? oy : 0)];
      let p;
      if (c === "M" || c === "L") {
        p = xy(v[0], v[1]);
        add(c === "M" ? "move" : "line", [p]);
        [x, y] = p;
        if (c === "M") {
          mx = x;
          my = y;
          cmd = rel ? "l" : "L";
        }
      } else if (c === "H") {
        x = v[0] + (rel ? ox : 0);
        add("line", [[x, y]]);
      } else if (c === "V") {
        y = v[0] + (rel ? oy : 0);
        add("line", [[x, y]]);
      } else if (c === "C") {
        const p1 = xy(v[0], v[1]),
          p2 = xy(v[2], v[3]);
        p = xy(v[4], v[5]);
        add("cubic", [p1, p2, p]);
        [cx, cy] = p2;
        [x, y] = p;
      } else if (c === "S") {
        const p1 =
            prev === "C" || prev === "S"
              ? [2 * ox - cx, 2 * oy - cy]
              : [ox, oy],
          p2 = xy(v[0], v[1]);
        p = xy(v[2], v[3]);
        add("cubic", [p1, p2, p]);
        [cx, cy] = p2;
        [x, y] = p;
      } else if (c === "Q") {
        const p1 = xy(v[0], v[1]);
        p = xy(v[2], v[3]);
        add("quad", [p1, p]);
        [cx, cy] = p1;
        [x, y] = p;
      } else if (c === "T") {
        const p1 =
          prev === "Q" || prev === "T" ? [2 * ox - cx, 2 * oy - cy] : [ox, oy];
        p = xy(v[0], v[1]);
        add("quad", [p1, p]);
        [cx, cy] = p1;
        [x, y] = p;
      } else if (c === "A") {
        p = xy(v[5], v[6]);
        const arc = document.createElementNS(
          "http://www.w3.org/2000/svg",
          "path",
        );
        arc.setAttribute(
          "d",
          "M" +
            ox +
            " " +
            oy +
            " A" +
            v.slice(0, 5).join(" ") +
            " " +
            p.join(" "),
        );
        const len = arc.getTotalLength(),
          steps = Math.max(2, Math.min(256, Math.ceil(len / 2)));
        for (let j = 1; j <= steps; j++) {
          const q = arc.getPointAtLength((len * j) / steps);
          add("line", [[q.x, q.y]]);
        }
        [x, y] = p;
      }
      prev = c;
    }
    return result;
  }
  const escapeXML = (s) =>
    s.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;");
  async function cssBackground(r, cs) {
    let bg = cs.backgroundImage,
      mask = cs.maskImage;
    if ((!bg || bg === "none") && (!mask || mask === "none")) return "";
    let combined = bg + "__MASK__" + mask;
    for (const m of [...combined.matchAll(/url\(["']?([^"')]+)["']?\)/g)]) {
      try {
        const response = await fetch(m[1]);
        if (!response.ok) throw new Error("background unavailable");
        const blob = await response.blob();
        const data = await new Promise((resolve) => {
          const reader = new FileReader();
          reader.onload = () => resolve(reader.result);
          reader.readAsDataURL(blob);
        });
        combined = combined.replace(m[0], 'url("' + data + '")');
      } catch (_) {
        combined = combined.replace(m[0], "none");
      }
    }
    [bg, mask] = combined.split("__MASK__");
    const w = Math.max(1, Math.ceil(r.width)),
      h = Math.max(1, Math.ceil(r.height));
    const style =
      "width:" +
      w +
      "px;height:" +
      h +
      "px;background-image:" +
      bg +
      ";background-size:" +
      cs.backgroundSize +
      ";background-position:" +
      cs.backgroundPosition +
      ";background-repeat:" +
      cs.backgroundRepeat +
      ";border-radius:" +
      cs.borderRadius +
      ";mask-image:" +
      mask +
      ";mask-size:" +
      cs.maskSize +
      ";mask-position:" +
      cs.maskPosition +
      ";mask-repeat:" +
      cs.maskRepeat +
      ";background-color:" +
      (mask !== "none" ? cs.backgroundColor : "transparent") +
      ";";
    const svg =
      '<svg xmlns="http://www.w3.org/2000/svg" width="' +
      w +
      '" height="' +
      h +
      '"><foreignObject width="100%" height="100%"><div xmlns="http://www.w3.org/1999/xhtml" style="' +
      escapeXML(style) +
      '"></div></foreignObject></svg>';
    try {
      const img = new Image();
      img.src = "data:image/svg+xml;charset=utf-8," + encodeURIComponent(svg);
      await img.decode();
      const c = document.createElement("canvas");
      c.width = w * 2;
      c.height = h * 2;
      c.getContext("2d").drawImage(img, 0, 0, c.width, c.height);
      return c.toDataURL("image/png");
    } catch (_) {
      return "";
    }
  }
  async function cssShadow(el, r, cs, sr, elements) {
    if (!cs.boxShadow || cs.boxShadow === "none") return;
    const pad = Math.ceil(
      Math.max(
        8,
        ...(
          cs.boxShadow.replace(/rgba?\([^)]*\)/g, "").match(/[-\d.]+px/g) || []
        ).map((v) => Math.abs(parseFloat(v)) * 3),
      ),
    );
    const w = Math.ceil(r.width + 2 * pad),
      h = Math.ceil(r.height + 2 * pad);
    const style =
      "position:absolute;left:" +
      pad +
      "px;top:" +
      pad +
      "px;width:" +
      r.width +
      "px;height:" +
      r.height +
      "px;border-radius:" +
      cs.borderRadius +
      ";box-shadow:" +
      cs.boxShadow +
      ";";
    const svg =
      '<svg xmlns="http://www.w3.org/2000/svg" width="' +
      w +
      '" height="' +
      h +
      '"><foreignObject width="100%" height="100%"><div xmlns="http://www.w3.org/1999/xhtml" style="' +
      escapeXML(style) +
      '"></div></foreignObject></svg>';
    try {
      const img = new Image();
      img.src = "data:image/svg+xml;charset=utf-8," + encodeURIComponent(svg);
      await img.decode();
      const c = document.createElement("canvas");
      c.width = w;
      c.height = h;
      c.getContext("2d").drawImage(img, 0, 0);
      elements.push({
        kind: "image",
        name: "CSS shadow",
        ...geom(
          { left: r.left - pad, top: r.top - pad, width: w, height: h },
          sr,
        ),
        opacity: opacity(el),
        image_data: c.toDataURL("image/png"),
      });
    } catch (_) {}
  }
  for (const page of document.querySelectorAll(".vstd-page")) {
    const slide = page.querySelector(".slide"),
      sr = slide.getBoundingClientRect();
    const elements = [];
    async function walk(el) {
      if (!(el instanceof Element) || el.matches(".notes,script,style")) return;
      const cs = getComputedStyle(el),
        r = el.getBoundingClientRect();
      if (!visible(el, cs, r)) return;
      if (r.width > 0.3 && r.height > 0.3) {
        await cssShadow(el, r, cs, sr, elements);
        const base = styleBase(el, cs, r, sr);
        const radii = cornerRadii(cs, r.width, r.height);
        const radius = Math.min(radii[0].x, radii[0].y);
        const shapeKind = radii.every((corner) => corner.x >= r.width / 2 && corner.y >= r.height / 2)
          ? "ellipse" : radius > 0 ? "roundRect" : "rect";
        if (
          (opaque(cs.backgroundColor) && cs.maskImage === "none") ||
          base.stroke_width > 0
        )
          elements.push({
            kind: shapeKind,
            name: (el.className || el.tagName).toString().slice(0, 80),
            ...base,
            gradient: [],
            radius,
            stroke_width: cs.backgroundImage === "none" ? base.stroke_width : 0,
          });
        borderElements(el, cs, r, sr, elements);
        if (cs.backgroundImage !== "none" || cs.maskImage !== "none") {
          const data = await cssBackground(r, cs);
          if (data)
            elements.push({
              kind: "image",
              name: "CSS background",
              ...geom(r, sr),
              opacity: base.opacity,
              image_data: data,
            });
          if (base.stroke_width > 0)
            elements.push({
              kind: shapeKind,
              ...base,
              fill: "",
              gradient: [],
              radius,
            });
        }
      }
      if (el.tagName === "IMG") {
        const data = await pictureData(el, r, cs);
        elements.push({
          kind: "image",
          name: el.alt || "Image",
          ...geom(r, sr),
          rotation: rotation(cs),
          opacity: opacity(el),
          image_data: data,
        });
        return;
      }
      if (el.tagName === "VIDEO") {
        let source = el;
        if (el.poster) {
          const p = new Image();
          p.src = el.poster;
          await Promise.race([p.decode().catch(() => {}), sleep(1800)]);
          source = p;
        }
        const data = await pictureData(source, r, cs);
        elements.push({
          kind: "video",
          video_id: el.dataset.vstdVideo || "",
          name: "Video",
          ...geom(r, sr),
          rotation: rotation(cs),
          opacity: opacity(el),
          image_data: data,
        });
        return;
      }
      if (el.localName === "svg") {
        for (const n of el.querySelectorAll(
          "rect,circle,ellipse,line,polyline,polygon,path,text",
        )) {
          const ns = getComputedStyle(n),
            nr = n.getBoundingClientRect();
          if (
            ns.display === "none" ||
            ns.visibility === "hidden" ||
            opacity(n) <= 0 ||
            (nr.width <= 0.3 && nr.height <= 0.3)
          )
            continue;
          const tag = (n.localName || n.tagName).toLowerCase();
          const transform = n.getScreenCTM(),
            scale = (Math.hypot(transform.a, transform.b) * 1280) / sr.width;
          const common = {
            ...svgGradient(n),
            ...geom(nr, sr),
            dash_array:
              ns.strokeDasharray === "none"
                ? []
                : ns.strokeDasharray
                    .split(/[ ,]+/)
                    .map((v) => parseFloat(v) * scale),
            line_cap: ns.strokeLinecap,
            name: (n.getAttribute("class") || n.tagName)
              .toString()
              .slice(0, 80),
            stroke: paintOpacity(
              svgColor(n, "stroke"),
              parseFloat(ns.strokeOpacity),
            ),
            fill:
              ns.fill === "none"
                ? ""
                : paintOpacity(svgColor(n, "fill"), parseFloat(ns.fillOpacity)),
            opacity: opacity(n),
            stroke_width:
              ns.stroke === "none"
                ? 0
                : (parseFloat(ns.strokeWidth) || 1) * scale,
          };
          if (tag === "text") {
            let g = geom(nr, sr),
              rot = svgRotation(n);
            if (Math.abs(rot % 180) > 45) {
              const cx = g.x + g.w / 2,
                cy = g.y + g.h / 2,
                t = g.w;
              g.w = g.h;
              g.h = t;
              g.x = cx - g.w / 2;
              g.y = cy - g.h / 2;
            }
            g.x -= 2;
            g.w += 4;
            elements.push({
              kind: "text",
              ...g,
              ...common,
              rotation: rot,
              nowrap: true,
              text: (n.textContent || "").trim(),
              font_family: fontFamily(ns),
              font_size: parseFloat(ns.fontSize) || 16,
              bold: parseInt(ns.fontWeight) >= 600,
              italic: ns.fontStyle === "italic",
              align:
                ns.textAnchor === "middle"
                  ? "center"
                  : ns.textAnchor === "end"
                    ? "right"
                    : "left",
              color: paintOpacity(
                svgColor(n, "fill"),
                parseFloat(ns.fillOpacity),
              ),
              valign: "middle",
            });
            continue;
          }
          if (tag === "rect")
            elements.push({
              kind:
                parseFloat(n.getAttribute("rx") || 0) > 0
                  ? "roundRect"
                  : "rect",
              radius:
                (parseFloat(n.getAttribute("rx") || 0) * nr.width) /
                (parseFloat(n.getAttribute("width")) || nr.width),
              ...geom(nr, sr),
              ...common,
            });
          else if (tag === "circle" || tag === "ellipse")
            elements.push({ kind: "ellipse", ...geom(nr, sr), ...common });
          else if (tag === "line")
            elements.push({
              kind: "line",
              ...common,
              points: [
                point(n, +n.getAttribute("x1"), +n.getAttribute("y1"), sr),
                point(n, +n.getAttribute("x2"), +n.getAttribute("y2"), sr),
              ],
            });
          else if (tag === "polyline" || tag === "polygon") {
            const pts = [...n.points].map((p) => point(n, p.x, p.y, sr));
            if (tag === "polygon" && pts.length) pts.push(pts[0]);
            elements.push({
              kind: "path",
              ...common,
              points: pts,
              closed: tag === "polygon",
            });
          } else if (tag === "path") {
            try {
              elements.push({
                kind: "path",
                ...common,
                commands: svgPath(n, sr),
              });
            } catch (_) {}
          }
        }
        return;
      }
      for (const child of el.childNodes) {
        if (child.nodeType === 3) captureText(child, sr, elements);
        else if (child.nodeType === 1) await walk(child);
      }
    }
    for (const video of slide.querySelectorAll("video[data-vstd-video]"))
      if (!video.poster)
        video.poster =
          "/assets/video/" +
          encodeURIComponent(video.dataset.vstdVideo) +
          "/poster";
    await walk(slide);
    out.slides.push({ id: slide.dataset.vstd || "", elements });
  }
  const pre = document.createElement("pre");
  pre.id = "vstd-pptx-json";
  pre.textContent = JSON.stringify(out);
  document.body.replaceChildren(pre);
})().catch((e) => {
  const pre = document.createElement("pre");
  pre.id = "vstd-pptx-error";
  pre.textContent = String((e && e.stack) || e);
  document.body.replaceChildren(pre);
});

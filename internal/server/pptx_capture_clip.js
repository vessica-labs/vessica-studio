function pptxClipPolygon(cs, width, height) {
  const match = cs.clipPath.match(/^polygon\((.*)\)(?: border-box)?$/);
  if (!match) return null;
  const vertices = match[1].split(/,(?![^()]*\))/);
  const fillRule = /^(nonzero|evenodd)$/.test(vertices[0].trim()) ? vertices.shift().trim() : "nonzero";
  const resolve = (value, size) =>
    [...CSSNumericValue.parse(value).toSum().values].reduce((total, term) => {
      if (term.unit === "percent") return total + term.value * size / 100;
      if (term.unit === "number" && term.value === 0) return total;
      return total + term.to("px").value;
    }, 0);
  try {
    const points = vertices.map((vertex) => {
      const pair = vertex.trim().match(/calc\([^()]*\)|[^\s()]+/g);
      if (!pair || pair.length !== 2) throw new Error("unsupported polygon coordinate");
      return { x: resolve(pair[0], width), y: resolve(pair[1], height) };
    });
    return points.length >= 3 ? { points, fillRule } : null;
  } catch (_) {
    return null;
  }
}

function pptxClipPicture(ctx, cs, width, height) {
  const polygon = pptxClipPolygon(cs, width, height);
  if (!polygon) return;
  ctx.beginPath();
  ctx.moveTo(polygon.points[0].x, polygon.points[0].y);
  for (const { x, y } of polygon.points.slice(1)) ctx.lineTo(x, y);
  ctx.closePath();
  ctx.clip(polygon.fillRule);
}

function pptxPolygonShape(polygon, base) {
  const angle = base.rotation * Math.PI / 180;
  const cos = Math.cos(angle), sin = Math.sin(angle);
  const points = polygon.points.map(({ x, y }) => ({
    x: base.x + base.w / 2 + (x - base.w / 2) * cos - (y - base.h / 2) * sin,
    y: base.y + base.h / 2 + (x - base.w / 2) * sin + (y - base.h / 2) * cos,
  }));
  return { ...base, kind: "path", rotation: 0, points, closed: true, stroke_width: 0 };
}

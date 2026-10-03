"""Compare browser slides to native PowerPoint PNGs and write an offline gallery."""
import argparse
import html
import json
import shutil
from pathlib import Path

import numpy as np
from PIL import Image

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--reference', type=Path, required=True)
p.add_argument('--baseline', type=Path, required=True)
p.add_argument('--candidate', type=Path, required=True)
p.add_argument('--output', type=Path, required=True)
p.add_argument('--exclude-id', action='append', default=[])
p.add_argument('--source-ref', default='')
a = p.parse_args()
slides = json.loads((a.reference / 'slides.json').read_text())
a.output.mkdir(parents=True, exist_ok=True)
rows = []
cards = []
for n, slide in enumerate(slides, 1):
    names = {'html': a.reference / f'html-{n:02}.png', 'baseline': a.baseline / f'Slide{n}.png', 'candidate': a.candidate / f'Slide{n}.png'}
    arrays = {}
    for key, path in names.items():
        with Image.open(path) as im:
            if im.size != (1280, 720):
                raise ValueError(f'{path}: expected 1280x720, got {im.size}')
            arrays[key] = np.asarray(im.convert('RGB'), dtype=np.float32)
        shutil.copyfile(path, a.output / f'{key}-{n:02}.png')
    scores = {}
    for key in ['baseline', 'candidate']:
        delta = np.abs(arrays['html'] - arrays[key])
        scores[key] = {'mae': float(delta.mean()), 'changed_fraction': float((delta.max(axis=2) > 20).mean())}
    included = slide['id'] not in a.exclude_id
    rows.append({'n': n, **slide, 'included': included, **scores})
    difference = np.clip(np.abs(arrays['html'] - arrays['candidate']) * 3, 0, 255).astype(np.uint8)
    Image.fromarray(difference).save(a.output / f'difference-{n:02}.png')
    cards.append(f'''<article><h2>{n}. {html.escape(slide['title'])}</h2><p>{html.escape(slide['id'])} · MAE {scores['baseline']['mae']:.2f} → {scores['candidate']['mae']:.2f}{'' if included else ' · excluded from aggregate'}</p><div class="images"><figure><figcaption>HTML reference</figcaption><img src="html-{n:02}.png"></figure><figure><figcaption>Original native PowerPoint</figcaption><img src="baseline-{n:02}.png"></figure><figure><figcaption>Candidate native PowerPoint</figcaption><img src="candidate-{n:02}.png"></figure></div><details><summary>Pixel overlay and amplified difference</summary><label>Candidate opacity <input type="range" min="0" max="100" value="50" oninput="this.closest('details').querySelector('.overlay').style.opacity=this.value/100"></label><div class="compare"><img src="html-{n:02}.png"><img class="overlay" src="candidate-{n:02}.png"></div><img src="difference-{n:02}.png"></details></article>''')
scored = [r for r in rows if r['included']]
if not scored:
    raise ValueError('All slides excluded')
means = {key: {metric: float(np.mean([r[key][metric] for r in scored])) for metric in ['mae', 'changed_fraction']} for key in ['baseline', 'candidate']}
reduction = 100 * (1 - means['candidate']['mae'] / means['baseline']['mae']) if means['baseline']['mae'] else 0
summary = {'source_ref': a.source_ref, 'scored_slides': len(scored), 'excluded_ids': a.exclude_id, 'means': means, 'mae_reduction_percent': reduction, 'slides': rows}
(a.output / 'metrics.json').write_text(json.dumps(summary, indent=2))
(a.output / 'index.html').write_text(f'''<!doctype html><html><head><meta charset="utf-8"><title>Editable PowerPoint fidelity review</title><style>body{{background:#f4f5f7;color:#19232c;font:16px system-ui;margin:24px}}article{{background:white;padding:20px;margin:24px 0;border-radius:12px}}h2{{font-size:20px}}.images{{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:12px}}figure{{margin:0}}img{{width:100%;height:auto}}figcaption{{margin-bottom:8px}}.compare{{position:relative}}.overlay{{position:absolute;inset:0;opacity:.5}}details{{margin-top:16px}}label{{display:block;margin:16px 0}}@media(max-width:1000px){{.images{{grid-template-columns:1fr}}}}</style></head><body><h1>Editable PowerPoint fidelity review</h1><p>{html.escape(a.source_ref)}</p><p>{len(scored)} scored slides · Mean absolute RGB error: <b>{means['baseline']['mae']:.2f} → {means['candidate']['mae']:.2f}</b> · <b>{reduction:.1f}% reduction</b></p><p>All images are 1280×720. PowerPoint images come from Microsoft's native PNG export. MAE uses RGB channels on a 0–255 scale; changed fraction counts pixels with any channel differing by more than 20. This measures this fixture and renderer, not universal fidelity. Text and image edges are sensitive to font metrics, antialiasing and resampling.</p>{''.join(cards)}</body></html>''')
print(json.dumps({k: v for k, v in summary.items() if k != 'slides'}, indent=2))

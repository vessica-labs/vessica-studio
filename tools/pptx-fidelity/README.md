# Native PowerPoint fidelity loop

Keep presentation assets and generated evidence outside the repository. Freeze
the source revision, and run baseline and candidate engines against that same
content. Use the editable export route and record the exact engine commits.

1. Capture the engine's print HTML with `capture.mjs`. It needs Playwright and
   Chrome; optional `--playwright` and `--chrome` paths support bundled runtimes.
2. Open each exported PPTX in Microsoft PowerPoint. Record any repair prompt;
   repaired baselines should be clearly identified. Export every slide as PNG,
   width 1280 and height 720, using PowerPoint's native Export command. A
   third-party slide renderer is not interchangeable evidence.
3. Compare with Pillow and NumPy:

```sh
node tools/pptx-fidelity/capture.mjs --url http://127.0.0.1:4400/api/deck/example/print.html --output /tmp/pptx-review/html
python3 tools/pptx-fidelity/compare.py --reference /tmp/pptx-review/html --baseline /tmp/pptx-review/baseline --candidate /tmp/pptx-review/candidate --output /tmp/pptx-review/review --exclude-id 0055-interactive-demo --source-ref 'Frozen revision and engine commits'
```

The comparison rejects incorrect dimensions and missing slides. It produces an
offline gallery, an opacity overlay, amplified differences, and per-slide metrics.
Only explicitly excluded IDs are removed from aggregate scores. Inspect the
worst slides and also examine small text at full resolution. Re-export after
changes; do not treat an older render as proof of the newest candidate.

For videos, verify the packaged MP4 digest against the manifest and demonstrate
playback in native PowerPoint. Keep poster comparison and playback verification
separate. Test editable text and chart/path selections as well as image fidelity.

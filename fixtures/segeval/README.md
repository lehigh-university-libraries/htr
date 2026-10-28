# Synthetic segmentation fixtures

`page.xml`, `page.kraken.json`, `page.htr.json`, and `page.iiif.json` describe the
same invented two-line page. No customer content or model invocation is involved.

`cbad-reference.json` records outputs from the Transkribus v0.1.3 reference
executable in adaptive-tolerance mode (`java -jar ... GT.txt HYP.txt -no_s`).
Each baseline is written as `x,y;x,y;...` on one line. Values in the fixture are
the reference executable's four-decimal output; Go tests allow 1e-3 tolerance.
The reference source revision and invocation are stored with the fixture.

`mote-reference.json` records outputs from the pinned reference function
`mote_error._core_bbox.bbox_mote_components` with `GTBoxes`, sequential positive
SSU IDs, and the stored page dimensions. Boxes use `[x,y,width,height]`.
Only NumPy and the reference's `_core_bbox` and `types` modules are needed to
reproduce these numbers. Python/Java are not dependencies of htr or its tests.

`scribe/expected_*` are unchanged goldens from Scribe's
`internal/server/testdata/crosswalk`, repository revision
`2c94d5a7c953aa4cee206a6d27e56325330a6fec`. The hOCR fixture provides the canonical
box-only input for checking all four writers byte for byte.

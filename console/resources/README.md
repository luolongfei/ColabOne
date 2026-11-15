# Application artwork

`icon.svg` is the editable vector master, traced from the user's original portrait
(the raster artwork introduced in commit 6355f4c). It retains the hair, face,
hairpin, and clothing linework. The portrait sits on a white rounded tile in
both appearances, with no border. Only the exterior of the tile is transparent.
A white silhouette also remains beneath the ink. The original open shoulder ends are closed only in that white
underlay, at the original artwork baseline. No ink details were redrawn.

The source was traced at its original 1254 × 1254 resolution using Potrace 1.16,
threshold 128, turdsize 0, alphamax 1, and optimization tolerance 0.08. The SVG
contains vector paths and a rounded rectangle, with no embedded bitmap, fonts,
or external resources.

The interface and favicon use the SVG directly. `npm --prefix frontend run icons`
(or any frontend build) renders the transparent 1024 px `icon.png` with resvg.
MyGo creates macOS ICNS and Windows ICO assets from this PNG; the native tray
also uses it. Do not edit the generated PNG independently of the SVG.

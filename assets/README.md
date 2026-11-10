# Jobman Dashboard logos

The SVGs in this directory are ready to use in READMEs and other image
contexts. Their Audiowide subtitles are vector paths. The matching files in
`source/` are the original editable artwork with live text.

To update a logo, open its editable source in Inkscape with Audiowide Regular
installed. Confirm the subtitle font, select the subtitle, and choose
**Path → Object to Path** in the export copy. Save the copy as Plain SVG with
the same filename, page dimensions, and transparency.

The web and iPhone clients use copies of these outlined SVGs. Update each
copy from the corresponding export when changing the artwork.

| Export | Copies |
| --- | --- |
| `logo-dashboard.svg` | `web/public/logo.svg` |
| `logo-dark-dashboard.svg` | `web/public/logo-dark.svg` |
| `logo-transparent-dashboard.svg` | `web/public/logo-transparent-dashboard.svg`, `ios/App/Assets.xcassets/DashboardLogoDark.imageset/logo-transparent-dashboard.svg` |

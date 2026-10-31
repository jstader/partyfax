# Fonts

Every font here is served by the site itself (the pages' policy allows no
other), under the SIL Open Font License; each has its licence beside it.

| File | Font | Used by |
|---|---|---|
| `VT323.woff2` | VT323 | everything: the pixel font |
| `BebasNeue-Regular.ttf` | Bebas Neue | photobooth strip titles |
| `Pacifico-Regular.ttf` | Pacifico | photobooth strip titles |
| `NotoSansSC-fortunes.woff2` | Noto Sans SC, trimmed | fortune slips |

## The fortunes font

The full Noto Sans SC is 17 MB, so the fortunes site has a copy trimmed to
the characters its slips use. Those are listed in `NotoSansSC-fortunes.txt`,
and a test fails when a fortune or lesson in `fortunes/js/fortunes.js` uses
one that isn't there.

To add characters:

1. Add them to `NotoSansSC-fortunes.txt`. They're kept sorted.
2. Download `NotoSansSC[wght].ttf` from google/fonts (`ofl/notosanssc`; the
   copy here was built from commit `a85815a427`, 2026-03-25).
3. Rebuild the font with fontTools (`pip install fonttools brotli`). This
   keeps the weights from 500 to 700 and only the listed characters:

   ```bash
   fonttools varLib.instancer 'NotoSansSC[wght].ttf' wght=500:700 -o limited.ttf
   pyftsubset limited.ttf --text-file=NotoSansSC-fortunes.txt --flavor=woff2 \
     --layout-features='*' --no-hinting --desubroutinize \
     --output-file=NotoSansSC-fortunes.woff2
   ```

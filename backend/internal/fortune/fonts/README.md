# The slips' fonts

Embedded in the gateway, which composes the slips. All under the SIL Open
Font License, with the licences beside them.

| File | Font | Used for |
|---|---|---|
| `NotoSansSC-fortunes-500.ttf` | Noto Sans SC, weight 500, trimmed | most of the text |
| `NotoSansSC-fortunes-700.ttf` | Noto Sans SC, weight 700, trimmed | lucky numbers and meanings |
| `VT323.ttf` | VT323 | the small labels |

They're TTF, one file per weight, because Go's font package reads neither
WOFF2 nor variable fonts.

The full Noto Sans SC is 17 MB, so these copies are trimmed to the characters
the slips use, listed in `NotoSansSC-fortunes.txt`. A test fails when a fortune
or lesson in `../words.go` uses one that isn't there.

To add characters:

1. Add them to `NotoSansSC-fortunes.txt`. They're kept sorted.
2. Download `NotoSansSC[wght].ttf` from google/fonts (`ofl/notosanssc`; the
   copies here were built from commit `a85815a427`, 2026-03-25).
3. Rebuild each weight with fontTools (`pip install fonttools`):

   ```bash
   for w in 500 700; do
     fonttools varLib.instancer 'NotoSansSC[wght].ttf' wght=$w -o limited.ttf
     pyftsubset limited.ttf --text-file=NotoSansSC-fortunes.txt \
       --layout-features='*' --no-hinting --desubroutinize \
       --output-file=NotoSansSC-fortunes-$w.ttf
   done
   ```

`VT323.ttf` is `frontend/fonts/VT323.woff2` unpacked with fontTools.

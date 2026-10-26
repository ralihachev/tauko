# README images

`hero.png`, `answer.png`, `gen.png`, `add.png` and `demo.gif` are mockups of the real tauko pane and CLI output,
drawn in HTML with the pane's labels and colors. Regenerate after UI changes:

```sh
cd docs/img/src
C="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
for f in hero:1440,720 answer:816,640 gen:916,720 add:816,500; do n=${f%%:*}
  "$C" --headless=new --force-device-scale-factor=2 --window-size=${f#*:} \
    --default-background-color=0d1117ff --screenshot=../$n.png "file://$PWD/$n.html"
done
```

Then crop the empty background (any image tool). `web.png` is a real screenshot of the browser app.

`demo.gif` is `demo.html?f=0` to `?f=8`, one screenshot each (`--window-size=1236,660`), cropped
to one shared box and joined with ffmpeg, each frame shown for 0.8, 1, 1.6, 0.35, 0.35, 0.9, 2.2,
2.6 and 3.2 s:

```sh
ffmpeg -f concat -safe 0 -i frames.txt -loop 0 \
  -vf "scale=1100:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=128[p];[b][p]paletteuse=dither=none" ../demo.gif
```

where `frames.txt` lists `file 'f0.png'` / `duration 0.8` pairs in order.

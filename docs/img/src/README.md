# README images

`hero.png`, `answer.png` and `gen.png` are mockups of the real tauko pane and CLI output,
drawn in HTML with the pane's labels and colors. Regenerate after UI changes:

```sh
cd docs/img/src
C="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
for f in hero:1440,720 answer:816,640 gen:916,720; do n=${f%%:*}
  "$C" --headless=new --force-device-scale-factor=2 --window-size=${f#*:} \
    --default-background-color=0d1117ff --screenshot=../$n.png "file://$PWD/$n.html"
done
```

Then crop the empty background (any image tool). `web.png` is a real screenshot of the browser app.

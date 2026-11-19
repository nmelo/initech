#!/usr/bin/env python3
"""compose.py puts the rendered stapler on the icon's background and writes the app icon.

    python3 app/icon/compose.py <render dir> [out png]

<render dir> holds touch-icon.png and touch-icon.json from stapler.py. The background is near-black with a red glow
under the point where the stapler bites, and a faint light around the cursor. The output is 1024x1024 with no
transparency, which is what the App Store takes; by default it is the app's icon asset. Needs Pillow.
"""
import json, os, sys
from PIL import Image, ImageDraw, ImageFilter

S = 1024
HERE = os.path.dirname(os.path.abspath(__file__))
DEFAULT_OUT = os.path.join(HERE, "..", "InitechApp", "Assets.xcassets", "AppIcon.appiconset", "AppIcon-1024.png")

def gradient(top, bottom):
    im = Image.new("RGB", (S, S)); d = ImageDraw.Draw(im)
    for y in range(S):
        t = y / (S - 1)
        d.line((0, y, S, y), fill=tuple(round(top[i] + (bottom[i] - top[i]) * t) for i in range(3)))
    return im.convert("RGBA")

def glow(base, color, centre, rx, ry, strength, blur):
    layer = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    ImageDraw.Draw(layer).ellipse((centre[0] - rx, centre[1] - ry, centre[0] + rx, centre[1] + ry), fill=(*color, strength))
    out = base.copy(); out.alpha_composite(layer.filter(ImageFilter.GaussianBlur(blur)))
    return out

def main():
    if len(sys.argv) < 2:
        sys.exit("compose: usage: compose.py <render dir> [out png]")
    src, out = sys.argv[1], sys.argv[2] if len(sys.argv) > 2 else DEFAULT_OUT
    at = json.load(open(os.path.join(src, "touch-icon.json")))
    render = Image.open(os.path.join(src, "touch-icon.png")).convert("RGBA")
    if render.size != (S, S):
        sys.exit(f"compose: the render is {render.size}, want {S}x{S}")
    im = glow(gradient((30, 22, 22), (12, 10, 10)), (215, 42, 30), at["bite"], 400, 250, 185, 150)   # red, under the bite
    im = glow(im, (255, 255, 255), at["cursor"], 120, 40, 70, 30)                                    # the cursor's own light
    im.alpha_composite(render)
    im.convert("RGB").save(out)
    print("compose: wrote", os.path.normpath(out))

if __name__ == "__main__":
    main()

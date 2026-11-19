# The app icon

The icon is a rendered 3D model: a classic red desk stapler stapling a terminal prompt. The chevron and the cursor
are one white card on the desk; the cursor crosses the stapler's open jaws at a right angle, under the point where
the staple comes out. Everything is made here from source; no picture is drawn by hand.

- `stapler.py`: the model, the lights, the camera and the render (Blender 4.5 LTS, Cycles). It is all code: the
  stapler is built from side profiles, and each look is one entry in `VARIANTS`.
- `compose.py`: the background (near-black, a red glow under the bite, a faint light around the cursor) and the
  final 1024x1024 PNG with no transparency (Pillow).

## Making the icon again

```bash
blender -b --python app/icon/stapler.py -- /tmp/icon-render 384     # writes touch-icon.png and touch-icon.json
python3 app/icon/compose.py /tmp/icon-render                        # writes the AppIcon asset in the app
```

A render takes seconds on a GPU. On a machine without Blender, download the 4.5 LTS build from blender.org and
run it from its folder; nothing is installed. The shared Mac runs no render: use a machine with a GPU.

## Changing it

The icon is the `icon` entry of `VARIANTS` in `stapler.py`. What each setting does:

| Setting | What it is |
|---|---|
| `turn`, `lift` | where the camera stands: degrees round from nose-on, and degrees above the desk |
| `open_deg` | how far the jaws are open |
| `stapler_scale` | the stapler's size against the prompt |
| `cursor_len`, `cursor_h` | the cursor's length, and half its height |
| `bite_x` | how far along the stapler the cursor lies; 1.86 is exactly under the staple point |
| `bite_mid` | the stapler bites the middle of the cursor, not its end |
| `live` | the cursor gives off light |
| `fit_min_x` | how much of the stapler's back runs out of the picture |
| `PICK_DIST` | the camera's distance, which fixes the scale |

The other entries of `VARIANTS` are the studies the icon came from: the plain stapler from six angles, touches on
the model, and the prompt in other positions. `blender ... -- <out> 256 all` renders every one.

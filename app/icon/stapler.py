# stapler.py is the model behind the app's icon: a classic red desk stapler, built from scratch (red enamel body
# and base, chrome magazine, anvil and hinge pin, a rubber sole), with the terminal prompt as a card on the desk:
# a chevron, then a cursor that crosses the open jaws at a right angle, under the point where the staple comes out.
#
#   blender -b --python app/icon/stapler.py -- <out dir> [samples] [variants] [camera distance]
#
# With no variants it renders "icon", the one the app ships: <out>/touch-icon.png (1024x1024, transparent, the
# ground shadow in its alpha) and <out>/touch-icon.json (where the bite and the cursor are in the picture).
# compose.py puts that on the icon's background. "all", or a comma-separated list, renders the other variants in
# VARIANTS: the studies the icon came from (plain angles, touches, the prompt in other positions). They are kept
# so the icon can be taken somewhere else from here. Made with Blender 4.5 LTS; Cycles, on the GPU when there is one.
import bpy, bmesh, math, sys
from mathutils import Vector

argv = sys.argv[sys.argv.index("--") + 1:] if "--" in sys.argv else []
OUT = argv[0] if argv else "/tmp/stapler-out"
SAMPLES = int(argv[1]) if len(argv) > 1 else 256
UP = Vector((0.0, 0.0, 1.0))
TARGET = Vector((0.97, 0.0, 0.20))
PIVOT = (0.15, 0.275)
ARM_ORIGIN = (0.13, 0.03)

def bez(p0, p1, p2, p3, n=20):
    out = []
    for i in range(n + 1):
        t = i / n; u = 1 - t
        out.append((u**3 * p0[0] + 3*u*u*t * p1[0] + 3*u*t*t * p2[0] + t**3 * p3[0],
                    u**3 * p0[1] + 3*u*u*t * p1[1] + 3*u*t*t * p2[1] + t**3 * p3[1]))
    return out

ARM_TOP = bez((0.17, 0.335), (0.55, 0.405), (1.20, 0.335), (1.62, 0.275), 28)
ARM_PROFILE = bez((0.03, 0.0), (-0.03, 0.16), (0.02, 0.30), (0.17, 0.335), 14) + ARM_TOP + bez((1.62, 0.275), (1.85, 0.245), (1.93, 0.12), (1.885, 0.0), 18)
BASE_PROFILE = ([(-0.05, 0.0), (1.95, 0.0)] + bez((1.95, 0.0), (2.01, 0.02), (2.01, 0.08), (1.94, 0.10))
                + [(0.52, 0.10)] + bez((0.52, 0.10), (0.42, 0.10), (0.40, 0.235), (0.30, 0.24))
                + [(0.02, 0.24)] + bez((0.02, 0.24), (-0.07, 0.24), (-0.07, 0.08), (-0.05, 0.0)))

def top_z(x):
    """The height of the arm's top at x, in the arm's own drawing."""
    for (x0, z0), (x1, z1) in zip(ARM_TOP, ARM_TOP[1:]):
        if x0 <= x <= x1:
            return z0 + (z1 - z0) * (x - x0) / (x1 - x0)
    return ARM_TOP[-1][1]

def render(name, open_deg=5.5, turn=30, lift=16, margin=0.20, prompt=False, staples=False, floor=False, rim=95, glyphs=None, top=55, card=False, live=False, staple_in=False, gscale=1.0, fit_min_x=None, stapler_scale=1.0, cursor_len=1.02, bite_mid=False, bite_x=1.70, cam_dist=None, cursor_h=0.10):
    bpy.ops.wm.read_factory_settings(use_empty=True)
    scene = bpy.context.scene

    def material(mname, color, rough, metal=0.0, coat=0.0, glow=0.0):
        m = bpy.data.materials.new(mname); m.use_nodes = True
        b = m.node_tree.nodes["Principled BSDF"]
        def put(key, value):
            if key in b.inputs:
                b.inputs[key].default_value = value
        put("Base Color", (*color, 1.0)); put("Roughness", rough); put("Metallic", metal)
        put("Coat Weight", coat); put("Coat Roughness", 0.04)
        if glow:
            put("Emission Color", (*color, 1.0)); put("Emission Strength", glow)
        return m
    RED = material("red enamel", (0.60, 0.012, 0.010), 0.24, 0.0, 1.0)
    CHROME = material("chrome", (0.86, 0.86, 0.88), 0.10, 1.0)
    RUBBER = material("rubber", (0.012, 0.012, 0.014), 0.75)
    WHITE = material("white enamel", (0.95, 0.95, 0.95), 0.30, 0.0, 0.0, 1.4)

    def solid(sname, profile, width, mat, bevel=0.0, origin=(0.0, 0.0), segments=8):
        pts = []
        for p in profile:
            if not pts or (abs(p[0] - pts[-1][0]) + abs(p[1] - pts[-1][1])) > 1e-6:
                pts.append(p)
        if abs(pts[0][0] - pts[-1][0]) + abs(pts[0][1] - pts[-1][1]) < 1e-6:
            pts.pop()
        bm = bmesh.new()
        left = [bm.verts.new((x - origin[0], -width / 2, z - origin[1])) for x, z in pts]
        right = [bm.verts.new((x - origin[0], width / 2, z - origin[1])) for x, z in pts]
        bm.faces.new(left); bm.faces.new(list(reversed(right)))
        for i in range(len(pts)):
            j = (i + 1) % len(pts)
            bm.faces.new((left[j], left[i], right[i], right[j]))
        bmesh.ops.recalc_face_normals(bm, faces=bm.faces)
        mesh = bpy.data.meshes.new(sname); bm.to_mesh(mesh); bm.free()
        for poly in mesh.polygons:
            poly.use_smooth = True
        obj = bpy.data.objects.new(sname, mesh); scene.collection.objects.link(obj); obj.data.materials.append(mat)
        if bevel > 0:
            b = obj.modifiers.new("bevel", "BEVEL")
            b.width = bevel; b.segments = segments; b.limit_method = "ANGLE"; b.angle_limit = math.radians(35)
            obj.modifiers.new("normals", "WEIGHTED_NORMAL")
        return obj
    def box(bname, x0, x1, z0, z1, width, mat, bevel=0.0, origin=(0.0, 0.0)):
        return solid(bname, [(x0, z0), (x1, z0), (x1, z1), (x0, z1)], width, mat, bevel, origin, 4)

    base = solid("base", BASE_PROFILE, 0.50, RED, 0.035)
    sole = box("sole", -0.02, 1.93, -0.035, 0.0, 0.46, RUBBER, 0.012)
    anvil = box("anvil", 1.50, 1.90, 0.10, 0.118, 0.26, CHROME, 0.006)
    arm = solid("arm", ARM_PROFILE, 0.44, RED, 0.085, ARM_ORIGIN, 12)
    magazine = box("magazine", 0.28, 1.865, -0.075, 0.012, 0.30, CHROME, 0.010, ARM_ORIGIN)
    turning = [arm, magazine]
    if prompt:
        # A terminal prompt, a chevron and its cursor, laid on the arm's top as raised white enamel.
        shapes = [[(1.00, 0.10), (1.055, 0.10), (1.235, 0.0), (1.18, 0.0)],
                  [(1.18, 0.0), (1.235, 0.0), (1.055, -0.10), (1.00, -0.10)],
                  [(1.31, -0.10), (1.52, -0.10), (1.52, -0.05), (1.31, -0.05)]]
        bm = bmesh.new()
        for shape in shapes:
            lo = [bm.verts.new((x - ARM_ORIGIN[0], y, top_z(x) - ARM_ORIGIN[1] - 0.002)) for x, y in shape]
            hi = [bm.verts.new((x - ARM_ORIGIN[0], y, top_z(x) - ARM_ORIGIN[1] + 0.009)) for x, y in shape]
            bm.faces.new(hi); bm.faces.new(list(reversed(lo)))
            for i in range(len(shape)):
                j = (i + 1) % len(shape)
                bm.faces.new((lo[i], lo[j], hi[j], hi[i]))
        bmesh.ops.recalc_face_normals(bm, faces=bm.faces)
        mesh = bpy.data.meshes.new("prompt"); bm.to_mesh(mesh); bm.free()
        mark = bpy.data.objects.new("prompt", mesh); scene.collection.objects.link(mark); mark.data.materials.append(WHITE)
        turning.append(mark)
    for part in turning:
        part.location = (PIVOT[0], 0.0, PIVOT[1]); part.rotation_euler = (0.0, math.radians(-open_deg), 0.0)
    bpy.ops.mesh.primitive_cylinder_add(vertices=48, radius=0.036, depth=0.53, location=(PIVOT[0], 0.0, PIVOT[1]), rotation=(math.radians(90), 0.0, 0.0))
    pin = bpy.context.object; pin.data.materials.append(CHROME)
    for poly in pin.data.polygons:
        poly.use_smooth = True
    pin.modifiers.new("bevel", "BEVEL").width = 0.006
    parts = [base, sole, anvil, arm, magazine, pin]
    if stapler_scale != 1.0:       # the stapler grows about the point under its anvil, staying on the desk
        A = Vector((1.70, 0.0, -0.036))
        for o in parts:
            o.location = A + stapler_scale * (Vector(o.location) - A); o.scale = (stapler_scale,) * 3
    FLOOR = -0.036
    if staples:
        # A strip of staples and two loose ones on the desk, toward the camera.
        u = [(0, 0), (0.012, 0), (0.012, 0.058), (0.098, 0.058), (0.098, 0), (0.11, 0), (0.11, 0.07), (0, 0.07)]
        strip = solid("staple strip", u, 0.0105, CHROME)
        arr = strip.modifiers.new("strip", "ARRAY"); arr.count = 40
        arr.use_relative_offset = False; arr.use_constant_offset = True; arr.constant_offset_displace = (0.0, 0.0125, 0.0)
        strip.location = (1.02, -0.92, FLOOR); strip.rotation_euler = (0.0, 0.0, math.radians(-62))
        for i, (x, y, rot) in enumerate([(1.72, -0.60, 20), (0.52, -0.80, 115)]):
            one = solid(f"staple {i}", u, 0.0105, CHROME)
            one.location = (x, y, FLOOR + 0.006); one.rotation_euler = (math.radians(90), 0.0, math.radians(rot))
            parts.append(one)
        parts.append(strip)

    if glyphs:
        # The terminal prompt as things on the desk, read from the camera: a chevron, then its cursor.
        # u runs along the line of text and v up the page, both laid on the desk square to the camera,
        # so the chevron is not skewed: u is the camera's right, v is away from it.
        PAPER = material("white card", (0.93, 0.93, 0.93), 0.45, 0.0, 0.0, 0.12)
        ta = math.radians(turn)
        RU, AV = (math.sin(ta), math.cos(ta)), (-math.cos(ta), math.sin(ta))
        def slab(gname, shape, at, z0, z1, mat, bevel=0.018):
            place = lambda u, v, z: (at[0] + u * RU[0] + v * AV[0], at[1] + u * RU[1] + v * AV[1], z)
            bm = bmesh.new()
            lo = [bm.verts.new(place(u, v, z0)) for u, v in shape]
            hi = [bm.verts.new(place(u, v, z1)) for u, v in shape]
            bm.faces.new(hi); bm.faces.new(list(reversed(lo)))
            for i in range(len(shape)):
                j = (i + 1) % len(shape)
                bm.faces.new((lo[i], lo[j], hi[j], hi[i]))
            bmesh.ops.recalc_face_normals(bm, faces=bm.faces)
            mesh = bpy.data.meshes.new(gname); bm.to_mesh(mesh); bm.free()
            for poly in mesh.polygons:
                poly.use_smooth = True
            obj = bpy.data.objects.new(gname, mesh); scene.collection.objects.link(obj); obj.data.materials.append(mat)
            b = obj.modifiers.new("bevel", "BEVEL"); b.width = bevel; b.segments = 5; b.limit_method = "ANGLE"
            obj.modifiers.new("normals", "WEIGHTED_NORMAL")
            parts.append(obj)
        w = 0.19
        upper = [(-1.20, 0.42), (-1.20 + w, 0.42), (-0.56 + w, 0.0), (-0.56, 0.0)]
        lower = [(-0.56, 0.0), (-0.56 + w, 0.0), (-1.20 + w, -0.42), (-1.20, -0.42)]
        marks = {}
        if glyphs == "bite":    # the stapler comes in from one side and bites the END of the cursor; the prompt stays in view
            k = gscale
            u0, u1, ub, vc = -0.45 * k, 0.55 * k, 0.33 * k, -0.30 * k
            at = (1.70 - ub * RU[0] - vc * AV[0], 0.0 - ub * RU[1] - vc * AV[1])   # the bite on the anvil
            cz = (0.118, 0.150) if card else (FLOOR, 0.150)             # a card rests on the base; a block stands on the desk
            hz = (FLOOR, FLOOR + 0.034) if card else (FLOOR, 0.150)
            cur = material("live cursor", (0.95, 0.95, 0.95), 0.40, 0.0, 0.0, 4.0) if live else PAPER
            slab("chevron a", [(u * k, v * k) for u, v in upper], at, hz[0], hz[1], PAPER)
            slab("chevron b", [(u * k, v * k) for u, v in lower], at, hz[0], hz[1], PAPER)
            slab("cursor", [(u0, vc - 0.10 * k), (u1, vc - 0.10 * k), (u1, vc + 0.10 * k), (u0, vc + 0.10 * k)], at, cz[0], cz[1], cur)
            if staple_in:       # a staple already in the cursor, beside the nose
                slab("staple in", [(-0.20 * k, vc - 0.014), (-0.02 * k, vc - 0.014), (-0.02 * k, vc + 0.014), (-0.20 * k, vc + 0.014)], at, cz[1], cz[1] + 0.012, CHROME, 0.004)
            mid = (u0 + 0.05 * k) / 2
            marks = {"bite": (1.70, 0.0, 0.15), "cursor": (at[0] + mid * RU[0] + vc * AV[0], at[1] + mid * RU[1] + vc * AV[1], cz[1])}
        elif glyphs == "inline":
            # The prompt lies in line with the stapler, the way a sheet goes into one: the chevron out in front, then
            # the cursor, which runs straight into the jaws along the stapler's own axis. It is one stiff card: its far
            # end rests on the desk and its near end sits on the anvil, so nothing passes through the stapler.
            # u runs along the reading (toward the stapler). v, up the page, is laid on the desk at the slant that makes
            # the mark look square in the picture: a true ">_" turned to the stapler's angle, not a skewed one.
            sl = math.sin(math.radians(lift))
            wr, wa = -math.cos(ta) * sl, -math.sin(ta) / sl                    # v, as (camera's right, away from camera)
            W = (wr * RU[0] + wa * AV[0], wr * RU[1] + wa * AV[1])
            t, vc, u_end = 0.06, -0.26, 0.72 + 1.05                            # thickness; the cursor's centre line; its end in the jaws
            P0 = (1.62 + u_end - vc * W[0], -vc * W[1])                        # the cursor's centre line runs down the stapler's middle
            slope = (0.118 - FLOOR) / (P0[0] + 0.3 - 1.90)
            zf = lambda x: max(FLOOR, 0.118 - (x - 1.90) * slope)              # rests on the anvil's front edge
            cur = material("live cursor", (0.95, 0.95, 0.95), 0.40, 0.0, 0.0, 4.0) if live else PAPER
            def piece(gname, shape, mat):
                pts = [(P0[0] - u + v * W[0], P0[1] + v * W[1]) for u, v in shape]
                bm = bmesh.new()
                lo = [bm.verts.new((x, y, zf(x))) for x, y in pts]
                hi = [bm.verts.new((x, y, zf(x) + t)) for x, y in pts]
                bm.faces.new(hi); bm.faces.new(list(reversed(lo)))
                for i in range(len(pts)):
                    j = (i + 1) % len(pts)
                    bm.faces.new((lo[i], lo[j], hi[j], hi[i]))
                bmesh.ops.recalc_face_normals(bm, faces=bm.faces)
                mesh = bpy.data.meshes.new(gname); bm.to_mesh(mesh); bm.free()
                for poly in mesh.polygons:
                    poly.use_smooth = True
                obj = bpy.data.objects.new(gname, mesh); scene.collection.objects.link(obj); obj.data.materials.append(mat)
                b = obj.modifiers.new("bevel", "BEVEL"); b.width = 0.016; b.segments = 5; b.limit_method = "ANGLE"
                obj.modifiers.new("normals", "WEIGHTED_NORMAL")
                parts.append(obj)
            piece("chevron a", [(0.0, 0.36), (0.21, 0.36), (0.62, 0.0), (0.41, 0.0)], PAPER)
            piece("chevron b", [(0.41, 0.0), (0.62, 0.0), (0.21, -0.36), (0.0, -0.36)], PAPER)
            piece("cursor", [(0.72, vc - 0.10), (u_end, vc - 0.10), (u_end, vc + 0.10), (0.72, vc + 0.10)], cur)
            mid = P0[0] - (0.72 + 0.72 + 0.62) / 2 + vc * W[0]
            marks = {"bite": (1.95, 0.0, 0.19), "cursor": (mid, 0.0, zf(mid) + t)}
        elif glyphs == "cross":
            # The cursor crosses the jaws at a true right angle to the stapler, the way a strip is stapled, and its end
            # stops under the nose. The prompt is one stiff card: flat on the anvil, then sloping down to rest on the
            # desk at the chevron, so nothing passes through the stapler. u runs along the reading (the world's +y);
            # v, up the page, is laid on the desk at the slant that makes the mark look square in the picture.
            sl = math.sin(math.radians(lift))
            rr, ra = RU[1], AV[1]                                   # the reading direction, as (camera's right, away)
            wr, wa = -ra * sl, rr / sl
            W = (wr * RU[0] + wa * AV[0], wr * RU[1] + wa * AV[1])
            k = gscale
            t, vc, u0, u_end = 0.06, -0.26 * k, 0.72 * k, 0.72 * k + cursor_len
            za, hw = FLOOR + stapler_scale * (0.118 - FLOOR), 0.13 * stapler_scale     # the anvil's top, and half its width
            if bite_mid:   # the stapler bites the middle of the cursor, which sticks out on both sides
                P0 = (bite_x - vc * W[0], -(u0 + u_end) / 2 - vc * W[1])
            else:          # the cursor's end is under the nose
                P0 = (1.70 - vc * W[0], 0.16 - u_end - vc * W[1])
            far = P0[1] - 0.36 * k * abs(W[1])
            slope = (za - FLOOR) / (-hw - far)
            zf = lambda y: max(FLOOR, za - max(0.0, -hw - y) * slope)
            cur = material("live cursor", (0.95, 0.95, 0.95), 0.40, 0.0, 0.0, 4.0) if live else PAPER
            def piece(gname, shape, mat):
                pts = [(P0[0] + v * W[0], P0[1] + u + v * W[1]) for u, v in shape]
                bm = bmesh.new()
                lo = [bm.verts.new((x, y, zf(y))) for x, y in pts]
                hi = [bm.verts.new((x, y, zf(y) + t)) for x, y in pts]
                bm.faces.new(hi); bm.faces.new(list(reversed(lo)))
                for i in range(len(pts)):
                    j = (i + 1) % len(pts)
                    bm.faces.new((lo[i], lo[j], hi[j], hi[i]))
                bmesh.ops.recalc_face_normals(bm, faces=bm.faces)
                mesh = bpy.data.meshes.new(gname); bm.to_mesh(mesh); bm.free()
                for poly in mesh.polygons:
                    poly.use_smooth = True
                obj = bpy.data.objects.new(gname, mesh); scene.collection.objects.link(obj); obj.data.materials.append(mat)
                b = obj.modifiers.new("bevel", "BEVEL"); b.width = 0.016; b.segments = 5; b.limit_method = "ANGLE"
                obj.modifiers.new("normals", "WEIGHTED_NORMAL")
                parts.append(obj)
            piece("chevron a", [(0.0, 0.36 * k), (0.21 * k, 0.36 * k), (0.62 * k, 0.0), (0.41 * k, 0.0)], PAPER)
            piece("chevron b", [(0.41 * k, 0.0), (0.62 * k, 0.0), (0.21 * k, -0.36 * k), (0.0, -0.36 * k)], PAPER)
            # The cursor is one strip with stations along its length, so that it follows the slope without seams.
            n = 16
            bm = bmesh.new(); ring = []
            for i in range(n + 1):
                u = u0 + (u_end - u0) * i / n
                row = []
                for v in (vc - cursor_h * k, vc + cursor_h * k):
                    x, y = P0[0] + v * W[0], P0[1] + u + v * W[1]
                    row += [bm.verts.new((x, y, zf(y))), bm.verts.new((x, y, zf(y) + t))]
                ring.append(row)   # low and high on one edge, then low and high on the other
            for i in range(n):
                p, q = ring[i], ring[i + 1]
                bm.faces.new((p[1], q[1], q[3], p[3])); bm.faces.new((p[0], p[2], q[2], q[0]))
                bm.faces.new((p[0], q[0], q[1], p[1])); bm.faces.new((p[2], p[3], q[3], q[2]))
            bm.faces.new((ring[0][0], ring[0][1], ring[0][3], ring[0][2])); bm.faces.new((ring[n][0], ring[n][2], ring[n][3], ring[n][1]))
            bmesh.ops.recalc_face_normals(bm, faces=bm.faces)
            mesh = bpy.data.meshes.new("cursor"); bm.to_mesh(mesh); bm.free()
            for poly in mesh.polygons:
                poly.use_smooth = True
            strip = bpy.data.objects.new("cursor", mesh); scene.collection.objects.link(strip); strip.data.materials.append(cur)
            bv = strip.modifiers.new("bevel", "BEVEL"); bv.width = 0.016; bv.segments = 5; bv.limit_method = "ANGLE"; bv.angle_limit = math.radians(35)
            strip.modifiers.new("normals", "WEIGHTED_NORMAL")
            parts.append(strip)
            seen = (P0[1] + u0 + vc * W[1] - 0.25 * stapler_scale) / 2
            marks = {"bite": (bite_x, 0.0, za + 0.07), "cursor": (bite_x, seen, zf(seen) + t)}
        elif glyphs == "clamp":   # the stapler bites the cursor: the bar runs across the base, under the nose
            top = 0.150
            at = (1.70 - 0.03 * RU[0] + 0.30 * AV[0], 0.0 - 0.03 * RU[1] + 0.30 * AV[1])   # the cursor's middle on the anvil
            slab("chevron a", upper, at, FLOOR, top, PAPER); slab("chevron b", lower, at, FLOOR, top, PAPER)
            slab("cursor", [(-0.40, -0.40), (0.46, -0.40), (0.46, -0.20), (-0.40, -0.20)], at, FLOOR, top, PAPER)
        else:                   # the cursor is a staple, just driven in, in front of the nose
            mid = (2.0 + 0.45 * math.cos(ta), -0.45 * math.sin(ta))   # a little in front of the nose, toward the camera
            at = (mid[0] - 0.03 * RU[0] + 0.375 * AV[0], mid[1] - 0.03 * RU[1] + 0.375 * AV[1])
            slab("chevron a", upper, at, FLOOR, FLOOR + 0.07, PAPER); slab("chevron b", lower, at, FLOOR, FLOOR + 0.07, PAPER)
            slab("staple", [(-0.38, -0.42), (0.44, -0.42), (0.44, -0.33), (-0.38, -0.33)], at, FLOOR, FLOOR + 0.06, CHROME, 0.025)

    bpy.ops.mesh.primitive_plane_add(size=60, location=(1.0, 0.0, FLOOR))
    ground = bpy.context.object
    if floor:
        ground.data.materials.append(material("polished desk", (0.035, 0.035, 0.04), 0.34, 1.0))
    else:
        ground.is_shadow_catcher = True

    world = bpy.data.worlds.new("studio"); world.use_nodes = True
    world.node_tree.nodes["Background"].inputs[0].default_value = (0.92, 0.93, 0.96, 1.0)
    world.node_tree.nodes["Background"].inputs[1].default_value = 0.16
    scene.world = world
    cam_data = bpy.data.cameras.new("camera"); cam_data.lens = 85
    cam = bpy.data.objects.new("camera", cam_data); scene.collection.objects.link(cam); scene.camera = cam
    cam.rotation_mode = "QUATERNION"
    def softbox(pos, size, power, seen=True):
        data = bpy.data.lights.new("softbox", "AREA"); data.shape = "SQUARE"; data.size = size; data.energy = power
        obj = bpy.data.objects.new("softbox", data); scene.collection.objects.link(obj)
        obj.location = pos; obj.rotation_mode = "QUATERNION"
        obj.rotation_quaternion = (TARGET - obj.location).to_track_quat("-Z", "Y")
        return obj

    scene.render.engine = "CYCLES"; scene.cycles.samples = SAMPLES; scene.cycles.use_denoising = True
    scene.render.film_transparent = True
    scene.render.resolution_x = scene.render.resolution_y = 1024
    scene.render.image_settings.file_format = "PNG"; scene.render.image_settings.color_mode = "RGBA"
    scene.view_settings.view_transform = "Standard"
    try:
        scene.view_settings.look = "Medium High Contrast"
    except Exception:
        pass
    prefs = bpy.context.preferences.addons["cycles"].preferences
    for kind in ("OPTIX", "CUDA"):
        try:
            prefs.compute_device_type = kind; prefs.get_devices()
            if any(d.type == kind for d in prefs.devices):
                for d in prefs.devices:
                    d.use = d.type == kind
                scene.cycles.device = "GPU"
                break
        except Exception as e:
            print("touches: no", kind, e)

    a, e = math.radians(turn), math.radians(lift)
    back = Vector((math.cos(e) * math.cos(a), -math.cos(e) * math.sin(a), math.sin(e)))
    side = UP.cross(back).normalized()   # to the camera's right
    cam.location = TARGET + back * 9.0
    cam.rotation_quaternion = (-back).to_track_quat("-Z", "Y")
    bpy.context.view_layer.update()
    corners = [o.matrix_world @ Vector(corner) for o in parts for corner in o.bound_box]
    if fit_min_x is not None:      # frame the front of the stapler only: its back runs out of the picture
        corners = [Vector((max(c.x, fit_min_x), c.y, c.z)) for c in corners]
    coords = [c for corner in corners for c in corner]
    loc, _ = cam.camera_fit_coords(bpy.context.evaluated_depsgraph_get(), coords)
    cam.location = Vector(loc) + back * (margin * (Vector(loc) - TARGET).length)
    if cam_dist is not None:       # the fit centres the picture; a given distance fixes its scale
        cam.location = cam.location + back * (cam_dist - (cam.location - TARGET).dot(back))
    print("touches: distance", name, round((cam.location - TARGET).dot(back), 4))
    softbox(TARGET + back * 2.6 - side * 2.2 + UP * 3.0, 3.0, 150)   # key, high and to the left
    softbox(TARGET + back * 2.4 + side * 3.0 + UP * 0.9, 3.5, 40)    # fill, low and to the right
    softbox(TARGET - back * 3.0 - side * 1.2 + UP * 2.6, 2.5, rim)   # rim, from behind
    softbox(TARGET + UP * 3.6, 5.0, top)                             # top, the long highlight
    if glyphs in ("bite", "inline", "cross"):
        # Where the bite and the cursor land in the picture, for the glow drawn behind them afterwards.
        import json
        from bpy_extras.object_utils import world_to_camera_view
        bpy.context.view_layer.update()
        where = {}
        for key, point in marks.items():
            c = world_to_camera_view(scene, cam, Vector(point))
            where[key] = [round(c.x * 1024), round((1 - c.y) * 1024)]
        open(f"{OUT}/touch-{name}.json", "w").write(json.dumps(where))
    scene.render.filepath = f"{OUT}/touch-{name}.png"
    bpy.ops.render.render(write_still=True)
    print("touches: wrote", scene.render.filepath)

ONLY = None if len(argv) > 2 and argv[2] == "all" else (argv[2].split(",") if len(argv) > 2 else ["icon"])
# The icon's camera distance. It fixes the icon's scale: the automatic fit would zoom in when the cursor gets shorter.
PICK_DIST = float(argv[3]) if len(argv) > 3 else 5.2525
VARIANTS = {
    "plain": dict(),
    "rim": dict(rim=420),
    "prompt": dict(prompt=True, lift=30),            # higher, so the top of the arm shows
    "staples": dict(staples=True),
    "open": dict(open_deg=16, lift=14),
    "floor": dict(floor=True),
    # The stapler and the terminal prompt together.
    "cursor": dict(turn=62, lift=10),                                   # the stapler stands where the cursor would (the chevron is drawn in 2D)
    "clamp": dict(glyphs="clamp", open_deg=-0.5, lift=40, margin=0.12),  # stapling the prompt
    "staple": dict(glyphs="staple", open_deg=12, lift=38, margin=0.12),  # the staple is the cursor
    # Refining "stapling the prompt": the stapler comes in from the upper right and bites the end of the cursor;
    # the light from behind is turned down so the arm's top stays red.
    "bite-a": dict(glyphs="bite", turn=-30, lift=40, margin=0.12, open_deg=-0.5, rim=18, top=38),
    "bite-b": dict(glyphs="bite", turn=-30, lift=40, margin=0.12, open_deg=-0.5, rim=18, top=38, live=True),
    "bite-c": dict(glyphs="bite", turn=-30, lift=40, margin=0.12, open_deg=-0.5, rim=18, top=38, card=True, staple_in=True),
    "bite-d": dict(glyphs="bite", turn=-30, lift=40, margin=0.12, open_deg=7, rim=18, top=38, gscale=1.3),
    # A bigger stapler, even if it runs out of the square; the prompt never passes through the stapler; the cursor
    # crosses the jaws at a right angle, the way a strip is stapled.
    "cross-a": dict(glyphs="cross", turn=-20, lift=40, margin=0.0, open_deg=0.6, rim=18, top=38, live=True, fit_min_x=0.6),
    "cross-b": dict(glyphs="cross", turn=-28, lift=42, margin=0.0, open_deg=0.6, rim=18, top=38, live=True, fit_min_x=0.6),
    "cross-c": dict(glyphs="cross", turn=-14, lift=36, margin=0.0, open_deg=0.6, rim=18, top=38, live=True, fit_min_x=0.6),
    "cross-d": dict(glyphs="cross", turn=-20, lift=40, margin=0.0, open_deg=0.6, rim=18, top=38, live=True, fit_min_x=1.0, gscale=0.85),
    # Turned 28 degrees, the stapler 10% larger, biting the middle of the cursor.
    "pick-1": dict(glyphs="cross", turn=-28, lift=42, margin=0.09, open_deg=0.6, rim=18, top=38, live=True, fit_min_x=0.6,
                   stapler_scale=1.1, cursor_len=1.3, bite_mid=True),
    # A shorter cursor, moved under the point where the staple comes out: the staple leaves the magazine near its
    # front end, at x = 1.86 on this model. The camera keeps pick-1's distance (PICK_DIST), so nothing changes size.
    "pick": dict(glyphs="cross", turn=-28, lift=42, margin=0.09, open_deg=0.6, rim=18, top=38, live=True, fit_min_x=0.6,
                 stapler_scale=1.1, cursor_len=1.0, bite_mid=True, bite_x=1.86, cam_dist=PICK_DIST),
    # With the cursor under the staple point it covered the chrome. The jaws open so the
    # chrome magazine shows above the cursor and the anvil behind it; in b the cursor is also a little slimmer.
    "pick-a": dict(glyphs="cross", turn=-28, lift=42, margin=0.09, open_deg=9, rim=18, top=38, live=True, fit_min_x=0.6,
                   stapler_scale=1.1, cursor_len=1.0, bite_mid=True, bite_x=1.86, cam_dist=PICK_DIST),
    "pick-b": dict(glyphs="cross", turn=-28, lift=42, margin=0.09, open_deg=5, rim=18, top=38, live=True, fit_min_x=0.6,
                   stapler_scale=1.1, cursor_len=1.0, bite_mid=True, bite_x=1.86, cam_dist=PICK_DIST, cursor_h=0.07),
    # Seen from this camera, the raised chrome tip lines up with a point about 0.2 further back on the desk, so the
    # cursor sits there: in the picture the staple point is over the cursor's middle, and the anvil's chrome shows in front.
    "pick-c": dict(glyphs="cross", turn=-28, lift=42, margin=0.09, open_deg=5, rim=18, top=38, live=True, fit_min_x=0.6,
                   stapler_scale=1.1, cursor_len=1.0, bite_mid=True, bite_x=1.68, cam_dist=PICK_DIST),
    # THE ICON.
    "icon": dict(glyphs="cross", turn=-28, lift=42, margin=0.09, open_deg=3.5, rim=18, top=38, live=True, fit_min_x=0.6,
                   stapler_scale=1.1, cursor_len=1.0, bite_mid=True, bite_x=1.73, cam_dist=PICK_DIST),
    # The same idea with the prompt in line with the stapler: readable only when the view is nearly side on.
    "inline-a": dict(glyphs="inline", turn=-72, lift=32, margin=0.0, open_deg=1.0, rim=18, top=38, live=True, fit_min_x=0.9),
}
for vname, opts in VARIANTS.items():
    if ONLY is None or vname in ONLY:
        render(vname, **opts)

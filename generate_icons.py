import os
import base64
from io import BytesIO
from PIL import Image, ImageDraw
import numpy as np

def make_squircle(img, radius_ratio=0.20):
    w, h = img.size
    radius = int(min(w, h) * radius_ratio)
    scale = 4
    mask = Image.new("L", (w * scale, h * scale), 0)
    draw = ImageDraw.Draw(mask)
    draw.rounded_rectangle([0, 0, w * scale - 1, h * scale - 1], radius=radius * scale, fill=255)
    mask = mask.resize((w, h), Image.Resampling.LANCZOS)
    out = img.copy()
    out.putalpha(mask)
    return out

def make_circle(img):
    w, h = img.size
    scale = 4
    mask = Image.new("L", (w * scale, h * scale), 0)
    draw = ImageDraw.Draw(mask)
    draw.ellipse([0, 0, w * scale - 1, h * scale - 1], fill=255)
    mask = mask.resize((w, h), Image.Resampling.LANCZOS)
    out = img.copy()
    out.putalpha(mask)
    return out

def make_adaptive_fg(img, target_size):
    scale_dim = int(target_size * 0.78)
    resized = img.resize((scale_dim, scale_dim), Image.Resampling.LANCZOS)
    fg = Image.new("RGBA", (target_size, target_size), (0, 0, 0, 0))
    offset = (target_size - scale_dim) // 2
    fg.paste(resized, (offset, offset))
    return fg

def main():
    base_dir = os.path.dirname(os.path.abspath(__file__))
    master_candidates = [
        os.path.expanduser(r"~\Downloads\bigpocketlogo.png"),
        os.path.join(base_dir, "bigpocket.png"),
        os.path.join(base_dir, "desktop", "logo.png"),
    ]
    src_path = None
    for cand in master_candidates:
        if os.path.exists(cand):
            src_path = cand
            break

    if not src_path:
        raise FileNotFoundError("Master logo not found")

    print(f"Loading master logo from {src_path}...")
    orig = Image.open(src_path).convert("RGBA")

    # Center crop to remove dead outer dark borders (content is 366x301)
    cx, cy = 266, 255
    half = 190
    x0 = max(0, cx - half)
    y0 = max(0, cy - half)
    x1 = min(orig.width, cx + half)
    y1 = min(orig.height, cy + half)
    tight_square = orig.crop((x0, y0, x1, y1))
    master_zoomed = tight_square.resize((512, 512), Image.Resampling.LANCZOS)

    master_squircle = make_squircle(master_zoomed, 0.20)
    master_circle = make_circle(master_zoomed)

    desktop_dir = os.path.join(base_dir, "desktop")
    android_res = os.path.join(base_dir, "android", "app", "src", "main", "res")

    # 1. Root & desktop PNG
    master_squircle.save(os.path.join(base_dir, "bigpocket.png"), "PNG")
    master_squircle.save(os.path.join(desktop_dir, "logo.png"), "PNG")
    master_squircle.save(os.path.join(desktop_dir, "frontend", "logo.png"), "PNG")

    # 2. Desktop ICO
    ico_sizes = [16, 24, 32, 48, 64, 128, 256]
    ico_images = [master_squircle.resize((s, s), Image.Resampling.LANCZOS) for s in ico_sizes]
    ico_images[-1].save(
        os.path.join(desktop_dir, "logo.ico"),
        format="ICO",
        sizes=[(s, s) for s in ico_sizes],
        append_images=ico_images[:-1]
    )

    # 3. Desktop SVG
    buf = BytesIO()
    master_squircle.save(buf, format="PNG", optimize=True)
    b64_logo = base64.b64encode(buf.getvalue()).decode("ascii")
    svg_code = f'''<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 512 512" width="100%" height="100%">
  <defs>
    <clipPath id="squircle">
      <rect x="0" y="0" width="512" height="512" rx="100" ry="100" />
    </clipPath>
  </defs>
  <rect x="0" y="0" width="512" height="512" rx="100" ry="100" fill="#131313"/>
  <image href="data:image/png;base64,{b64_logo}" width="512" height="512" clip-path="url(#squircle)" />
</svg>'''
    with open(os.path.join(desktop_dir, "frontend", "logo.svg"), "w", encoding="utf-8") as f:
        f.write(svg_code)

    # 4. Tauri icons
    tauri_icons_dir = os.path.join(desktop_dir, "src-tauri", "icons")
    os.makedirs(tauri_icons_dir, exist_ok=True)
    master_squircle.save(os.path.join(tauri_icons_dir, "icon.png"), "PNG")
    master_squircle.resize((32, 32), Image.Resampling.LANCZOS).save(os.path.join(tauri_icons_dir, "32x32.png"), "PNG")
    master_squircle.resize((128, 128), Image.Resampling.LANCZOS).save(os.path.join(tauri_icons_dir, "128x128.png"), "PNG")
    master_squircle.resize((256, 256), Image.Resampling.LANCZOS).save(os.path.join(tauri_icons_dir, "128x128@2x.png"), "PNG")
    ico_images[-1].save(
        os.path.join(tauri_icons_dir, "icon.ico"),
        format="ICO",
        sizes=[(s, s) for s in ico_sizes],
        append_images=ico_images[:-1]
    )
    master_squircle.save(os.path.join(tauri_icons_dir, "icon.icns"), format="ICNS")

    # 5. Desktop winres
    winres_dir = os.path.join(desktop_dir, "winres")
    if os.path.exists(winres_dir):
        master_squircle.resize((256, 256), Image.Resampling.LANCZOS).save(os.path.join(winres_dir, "icon.png"), "PNG")
        master_squircle.resize((16, 16), Image.Resampling.LANCZOS).save(os.path.join(winres_dir, "icon16.png"), "PNG")

    # 6. Android assets
    drawable_dir = os.path.join(android_res, "drawable")
    os.makedirs(drawable_dir, exist_ok=True)
    master_squircle.save(os.path.join(drawable_dir, "logo.png"), "PNG")

    mipmap_specs = {
        "mipmap-mdpi": (48, 108),
        "mipmap-hdpi": (72, 162),
        "mipmap-xhdpi": (96, 216),
        "mipmap-xxhdpi": (144, 324),
        "mipmap-xxxhdpi": (192, 432),
    }

    for folder, (icon_dim, fg_dim) in mipmap_specs.items():
        folder_path = os.path.join(android_res, folder)
        os.makedirs(folder_path, exist_ok=True)
        launcher_img = master_squircle.resize((icon_dim, icon_dim), Image.Resampling.LANCZOS)
        launcher_img.save(os.path.join(folder_path, "ic_launcher.png"), "PNG")
        round_img = master_circle.resize((icon_dim, icon_dim), Image.Resampling.LANCZOS)
        round_img.save(os.path.join(folder_path, "ic_launcher_round.png"), "PNG")
        fg_img = make_adaptive_fg(master_zoomed, fg_dim)
        fg_img.save(os.path.join(folder_path, "ic_launcher_foreground.png"), "PNG")

    print("All zoomed logos and icons refreshed across PC and Android!")

if __name__ == "__main__":
    main()

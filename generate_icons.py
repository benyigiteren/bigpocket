import os
import math
from PIL import Image, ImageDraw

def render_logo(size=512):
    # Render at 4x supersampling for extreme sharpness
    scale = 4
    canvas_size = size * scale
    img = Image.new("RGBA", (canvas_size, canvas_size), (0, 0, 0, 0))
    draw = ImageDraw.Draw(img)

    def s(val):
        return val * canvas_size / 128.0

    # 1. Background Squircle
    # Background gradient
    bg_pad = s(4)
    rx = s(28)
    draw.rounded_rectangle(
        [bg_pad, bg_pad, canvas_size - bg_pad, canvas_size - bg_pad],
        radius=int(rx),
        fill=(14, 14, 18, 255),
        outline=(42, 42, 52, 255),
        width=int(s(2.5))
    )

    # 2. Phone sliding out of pocket (Upper Device)
    phone_rect = [s(44), s(20), s(84), s(68)]
    phone_rx = int(s(8))
    # Fill phone with gradient violet/indigo
    draw.rounded_rectangle(
        phone_rect,
        radius=phone_rx,
        fill=(129, 140, 248, 255), # Electric Indigo
        outline=(255, 255, 255, 255),
        width=int(s(2.5))
    )

    # Phone Speaker / Camera Notch
    draw.rounded_rectangle(
        [s(56), s(26), s(72), s(29)],
        radius=int(s(1.5)),
        fill=(255, 255, 255, 230)
    )

    # 3. Pocket Body (Lower Silhouette)
    # Pocket polygon / arc
    # M34 48 L34 70 C34 90, 48 100, 64 100 C80 100, 94 90, 94 70 L94 48 Z
    pocket_points = []
    # Left edge
    pocket_points.append((s(34), s(48)))
    pocket_points.append((s(34), s(70)))
    # Bottom curve: 34,70 to 64,100
    steps = 40
    for i in range(steps + 1):
        t = i / float(steps)
        # Cubic bezier: P0=(34,70), P1=(34,90), P2=(48,100), P3=(64,100)
        bx = (1-t)**3 * 34 + 3*(1-t)**2*t * 34 + 3*(1-t)*t**2 * 48 + t**3 * 64
        by = (1-t)**3 * 70 + 3*(1-t)**2*t * 90 + 3*(1-t)*t**2 * 100 + t**3 * 100
        pocket_points.append((s(bx), s(by)))
    for i in range(1, steps + 1):
        t = i / float(steps)
        # Cubic bezier: P0=(64,100), P1=(80,100), P2=(94,90), P3=(94,70)
        bx = (1-t)**3 * 64 + 3*(1-t)**2*t * 80 + 3*(1-t)*t**2 * 94 + t**3 * 94
        by = (1-t)**3 * 100 + 3*(1-t)**2*t * 100 + 3*(1-t)*t**2 * 90 + t**3 * 70
        pocket_points.append((s(bx), s(by)))
    pocket_points.append((s(94), s(48)))

    # Fill pocket interior
    draw.polygon(pocket_points, fill=(24, 24, 32, 255))
    
    # Pocket border
    draw.line(pocket_points, fill=(255, 255, 255, 255), width=int(s(5)), joint="curve")

    # 4. Top Pocket Rim (Crisp White Bar)
    draw.line([(s(28), s(48)), (s(100), s(48))], fill=(255, 255, 255, 255), width=int(s(6)))

    # 5. Inner Accent Seam (Lavender)
    seam_points = []
    for i in range(steps + 1):
        t = i / float(steps)
        # P0=(44,60), P1=(44,76), P2=(52,86), P3=(64,86)
        bx = (1-t)**3 * 44 + 3*(1-t)**2*t * 44 + 3*(1-t)*t**2 * 52 + t**3 * 64
        by = (1-t)**3 * 60 + 3*(1-t)**2*t * 76 + 3*(1-t)*t**2 * 86 + t**3 * 86
        seam_points.append((s(bx), s(by)))
    for i in range(1, steps + 1):
        t = i / float(steps)
        # P0=(64,86), P1=(76,86), P2=(84,76), P3=(84,60)
        bx = (1-t)**3 * 64 + 3*(1-t)**2*t * 76 + 3*(1-t)*t**2 * 84 + t**3 * 84
        by = (1-t)**3 * 86 + 3*(1-t)**2*t * 86 + 3*(1-t)*t**2 * 76 + t**3 * 60
        seam_points.append((s(bx), s(by)))
    draw.line(seam_points, fill=(192, 132, 252, 255), width=int(s(3.5)), joint="curve")

    # 6. Center Wireless Connection Dot
    dot_r = s(4)
    dot_cx, dot_cy = s(64), s(73)
    draw.ellipse(
        [dot_cx - dot_r, dot_cy - dot_r, dot_cx + dot_r, dot_cy + dot_r],
        fill=(255, 255, 255, 255)
    )

    # Downsample using Lanczos
    return img.resize((size, size), Image.Resampling.LANCZOS)

def main():
    base_dir = os.path.dirname(os.path.abspath(__file__))
    desktop_dir = os.path.join(base_dir, "desktop")
    android_res_dir = os.path.join(base_dir, "android", "app", "src", "main", "res")

    print("Generating crisp master icon (512x512)...")
    icon512 = render_logo(512)
    icon512.save(os.path.join(desktop_dir, "frontend", "logo.png"), "PNG")
    icon512.save(os.path.join(desktop_dir, "logo.png"), "PNG")

    # Multi-resolution ICO for Windows
    print("Generating Windows logo.ico...")
    sizes = [16, 24, 32, 48, 64, 128, 256]
    icon_images = [icon512.resize((s, s), Image.Resampling.LANCZOS) for s in sizes]
    ico_path = os.path.join(desktop_dir, "logo.ico")
    icon_images[-1].save(
        ico_path,
        format="ICO",
        sizes=[(s, s) for s in sizes],
        append_images=icon_images[:-1]
    )
    print(f"Saved {ico_path} with sizes: {sizes}")

    icon512.save(os.path.join(base_dir, "bigpocket.png"), "PNG")

    # Winres icons for Windows PE group icon
    winres_dir = os.path.join(desktop_dir, "winres")
    if os.path.exists(winres_dir):
        icon256 = icon512.resize((256, 256), Image.Resampling.LANCZOS)
        icon256.save(os.path.join(winres_dir, "icon.png"), "PNG")
        icon16 = icon512.resize((16, 16), Image.Resampling.LANCZOS)
        icon16.save(os.path.join(winres_dir, "icon16.png"), "PNG")
        print(f"Updated winres/icon.png and winres/icon16.png")

    # Android Mipmaps
    mipmap_targets = {
        "mipmap-mdpi": 48,
        "mipmap-hdpi": 72,
        "mipmap-xhdpi": 96,
        "mipmap-xxhdpi": 144,
        "mipmap-xxxhdpi": 192,
    }
    for folder, dim in mipmap_targets.items():
        target_dir = os.path.join(android_res_dir, folder)
        if os.path.exists(target_dir):
            scaled = icon512.resize((dim, dim), Image.Resampling.LANCZOS)
            scaled.save(os.path.join(target_dir, "ic_launcher.png"), "PNG")
            scaled.save(os.path.join(target_dir, "ic_launcher_round.png"), "PNG")
            print(f"Updated Android {folder}/ic_launcher.png ({dim}x{dim})")

    print("All icons successfully generated!")

if __name__ == "__main__":
    main()

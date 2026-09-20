import math
from PIL import Image, ImageDraw, ImageFilter

def create_smooth_app_icon(size=256):
    scale = 4  # 4x supersampling
    w = size * scale
    img = Image.new("RGBA", (w, w), (0, 0, 0, 0))
    
    # 1. Base Squircle / Rounded rect with rich dark gradient
    margin = int(w * 0.07)
    radius = int(w * 0.24)
    
    # Background gradient
    bg = Image.new("RGBA", (w, w), (0, 0, 0, 0))
    bg_draw = ImageDraw.Draw(bg)
    bg_draw.rounded_rectangle(
        [margin, margin, w - margin, w - margin],
        radius=radius,
        fill=(15, 23, 42, 255) # Slate 900
    )
    
    # Inner glowing outline
    bg_draw.rounded_rectangle(
        [margin, margin, w - margin, w - margin],
        radius=radius,
        outline=(56, 189, 248, 140), # Sky 400
        width=int(w * 0.012)
    )
    img = Image.alpha_composite(img, bg)
    draw = ImageDraw.Draw(img)

    center_x, center_y = w // 2, w // 2

    # 2. Modern circular sync arrows
    arrow_radius = int(w * 0.30)
    arrow_thick = int(w * 0.042)
    bbox = [
        center_x - arrow_radius, center_y - arrow_radius,
        center_x + arrow_radius, center_y + arrow_radius
    ]

    # Top arc: Google Emerald Green (34, 197, 94)
    draw.arc(bbox, start=25, end=155, fill=(34, 197, 94, 255), width=arrow_thick)
    # Bottom arc: Google Vibrant Blue (59, 130, 246)
    draw.arc(bbox, start=205, end=335, fill=(59, 130, 246, 255), width=arrow_thick)

    def draw_arrow_head(angle_deg, color, cw=True):
        rad = math.radians(angle_deg)
        tx = center_x + arrow_radius * math.cos(rad)
        ty = center_y + arrow_radius * math.sin(rad)
        tangent = rad + (math.pi / 2 if cw else -math.pi / 2)
        alen = arrow_thick * 2.3
        awid = arrow_thick * 1.9

        tip = (tx + math.cos(tangent) * alen * 0.45, ty + math.sin(tangent) * alen * 0.45)
        left = (
            tx - math.cos(tangent) * alen * 0.55 + math.sin(tangent) * awid * 0.5,
            ty - math.sin(tangent) * alen * 0.55 - math.cos(tangent) * awid * 0.5
        )
        right = (
            tx - math.cos(tangent) * alen * 0.55 - math.sin(tangent) * awid * 0.5,
            ty - math.sin(tangent) * alen * 0.55 + math.cos(tangent) * awid * 0.5
        )
        draw.polygon([tip, left, right], fill=color)

    draw_arrow_head(155, (34, 197, 94, 255))
    draw_arrow_head(335, (59, 130, 246, 255))

    # 3. Seamless Cloud Silhouette
    # Cloud base pill
    cloud_layer = Image.new("RGBA", (w, w), (0, 0, 0, 0))
    cdraw = ImageDraw.Draw(cloud_layer)

    cy = center_y + int(w * 0.02)
    base_w = int(w * 0.22)
    base_h = int(w * 0.08)
    
    # Bottom rounded pill
    cdraw.rounded_rectangle(
        [center_x - base_w, cy, center_x + base_w, cy + base_h * 2],
        radius=base_h,
        fill=(255, 255, 255, 255)
    )

    # Main center puff (large circle)
    cr_main = int(w * 0.13)
    cdraw.ellipse(
        [center_x - cr_main, cy - int(cr_main * 1.1), center_x + cr_main, cy + int(cr_main * 0.9)],
        fill=(255, 255, 255, 255)
    )

    # Left puff
    cr_left = int(w * 0.095)
    cdraw.ellipse(
        [center_x - base_w, cy - int(cr_left * 0.7), center_x - base_w + cr_left * 2, cy + cr_left * 1.3],
        fill=(255, 255, 255, 255)
    )

    # Right puff
    cr_right = int(w * 0.085)
    cdraw.ellipse(
        [center_x + base_w - cr_right * 2, cy - int(cr_right * 0.5), center_x + base_w, cy + cr_right * 1.5],
        fill=(255, 255, 255, 255)
    )

    # Composite cloud
    img = Image.alpha_composite(img, cloud_layer)

    # Downsample
    final = img.resize((size, size), Image.Resampling.LANCZOS)
    return final

def make_ico():
    sizes = [16, 24, 32, 48, 64, 128, 256]
    images = [create_smooth_app_icon(s) for s in sizes]
    
    # Save 256x256 preview PNG
    images[-1].save("icon.png")
    
    # Save multi-resolution ICO
    images[-1].save("app.ico", format="ICO", sizes=[(s, s) for s in sizes], append_images=images[:-1])
    print("Generated perfected app.ico and icon.png successfully!")

if __name__ == "__main__":
    make_ico()

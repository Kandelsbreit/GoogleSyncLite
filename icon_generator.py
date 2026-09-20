from PIL import Image, ImageDraw

def create_tray_icon_image(size: int = 64, syncing: bool = False) -> Image.Image:
    """Generates a clean modern sync icon for the Windows taskbar tray."""
    image = Image.new('RGBA', (size, size), (0, 0, 0, 0))
    draw = ImageDraw.Draw(image)
    
    # Outer circle
    color = (66, 133, 244) if not syncing else (52, 168, 83) # Google Blue or Green
    margin = 4
    draw.ellipse([margin, margin, size - margin, size - margin], outline=color, width=6)
    
    # Inner arrows / sync design
    center = size // 2
    draw.arc([margin + 10, margin + 10, size - margin - 10, size - margin - 10], 
             start=30, end=150, fill=color, width=5)
    draw.arc([margin + 10, margin + 10, size - margin - 10, size - margin - 10], 
             start=210, end=330, fill=color, width=5)
    
    # Arrow heads
    draw.polygon([(center + 12, margin + 10), (center + 20, margin + 16), (center + 12, margin + 22)], fill=color)
    draw.polygon([(center - 12, size - margin - 10), (center - 20, size - margin - 16), (center - 12, size - margin - 22)], fill=color)
    
    return image

if __name__ == "__main__":
    img = create_tray_icon_image()
    img.save("test_icon.png")
    print("Tray icon generated.")

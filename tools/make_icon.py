"""Render ColabOne's geometric source mark; requires Pillow only when regenerating."""

from pathlib import Path
from PIL import Image, ImageDraw

root = Path(__file__).resolve().parents[1] / "console/resources"
image = Image.new("RGBA", (1024, 1024), (0, 0, 0, 0))
d = ImageDraw.Draw(image)
d.rounded_rectangle((20, 20, 1004, 1004), radius=224, fill="#22634b")
for x, y in ((512, 345), (367, 590), (657, 590)):
    d.ellipse((x - 160, y - 160, x + 160, y + 160), outline="#f5fbf7", width=42)
image.save(root / "icon.png")
tray = Image.new("RGBA", (64, 64), (0, 0, 0, 0))
d = ImageDraw.Draw(tray)
for x, y in ((32, 21), (22, 39), (42, 39)):
    d.ellipse((x - 10, y - 10, x + 10, y + 10), outline="#6b967d", width=3)
tray.save(root / "tray.png")

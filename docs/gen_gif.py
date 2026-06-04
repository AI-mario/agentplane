#!/usr/bin/env python3
"""Generate an animated GIF showing an orchestrated agent ecosystem:
- A fenced pasture with emoji-style animals (agents) moving around
- A farm building (registry/scheduler)
- A factory building (policy engine/lifecycle)
- Connecting paths showing the orchestrated ecosystem
"""

import math
import random
from PIL import Image, ImageDraw, ImageFont

WIDTH, HEIGHT = 600, 400
FRAMES = 60
BG_COLOR = (135, 206, 235)  # sky blue

# Try to get a font that supports rendering
try:
    font_large = ImageFont.truetype("/System/Library/Fonts/Helvetica.ttc", 14)
    font_small = ImageFont.truetype("/System/Library/Fonts/Helvetica.ttc", 10)
    font_title = ImageFont.truetype("/System/Library/Fonts/Helvetica.ttc", 18)
except:
    font_large = ImageFont.load_default()
    font_small = ImageFont.load_default()
    font_title = ImageFont.load_default()


# Animals with distinct shapes
ANIMALS = [
    {"type": "sheep", "x": 220, "y": 220, "dx": 0.8, "dy": 0.5, "color": (240, 240, 240), "outline": (100, 100, 100)},
    {"type": "cow", "x": 310, "y": 260, "dx": -0.6, "dy": 0.7, "color": (50, 50, 50), "outline": (20, 20, 20)},
    {"type": "chicken", "x": 270, "y": 180, "dx": 1.2, "dy": -0.4, "color": (255, 200, 50), "outline": (200, 100, 0)},
    {"type": "pig", "x": 350, "y": 300, "dx": -0.5, "dy": -0.8, "color": (255, 180, 180), "outline": (200, 100, 100)},
    {"type": "horse", "x": 200, "y": 300, "dx": 0.9, "dy": -0.3, "color": (139, 90, 43), "outline": (80, 50, 20)},
    {"type": "duck", "x": 380, "y": 200, "dx": -1.0, "dy": 0.6, "color": (255, 255, 200), "outline": (200, 150, 0)},
]

# Pasture bounds
PASTURE_LEFT = 160
PASTURE_TOP = 150
PASTURE_RIGHT = 450
PASTURE_BOTTOM = 360


def draw_sky_and_ground(draw):
    """Draw sky gradient and green ground."""
    # Sky (already bg color)
    # Ground
    draw.rectangle([0, 120, WIDTH, HEIGHT], fill=(100, 180, 60))
    # Darker grass patches
    random.seed(42)
    for _ in range(50):
        gx = random.randint(0, WIDTH)
        gy = random.randint(130, HEIGHT)
        draw.ellipse([gx, gy, gx + random.randint(5, 15), gy + random.randint(3, 8)], fill=(80, 160, 50))


def draw_farm(draw, frame):
    """Draw the farm building (left side) — represents Registry/Scheduler."""
    # Barn body
    draw.rectangle([20, 80, 120, 145], fill=(180, 50, 50), outline=(100, 30, 30), width=2)
    # Barn roof
    draw.polygon([(15, 80), (70, 45), (125, 80)], fill=(120, 30, 30), outline=(80, 20, 20))
    # Door
    draw.rectangle([55, 110, 85, 145], fill=(80, 40, 20))
    # Window
    draw.rectangle([30, 90, 50, 108], fill=(200, 220, 255), outline=(80, 40, 20))
    # Hay bale indicator (pulsing)
    pulse = abs(math.sin(frame * 0.1)) * 0.3 + 0.7
    hay_color = (int(220 * pulse), int(180 * pulse), int(50 * pulse))
    draw.ellipse([95, 125, 115, 143], fill=hay_color, outline=(150, 120, 30))
    # Label
    draw.text((25, 148), "Registry", fill=(50, 50, 50), font=font_small)
    draw.text((25, 160), "& Scheduler", fill=(50, 50, 50), font=font_small)


def draw_factory(draw, frame):
    """Draw the factory building (right side) — represents Policy/Lifecycle."""
    # Factory body
    draw.rectangle([480, 70, 580, 145], fill=(160, 160, 170), outline=(100, 100, 110), width=2)
    # Chimney
    draw.rectangle([540, 40, 560, 75], fill=(120, 120, 130), outline=(80, 80, 90))
    # Smoke (animated)
    for i in range(3):
        sx = 550 + math.sin(frame * 0.15 + i) * 8
        sy = 35 - i * 12 - (frame % 20) * 0.3
        size = 6 + i * 3
        alpha = max(0, 200 - i * 60)
        draw.ellipse([sx - size, sy - size, sx + size, sy + size], fill=(220, 220, 220))
    # Windows
    for wx in [495, 520, 545]:
        draw.rectangle([wx, 85, wx + 15, 100], fill=(200, 220, 255), outline=(80, 80, 90))
    # Door
    draw.rectangle([515, 115, 545, 145], fill=(80, 80, 90))
    # Gear icon (rotating)
    cx, cy = 530, 105
    angle = frame * 0.1
    for i in range(6):
        a = angle + i * math.pi / 3
        x1 = cx + math.cos(a) * 4
        y1 = cy + math.sin(a) * 4
        draw.ellipse([x1 - 2, y1 - 2, x1 + 2, y1 + 2], fill=(60, 60, 70))
    # Label
    draw.text((485, 148), "Policy Engine", fill=(50, 50, 50), font=font_small)
    draw.text((485, 160), "& Lifecycle", fill=(50, 50, 50), font=font_small)


def draw_fence(draw):
    """Draw wooden fence around pasture."""
    # Horizontal rails
    for y in [PASTURE_TOP, PASTURE_TOP + 15, PASTURE_BOTTOM - 15, PASTURE_BOTTOM]:
        draw.line([(PASTURE_LEFT, y), (PASTURE_RIGHT, y)], fill=(139, 90, 43), width=3)
    # Vertical rails
    for x in [PASTURE_LEFT, PASTURE_RIGHT]:
        draw.line([(x, PASTURE_TOP), (x, PASTURE_BOTTOM)], fill=(139, 90, 43), width=3)
    # Fence posts
    for x in range(PASTURE_LEFT, PASTURE_RIGHT + 1, 25):
        draw.rectangle([x - 2, PASTURE_TOP - 5, x + 2, PASTURE_TOP + 5], fill=(100, 60, 20))
        draw.rectangle([x - 2, PASTURE_BOTTOM - 5, x + 2, PASTURE_BOTTOM + 5], fill=(100, 60, 20))
    for y in range(PASTURE_TOP, PASTURE_BOTTOM + 1, 25):
        draw.rectangle([PASTURE_LEFT - 5, y - 2, PASTURE_LEFT + 5, y + 2], fill=(100, 60, 20))
        draw.rectangle([PASTURE_RIGHT - 5, y - 2, PASTURE_RIGHT + 5, y + 2], fill=(100, 60, 20))


def draw_paths(draw, frame):
    """Draw animated connecting paths between buildings and pasture."""
    # Path from farm to pasture (dashed, animated)
    dash_offset = frame % 10
    for i in range(0, 40, 10):
        x = 120 + i + dash_offset
        if x < PASTURE_LEFT:
            draw.rectangle([x, 148, x + 5, 152], fill=(180, 150, 100))

    # Path from factory to pasture
    for i in range(0, 30, 10):
        x = PASTURE_RIGHT + i + 5 + dash_offset
        if x < 480:
            draw.rectangle([x, 148, x + 5, 152], fill=(180, 150, 100))

    # Signal dots (flowing from farm to pasture)
    dot_x = 120 + ((frame * 3) % (PASTURE_LEFT - 120))
    draw.ellipse([dot_x - 3, 147, dot_x + 3, 153], fill=(50, 200, 50))

    # Signal dots (flowing from factory to pasture)
    dot_x2 = PASTURE_RIGHT + 5 + ((frame * 2.5) % (480 - PASTURE_RIGHT - 5))
    draw.ellipse([dot_x2 - 3, 147, dot_x2 + 3, 153], fill=(50, 100, 200))


def draw_animal(draw, animal, frame):
    """Draw a schematic emoji-style animal."""
    x, y = int(animal["x"]), int(animal["y"])
    color = animal["color"]
    outline = animal["outline"]
    t = animal["type"]

    # Body bounce
    bounce = math.sin(frame * 0.3 + hash(t)) * 1.5

    if t == "sheep":
        # Fluffy body
        for dx, dy in [(-4, -3), (4, -3), (-3, 3), (3, 3), (0, -5), (0, 4)]:
            draw.ellipse([x + dx - 5, y + dy - 4 + bounce, x + dx + 5, y + dy + 4 + bounce], fill=color)
        # Head
        draw.ellipse([x - 4, y - 10 + bounce, x + 4, y - 3 + bounce], fill=(200, 200, 200), outline=outline)
        # Eyes
        draw.ellipse([x - 2, y - 8 + bounce, x, y - 6 + bounce], fill=(0, 0, 0))
        draw.ellipse([x + 1, y - 8 + bounce, x + 3, y - 6 + bounce], fill=(0, 0, 0))

    elif t == "cow":
        # Body
        draw.ellipse([x - 12, y - 6 + bounce, x + 12, y + 8 + bounce], fill=color, outline=outline)
        # Spots
        draw.ellipse([x - 5, y - 3 + bounce, x + 2, y + 3 + bounce], fill=(240, 240, 240))
        draw.ellipse([x + 3, y + 1 + bounce, x + 8, y + 5 + bounce], fill=(240, 240, 240))
        # Head
        draw.ellipse([x - 14, y - 10 + bounce, x - 6, y - 2 + bounce], fill=color, outline=outline)
        # Horns
        draw.line([(x - 12, y - 10 + bounce), (x - 14, y - 14 + bounce)], fill=(200, 180, 100), width=2)
        draw.line([(x - 8, y - 10 + bounce), (x - 6, y - 14 + bounce)], fill=(200, 180, 100), width=2)

    elif t == "chicken":
        # Body
        draw.ellipse([x - 7, y - 5 + bounce, x + 7, y + 6 + bounce], fill=color, outline=outline)
        # Head
        draw.ellipse([x + 5, y - 9 + bounce, x + 12, y - 2 + bounce], fill=color, outline=outline)
        # Beak
        draw.polygon([(x + 12, y - 6 + bounce), (x + 16, y - 5 + bounce), (x + 12, y - 4 + bounce)], fill=(255, 100, 0))
        # Comb
        draw.polygon([(x + 7, y - 9 + bounce), (x + 8, y - 13 + bounce), (x + 10, y - 9 + bounce)], fill=(255, 0, 0))
        # Eye
        draw.ellipse([x + 8, y - 7 + bounce, x + 10, y - 5 + bounce], fill=(0, 0, 0))
        # Legs
        draw.line([(x - 2, y + 6 + bounce), (x - 2, y + 10 + bounce)], fill=outline, width=1)
        draw.line([(x + 2, y + 6 + bounce), (x + 2, y + 10 + bounce)], fill=outline, width=1)

    elif t == "pig":
        # Body
        draw.ellipse([x - 10, y - 7 + bounce, x + 10, y + 7 + bounce], fill=color, outline=outline)
        # Head
        draw.ellipse([x + 8, y - 8 + bounce, x + 18, y + 2 + bounce], fill=color, outline=outline)
        # Snout
        draw.ellipse([x + 15, y - 5 + bounce, x + 21, y + 0 + bounce], fill=(255, 150, 150), outline=outline)
        draw.ellipse([x + 16, y - 3 + bounce, x + 18, y - 1 + bounce], fill=outline)
        draw.ellipse([x + 19, y - 3 + bounce, x + 21, y - 1 + bounce], fill=outline)
        # Ear
        draw.polygon([(x + 10, y - 8 + bounce), (x + 12, y - 13 + bounce), (x + 14, y - 8 + bounce)], fill=color, outline=outline)
        # Curly tail
        tail_x = x - 10
        draw.arc([tail_x - 6, y - 4 + bounce, tail_x, y + 2 + bounce], 0, 270, fill=outline, width=2)

    elif t == "horse":
        # Body
        draw.ellipse([x - 12, y - 6 + bounce, x + 12, y + 8 + bounce], fill=color, outline=outline)
        # Neck + Head
        draw.polygon([(x - 8, y - 6 + bounce), (x - 12, y - 18 + bounce), (x - 4, y - 18 + bounce), (x - 2, y - 6 + bounce)], fill=color, outline=outline)
        draw.ellipse([x - 15, y - 22 + bounce, x - 5, y - 15 + bounce], fill=color, outline=outline)
        # Mane
        for i in range(4):
            my = y - 8 - i * 3 + bounce
            draw.line([(x - 6, my), (x - 2, my - 2)], fill=(60, 30, 10), width=2)
        # Legs
        for lx in [x - 7, x - 3, x + 3, x + 7]:
            draw.line([(lx, y + 8 + bounce), (lx, y + 14 + bounce)], fill=outline, width=2)

    elif t == "duck":
        # Body
        draw.ellipse([x - 8, y - 5 + bounce, x + 8, y + 6 + bounce], fill=color, outline=outline)
        # Head
        draw.ellipse([x + 6, y - 10 + bounce, x + 14, y - 2 + bounce], fill=color, outline=outline)
        # Beak
        draw.polygon([(x + 13, y - 7 + bounce), (x + 19, y - 6 + bounce), (x + 13, y - 4 + bounce)], fill=(255, 150, 0))
        # Eye
        draw.ellipse([x + 9, y - 8 + bounce, x + 11, y - 6 + bounce], fill=(0, 0, 0))
        # Wing
        draw.ellipse([x - 4, y - 3 + bounce, x + 4, y + 4 + bounce], fill=(230, 230, 170), outline=outline)


def draw_sun(draw, frame):
    """Draw animated sun."""
    cx, cy = 560, 35
    # Rays (rotating)
    for i in range(8):
        angle = frame * 0.05 + i * math.pi / 4
        x1 = cx + math.cos(angle) * 20
        y1 = cy + math.sin(angle) * 20
        x2 = cx + math.cos(angle) * 28
        y2 = cy + math.sin(angle) * 28
        draw.line([(x1, y1), (x2, y2)], fill=(255, 200, 0), width=2)
    # Sun body
    draw.ellipse([cx - 15, cy - 15, cx + 15, cy + 15], fill=(255, 220, 50), outline=(255, 180, 0))


def draw_clouds(draw, frame):
    """Draw drifting clouds."""
    for i, (base_x, base_y) in enumerate([(80, 30), (250, 20), (420, 40)]):
        cx = (base_x + frame * (0.5 + i * 0.2)) % (WIDTH + 60) - 30
        for dx, dy, r in [(-10, 0, 12), (0, -5, 15), (10, 0, 12), (5, 5, 10)]:
            draw.ellipse([cx + dx - r, base_y + dy - r, cx + dx + r, base_y + dy + r], fill=(255, 255, 255))


def bounce_animal(animal):
    """Bounce animal off pasture walls."""
    margin = 20
    if animal["x"] <= PASTURE_LEFT + margin or animal["x"] >= PASTURE_RIGHT - margin:
        animal["dx"] *= -1
        animal["x"] = max(PASTURE_LEFT + margin, min(PASTURE_RIGHT - margin, animal["x"]))
    if animal["y"] <= PASTURE_TOP + margin or animal["y"] >= PASTURE_BOTTOM - margin:
        animal["dy"] *= -1
        animal["y"] = max(PASTURE_TOP + margin, min(PASTURE_BOTTOM - margin, animal["y"]))


def draw_title(draw, frame):
    """Draw title with subtle pulse."""
    pulse = 0.9 + math.sin(frame * 0.1) * 0.1
    c = int(40 * pulse)
    draw.text((WIDTH // 2 - 70, HEIGHT - 25), "AgentPlane Fleet", fill=(c, c, c), font=font_title)


def draw_status_indicators(draw, frame):
    """Draw small status LEDs near the farm and factory."""
    # Farm status (green blinking)
    if frame % 20 < 15:
        draw.ellipse([125, 82, 133, 90], fill=(0, 220, 0))
    else:
        draw.ellipse([125, 82, 133, 90], fill=(0, 100, 0))

    # Factory status
    if frame % 30 < 25:
        draw.ellipse([475, 72, 483, 80], fill=(0, 150, 255))
    else:
        draw.ellipse([475, 72, 483, 80], fill=(0, 60, 120))


def generate_gif(output_path):
    frames = []

    for frame_idx in range(FRAMES):
        img = Image.new("RGB", (WIDTH, HEIGHT), BG_COLOR)
        draw = ImageDraw.Draw(img)

        draw_sky_and_ground(draw)
        draw_clouds(draw, frame_idx)
        draw_sun(draw, frame_idx)

        # Pasture grass (lighter area)
        draw.rectangle([PASTURE_LEFT + 5, PASTURE_TOP + 5, PASTURE_RIGHT - 5, PASTURE_BOTTOM - 5],
                       fill=(130, 200, 80))

        draw_paths(draw, frame_idx)
        draw_farm(draw, frame_idx)
        draw_factory(draw, frame_idx)
        draw_fence(draw)
        draw_status_indicators(draw, frame_idx)

        # Update and draw animals
        for animal in ANIMALS:
            wobble_x = math.sin(frame_idx * 0.2 + hash(animal["type"])) * 0.3
            wobble_y = math.cos(frame_idx * 0.25 + hash(animal["type"])) * 0.3
            animal["x"] += animal["dx"] + wobble_x
            animal["y"] += animal["dy"] + wobble_y
            bounce_animal(animal)
            draw_animal(draw, animal, frame_idx)

        draw_title(draw, frame_idx)

        frames.append(img)

    # Save as GIF
    frames[0].save(
        output_path,
        save_all=True,
        append_images=frames[1:],
        duration=80,  # 80ms per frame ≈ 12.5fps
        loop=0,
        optimize=True,
    )
    print(f"GIF saved to {output_path} ({len(frames)} frames, {WIDTH}x{HEIGHT})")


if __name__ == "__main__":
    generate_gif("docs/agentplane-fleet.gif")

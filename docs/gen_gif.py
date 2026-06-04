#!/usr/bin/env python3
"""Generate a small animated GIF showing 'agents' (animals) moving in a fenced area."""

import math
import random
from PIL import Image, ImageDraw, ImageFont

WIDTH, HEIGHT = 400, 300
FRAMES = 40
FENCE_MARGIN = 30
BG_COLOR = (245, 245, 220)  # beige
FENCE_COLOR = (139, 69, 19)  # saddle brown
GRASS_COLOR = (200, 230, 180)

# Animals as simple colored circles with labels
ANIMALS = [
    {"name": "🐑", "color": (180, 180, 180), "x": 120, "y": 140, "dx": 1.5, "dy": 0.8},
    {"name": "🐄", "color": (60, 60, 60), "x": 250, "y": 180, "dx": -1.0, "dy": 1.2},
    {"name": "🐓", "color": (200, 50, 50), "x": 180, "y": 100, "dx": 2.0, "dy": -0.5},
    {"name": "🐖", "color": (255, 180, 180), "x": 300, "y": 220, "dx": -0.8, "dy": -1.0},
    {"name": "🐴", "color": (139, 90, 43), "x": 100, "y": 220, "dx": 1.2, "dy": -0.7},
]

RADIUS = 12


def draw_fence(draw):
    """Draw a wooden fence border."""
    m = FENCE_MARGIN
    # Posts
    for x in range(m, WIDTH - m + 1, 30):
        draw.rectangle([x - 3, m - 5, x + 3, m + 8], fill=FENCE_COLOR)
        draw.rectangle([x - 3, HEIGHT - m - 8, x + 3, HEIGHT - m + 5], fill=FENCE_COLOR)
    for y in range(m, HEIGHT - m + 1, 30):
        draw.rectangle([m - 5, y - 3, m + 8, y + 3], fill=FENCE_COLOR)
        draw.rectangle([WIDTH - m - 8, y - 3, WIDTH - m + 5, y + 3], fill=FENCE_COLOR)
    # Rails
    draw.rectangle([m, m, WIDTH - m, m + 4], fill=FENCE_COLOR)
    draw.rectangle([m, HEIGHT - m - 4, WIDTH - m, HEIGHT - m], fill=FENCE_COLOR)
    draw.rectangle([m, m, m + 4, HEIGHT - m], fill=FENCE_COLOR)
    draw.rectangle([WIDTH - m - 4, m, WIDTH - m, HEIGHT - m], fill=FENCE_COLOR)


def draw_grass(draw):
    """Draw simple grass tufts."""
    random.seed(42)
    for _ in range(30):
        gx = random.randint(FENCE_MARGIN + 20, WIDTH - FENCE_MARGIN - 20)
        gy = random.randint(FENCE_MARGIN + 20, HEIGHT - FENCE_MARGIN - 20)
        draw.line([(gx, gy), (gx - 3, gy - 8)], fill=(80, 160, 80), width=1)
        draw.line([(gx, gy), (gx + 3, gy - 7)], fill=(80, 160, 80), width=1)


def bounce(animal):
    """Bounce animal off fence walls."""
    m = FENCE_MARGIN + RADIUS + 5
    if animal["x"] <= m or animal["x"] >= WIDTH - m:
        animal["dx"] *= -1
        animal["x"] = max(m, min(WIDTH - m, animal["x"]))
    if animal["y"] <= m or animal["y"] >= HEIGHT - m:
        animal["dy"] *= -1
        animal["y"] = max(m, min(HEIGHT - m, animal["y"]))


def generate_gif(output_path):
    frames = []

    for frame_idx in range(FRAMES):
        img = Image.new("RGB", (WIDTH, HEIGHT), BG_COLOR)
        draw = ImageDraw.Draw(img)

        # Green pasture area
        draw.rectangle(
            [FENCE_MARGIN + 5, FENCE_MARGIN + 5, WIDTH - FENCE_MARGIN - 5, HEIGHT - FENCE_MARGIN - 5],
            fill=GRASS_COLOR,
        )

        draw_grass(draw)
        draw_fence(draw)

        # Draw title
        draw.text((WIDTH // 2 - 60, 8), "AgentPlane Fleet", fill=(50, 50, 50))

        # Update and draw animals
        for animal in ANIMALS:
            # Add slight wobble
            wobble_x = math.sin(frame_idx * 0.3 + hash(animal["name"])) * 0.5
            wobble_y = math.cos(frame_idx * 0.4 + hash(animal["name"])) * 0.5

            animal["x"] += animal["dx"] + wobble_x
            animal["y"] += animal["dy"] + wobble_y
            bounce(animal)

            x, y = int(animal["x"]), int(animal["y"])

            # Shadow
            draw.ellipse([x - RADIUS + 2, y - RADIUS + 2, x + RADIUS + 2, y + RADIUS + 2], fill=(100, 100, 100, 80))
            # Body
            draw.ellipse([x - RADIUS, y - RADIUS, x + RADIUS, y + RADIUS], fill=animal["color"], outline=(40, 40, 40))
            # Eyes
            draw.ellipse([x - 3, y - 4, x - 1, y - 2], fill=(255, 255, 255))
            draw.ellipse([x + 1, y - 4, x + 3, y - 2], fill=(255, 255, 255))

        frames.append(img)

    # Save as GIF
    frames[0].save(
        output_path,
        save_all=True,
        append_images=frames[1:],
        duration=100,  # 100ms per frame = 10fps
        loop=0,
    )
    print(f"GIF saved to {output_path} ({len(frames)} frames, {WIDTH}x{HEIGHT})")


if __name__ == "__main__":
    generate_gif("docs/agentplane-fleet.gif")

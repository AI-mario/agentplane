#!/usr/bin/env python3
"""Generate an animated GIF showing AgentPlane as a control tower with orbiting agents,
pulsing connections, and a mini live dashboard. Communicates the concept instantly."""

import math
from PIL import Image, ImageDraw, ImageFont

WIDTH, HEIGHT = 640, 360
FRAMES = 80
BG_COLOR = (18, 22, 36)  # dark navy
CENTER_X, CENTER_Y = 260, 180

# Fonts
try:
    font_title = ImageFont.truetype("/System/Library/Fonts/Helvetica.ttc", 20)
    font_label = ImageFont.truetype("/System/Library/Fonts/Helvetica.ttc", 11)
    font_small = ImageFont.truetype("/System/Library/Fonts/Helvetica.ttc", 9)
    font_tagline = ImageFont.truetype("/System/Library/Fonts/Helvetica.ttc", 13)
except:
    font_title = ImageFont.load_default()
    font_label = ImageFont.load_default()
    font_small = ImageFont.load_default()
    font_tagline = ImageFont.load_default()

# Agents orbiting
AGENTS = [
    {"name": "Claude", "color": (210, 140, 60), "orbit": 110, "speed": 0.025, "offset": 0},
    {"name": "Kiro", "color": (100, 180, 255), "orbit": 110, "speed": 0.025, "offset": math.pi * 0.5},
    {"name": "Bedrock", "color": (255, 100, 130), "orbit": 110, "speed": 0.025, "offset": math.pi},
    {"name": "Custom", "color": (130, 220, 130), "orbit": 110, "speed": 0.025, "offset": math.pi * 1.5},
]


def lerp_color(c1, c2, t):
    return tuple(int(a + (b - a) * t) for a, b in zip(c1, c2))


def draw_starfield(draw, frame):
    """Subtle animated starfield background."""
    import random
    random.seed(123)
    for _ in range(60):
        sx = random.randint(0, WIDTH)
        sy = random.randint(0, HEIGHT)
        brightness = random.randint(40, 100) + int(math.sin(frame * 0.1 + sx) * 20)
        brightness = max(30, min(120, brightness))
        draw.point((sx, sy), fill=(brightness, brightness, brightness + 20))


def draw_orbit_rings(draw):
    """Draw subtle orbit path."""
    for i in range(4):
        # Faint dashed orbit
        for angle_deg in range(0, 360, 8):
            a = math.radians(angle_deg)
            x = CENTER_X + math.cos(a) * AGENTS[0]["orbit"]
            y = CENTER_Y + math.sin(a) * AGENTS[0]["orbit"] * 0.55  # elliptical
            if angle_deg % 16 < 8:
                draw.ellipse([x - 1, y - 1, x + 1, y + 1], fill=(50, 60, 80))


def draw_control_tower(draw, frame):
    """Draw central hexagonal control tower with pulsing glow."""
    cx, cy = CENTER_X, CENTER_Y
    # Glow pulse
    pulse = 0.7 + math.sin(frame * 0.08) * 0.3
    glow_radius = int(38 * pulse)
    for r in range(glow_radius, 25, -2):
        alpha = int((1 - (r - 25) / (glow_radius - 25)) * 40)
        color = (30 + alpha, 80 + alpha, 180 + min(alpha, 75))
        draw.ellipse([cx - r, cy - r, cx + r, cy + r], fill=color)

    # Hexagon body
    hex_r = 28
    points = []
    for i in range(6):
        angle = math.pi / 6 + i * math.pi / 3
        points.append((cx + math.cos(angle) * hex_r, cy + math.sin(angle) * hex_r))
    draw.polygon(points, fill=(40, 70, 140), outline=(100, 160, 255))

    # Inner circle
    draw.ellipse([cx - 12, cy - 12, cx + 12, cy + 12], fill=(20, 40, 100), outline=(80, 140, 255))

    # Rotating inner indicator
    ind_angle = frame * 0.1
    ix = cx + math.cos(ind_angle) * 7
    iy = cy + math.sin(ind_angle) * 7
    draw.ellipse([ix - 3, iy - 3, ix + 3, iy + 3], fill=(100, 200, 255))

    # Label
    draw.text((cx - 30, cy + 32), "AgentPlane", fill=(180, 200, 255), font=font_label)


def draw_agent_node(draw, agent, frame):
    """Draw orbiting agent with connection line."""
    angle = frame * agent["speed"] + agent["offset"]
    x = CENTER_X + math.cos(angle) * agent["orbit"]
    y = CENTER_Y + math.sin(angle) * agent["orbit"] * 0.55  # elliptical orbit

    # Connection line (pulsing)
    pulse_pos = (frame * 0.05 + agent["offset"]) % 1.0
    for t in [0.3, 0.5, 0.7]:
        actual_t = (t + pulse_pos) % 1.0
        px = CENTER_X + (x - CENTER_X) * actual_t
        py = CENTER_Y + (y - CENTER_Y) * actual_t
        dot_size = 2
        brightness = int(255 * (1 - abs(actual_t - 0.5) * 2))
        dot_color = tuple(int(c * brightness / 255) for c in agent["color"])
        draw.ellipse([px - dot_size, py - dot_size, px + dot_size, py + dot_size], fill=dot_color)

    # Faint connection line
    draw.line([(CENTER_X, CENTER_Y), (x, y)], fill=(40, 50, 70), width=1)

    # Agent node glow
    for r in range(14, 8, -1):
        alpha_factor = (14 - r) / 6
        glow = tuple(int(c * alpha_factor * 0.4) for c in agent["color"])
        draw.ellipse([x - r, y - r, x + r, y + r], fill=glow)

    # Agent node body
    draw.ellipse([x - 8, y - 8, x + 8, y + 8], fill=agent["color"], outline=(255, 255, 255))

    # Agent label
    label_x = x - len(agent["name"]) * 3
    label_y = y + 12
    draw.text((label_x, label_y), agent["name"], fill=agent["color"], font=font_small)

    return x, y


def draw_mini_dashboard(draw, frame):
    """Draw animated mini dashboard on the right side."""
    dx, dy = 470, 40
    panel_w, panel_h = 150, 280

    # Panel background
    draw.rounded_rectangle([dx, dy, dx + panel_w, dy + panel_h], radius=8, fill=(25, 30, 50), outline=(60, 80, 120))

    # Header
    draw.text((dx + 15, dy + 8), "Live Status", fill=(150, 180, 220), font=font_label)
    draw.line([(dx + 10, dy + 25), (dx + panel_w - 10, dy + 25)], fill=(50, 60, 90))

    # Health indicator
    draw.text((dx + 12, dy + 32), "Health", fill=(120, 140, 160), font=font_small)
    health_color = (50, 220, 100) if frame % 60 < 55 else (220, 180, 50)
    draw.ellipse([dx + 120, dy + 33, dx + 130, dy + 43], fill=health_color)

    # Active missions counter (animated)
    missions = 12 + int(math.sin(frame * 0.08) * 3)
    draw.text((dx + 12, dy + 50), "Missions", fill=(120, 140, 160), font=font_small)
    draw.text((dx + 110, dy + 50), str(missions), fill=(100, 200, 255), font=font_label)

    # Agents online
    draw.text((dx + 12, dy + 68), "Agents", fill=(120, 140, 160), font=font_small)
    draw.text((dx + 110, dy + 68), "4/4", fill=(50, 220, 100), font=font_label)

    # Cost bars (animated)
    draw.text((dx + 12, dy + 92), "Cost / Budget", fill=(120, 140, 160), font=font_small)
    teams = [("Platform", 0.6), ("ML Ops", 0.35), ("Data", 0.8)]
    for i, (team, base_pct) in enumerate(teams):
        by = dy + 110 + i * 28
        pct = base_pct + math.sin(frame * 0.05 + i) * 0.05
        pct = max(0.1, min(0.95, pct))
        bar_w = int(110 * pct)

        draw.text((dx + 12, by), team, fill=(100, 120, 140), font=font_small)
        # Bar background
        draw.rounded_rectangle([dx + 12, by + 13, dx + 135, by + 21], radius=3, fill=(35, 40, 60))
        # Bar fill
        bar_color = (50, 180, 100) if pct < 0.7 else (220, 180, 50) if pct < 0.9 else (220, 80, 80)
        draw.rounded_rectangle([dx + 12, by + 13, dx + 12 + bar_w, by + 21], radius=3, fill=bar_color)
        # Percentage
        draw.text((dx + 138, by + 11), f"{int(pct * 100)}%", fill=(150, 160, 180), font=font_small)

    # SLO compliance
    draw.line([(dx + 10, dy + 200), (dx + panel_w - 10, dy + 200)], fill=(50, 60, 90))
    draw.text((dx + 12, dy + 208), "SLO Compliance", fill=(120, 140, 160), font=font_small)

    # Mini sparkline
    sparkline_y = dy + 228
    points = []
    for i in range(20):
        sx = dx + 12 + i * 6
        val = 95 + math.sin((frame + i * 3) * 0.1) * 3
        sy = sparkline_y + 15 - (val - 90) * 2
        points.append((sx, sy))
    if len(points) > 1:
        draw.line(points, fill=(100, 200, 255), width=2)

    # Current value
    current_slo = 95 + math.sin(frame * 0.1) * 2
    draw.text((dx + 100, dy + 208), f"{current_slo:.1f}%", fill=(100, 220, 150), font=font_label)

    # Circuit breaker status
    draw.text((dx + 12, dy + 255), "Circuit Breakers", fill=(120, 140, 160), font=font_small)
    for i in range(4):
        cb_x = dx + 100 + i * 12
        cb_color = (50, 220, 100)  # all closed/green
        draw.ellipse([cb_x, dy + 256, cb_x + 8, dy + 264], fill=cb_color)


def draw_tagline(draw, frame):
    """Draw bottom tagline."""
    text = "One binary. Fleet control. Zero ops."
    # Fade in/out subtly
    alpha = int(180 + math.sin(frame * 0.06) * 40)
    color = (alpha, alpha, min(255, alpha + 40))
    draw.text((WIDTH // 2 - 120, HEIGHT - 30), text, fill=color, font=font_tagline)


def draw_title(draw):
    """Draw top title."""
    draw.text((20, 12), "AgentPlane", fill=(200, 220, 255), font=font_title)
    draw.text((155, 18), "control plane for AI agents", fill=(100, 120, 150), font=font_small)


def generate_gif(output_path):
    frames = []

    for frame_idx in range(FRAMES):
        img = Image.new("RGB", (WIDTH, HEIGHT), BG_COLOR)
        draw = ImageDraw.Draw(img)

        draw_starfield(draw, frame_idx)
        draw_orbit_rings(draw)
        draw_control_tower(draw, frame_idx)

        for agent in AGENTS:
            draw_agent_node(draw, agent, frame_idx)

        draw_mini_dashboard(draw, frame_idx)
        draw_title(draw)
        draw_tagline(draw, frame_idx)

        frames.append(img)

    frames[0].save(
        output_path,
        save_all=True,
        append_images=frames[1:],
        duration=70,  # ~14fps
        loop=0,
        optimize=True,
    )
    print(f"GIF saved to {output_path} ({len(frames)} frames, {WIDTH}x{HEIGHT})")


if __name__ == "__main__":
    generate_gif("docs/agentplane-fleet.gif")

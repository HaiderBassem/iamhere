"""Procedural test scenes: a room's front wall (posters, board, door, text) seen
from different seats. Real-photo accuracy is measured separately (docs/VISION.md);
these keep the service's behaviour pinned in CI without third-party images."""
import cv2
import numpy as np
import qrcode

WORDS = ["EXIT", "LAB 3", "ROOM 204", "Physics", "Welcome", "Library", "Fire plan", "Notices", "Math", "Club"]


def wall(seed: int, w: int = 1600, h: int = 1000) -> np.ndarray:
    r = np.random.default_rng(seed)
    img = np.full((h, w, 3), r.integers(150, 225, 3), np.uint8)
    img = cv2.add(img, r.integers(0, 18, (h, w, 3), dtype=np.uint8))              # paint texture
    bx, by = float(r.uniform(.12, .4)), float(r.uniform(.1, .25))                    # board: every room has one, not in the same place
    cv2.rectangle(img, (int(w * bx), int(h * by)), (int(w * (bx + .45)), int(h * (by + .42))), (40, 60, 40), -1)
    for _ in range(int(r.integers(10, 16))):                                         # posters, frames, signs
        x, y = int(r.integers(0, w - 220)), int(r.integers(0, h - 200))
        pw, ph = int(r.integers(60, 220)), int(r.integers(60, 200))
        col = tuple(int(c) for c in r.integers(0, 255, 3))
        cv2.rectangle(img, (x, y), (x + pw, y + ph), col, -1)
        cv2.rectangle(img, (x, y), (x + pw, y + ph), (20, 20, 20), 3)
        for k in range(int(r.integers(1, 4))):
            cv2.putText(img, str(r.choice(WORDS)), (x + 6, y + 30 + 28 * k), cv2.FONT_HERSHEY_SIMPLEX, 0.7,
                        tuple(int(c) for c in r.integers(0, 255, 3)), 2)
        cv2.circle(img, (int(r.integers(0, w)), int(r.integers(0, h))), int(r.integers(10, 40)),
                   tuple(int(c) for c in r.integers(0, 255, 3)), -1)
    dx = float(r.choice([r.uniform(.02, .08), r.uniform(.84, .9)]))                  # door, left or right
    cv2.rectangle(img, (int(w * dx), int(h * .25)), (int(w * (dx + .1)), h - 1), (90, 60, 30), -1)
    return img


def view(scene: np.ndarray, seed: int, angle: float = 0.18, light: float = 1.0, blur: float = 0.0,
         out=(1280, 960), quality: int = 85) -> np.ndarray:
    """The wall photographed from another seat: perspective, framing, light, blur, JPEG.
    The wall overfills the frame, as in a real photo (no artificial borders)."""
    r = np.random.default_rng(seed)
    h, w = scene.shape[:2]
    j = lambda s: float(r.uniform(-s, s))
    src = np.float32([[0, 0], [w, 0], [w, h], [0, h]])
    a = angle
    dst = np.float32([[out[0] * (j(.06) + a * .5), out[1] * j(.06)], [out[0] * (1 + j(.06)), out[1] * (j(.06) + a * .4)],
                      [out[0] * (1 + j(.06)), out[1] * (1 - a * .4 + j(.06))], [out[0] * (j(.06) + a * .5), out[1] * (1 + j(.06))]])
    c = np.float32(out) / 2
    dst = (dst - c) * 1.2 + c
    img = cv2.warpPerspective(scene, cv2.getPerspectiveTransform(src, dst), out, borderMode=cv2.BORDER_REFLECT)
    img = np.clip(img.astype(np.float32) * light, 0, 255).astype(np.uint8)
    if blur:
        img = cv2.GaussianBlur(img, (0, 0), blur)
    img = np.clip(img + r.normal(0, 3, img.shape), 0, 255).astype(np.uint8)
    return cv2.imdecode(cv2.imencode(".jpg", img, [cv2.IMWRITE_JPEG_QUALITY, quality])[1], cv2.IMREAD_COLOR)


def jpeg(img: np.ndarray, q: int = 88) -> bytes:
    return cv2.imencode(".jpg", img, [cv2.IMWRITE_JPEG_QUALITY, q])[1].tobytes()


def with_screen(img: np.ndarray, text: str, center=(640, 330), side=220) -> tuple[np.ndarray, list]:
    """Projects the app's present screen (title, QR, counter) into a photo; returns corners."""
    q = qrcode.QRCode(error_correction=qrcode.constants.ERROR_CORRECT_M, border=0, box_size=1)
    q.add_data(text)
    q.make()
    m = (1 - np.array(q.get_matrix(), np.uint8)) * 255
    code = cv2.resize(m, (side, side), interpolation=cv2.INTER_NEAREST)
    out = img.copy()
    cx, cy = center
    x0, y0 = cx - side // 2, cy - side // 2
    cv2.rectangle(out, (x0 - 90, y0 - 60), (x0 + side + 90, y0 + side + 110), (245, 245, 250), -1)  # projected page
    cv2.putText(out, "Software Engineering", (x0 - 70, y0 - 20), cv2.FONT_HERSHEY_SIMPLEX, 0.8, (30, 30, 40), 2)
    out[y0:y0 + side, x0:x0 + side] = cv2.cvtColor(code, cv2.COLOR_GRAY2BGR)
    cv2.putText(out, "12 checked in", (x0 + 10, y0 + side + 45), cv2.FONT_HERSHEY_SIMPLEX, 0.8, (30, 120, 60), 2)
    cv2.rectangle(out, (x0 + 60, y0 + side + 65), (x0 + 160, y0 + side + 100), (200, 200, 210), -1)
    cv2.putText(out, "Exit", (x0 + 85, y0 + side + 92), cv2.FONT_HERSHEY_SIMPLEX, 0.7, (30, 30, 40), 2)
    corners = [[x0, y0], [x0 + side, y0], [x0 + side, y0 + side], [x0, y0 + side]]
    return out, corners

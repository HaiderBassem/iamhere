"""Writes a Y4M video for Chromium's fake camera (--use-file-for-fake-video-capture):
a procedural room seen from one seat, optionally with the app's screen showing a
given check-in link as a QR code. Used by the web app's end-to-end tests.

    python tests/camfeed.py OUT.y4m --room 1 --seat 100 [--code URL]
"""
import argparse

import cv2
import numpy as np

from scenes import view, wall, with_screen

W, H = 1280, 720


def frames(room: int, seat: int, code: str | None, n: int = 4):
    angle = 0.0 if seat >= 100 else 0.05 + 0.03 * (seat % 4)
    base = view(wall(room), seat, angle, out=(W, H))
    if code:
        base, _ = with_screen(base, code, center=(W // 2, 300), side=220)
    rng = np.random.default_rng(seat)
    for _ in range(n):   # a little sensor noise, different every frame
        yield np.clip(base + rng.normal(0, 2, base.shape), 0, 255).astype(np.uint8)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("out")
    ap.add_argument("--room", type=int, required=True)
    ap.add_argument("--seat", type=int, required=True, help=">= 100: straight on (the organizer's photos)")
    ap.add_argument("--code", help="check-in link shown on the room's screen")
    a = ap.parse_args()
    with open(a.out, "wb") as f:
        f.write(f"YUV4MPEG2 W{W} H{H} F10:1 Ip A1:1 C420jpeg\n".encode())
        for img in frames(a.room, a.seat, a.code):
            f.write(b"FRAME\n")
            f.write(cv2.cvtColor(img, cv2.COLOR_BGR2YUV_I420).tobytes())


if __name__ == "__main__":
    main()

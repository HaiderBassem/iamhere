"""Is a photo good enough to serve as a reference for a room?

Reference photos are checked when they are added, so a blurry, dark or
featureless photo is refused with a reason the organizer can act on, instead
of silently making the daily check fail later.
"""
from __future__ import annotations

import cv2
import numpy as np

MIN_SIDE = 320
MIN_BRIGHTNESS = 35        # mean luma, 0-255
MIN_SHARPNESS = 25.0       # variance of the Laplacian at 640 px
MIN_KEYPOINTS = 400


def problems(img: np.ndarray, keypoints: int) -> list[str]:
    h, w = img.shape[:2]
    if min(h, w) < MIN_SIDE:
        return ["too_small"]
    g = cv2.cvtColor(img, cv2.COLOR_BGR2GRAY)
    s = 640 / max(h, w)
    if s < 1:
        g = cv2.resize(g, (int(w * s), int(h * s)), interpolation=cv2.INTER_AREA)
    out = []
    if g.mean() < MIN_BRIGHTNESS:
        out.append("too_dark")
    if cv2.Laplacian(g, cv2.CV_64F).var() < MIN_SHARPNESS:
        out.append("blurry")
    if keypoints < MIN_KEYPOINTS:
        out.append("too_little_detail")
    return out


MESSAGES = {
    "too_small": "The photo is too small.",
    "too_dark": "The photo is too dark. Turn on the lights or face away from windows.",
    "blurry": "The photo is blurry. Hold the phone still and try again.",
    "too_little_detail": "The photo shows too little detail. Include the board, walls, doors and windows, not just a plain wall.",
}

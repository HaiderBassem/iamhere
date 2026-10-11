"""Reading the check-in QR code inside a photo.

A strict-mode attendee photographs the projector screen. The photo is only
useful as proof if the *current* code is readable in it: that shows the camera
was pointed at a live display within the last few seconds, not at an old photo.

The browser already found the code (jsQR) and reports its four corners. Those
corners are only a hint: we rectify the region around them and decode it
ourselves with zxing-cpp, which reads codes down to ~2-3 pixels per module
(measured; OpenCV's decoder needs about twice that). Without a usable hint we
fall back to searching the whole image.
"""
from __future__ import annotations

import cv2
import numpy as np
import zxingcpp


def _decode(img: np.ndarray) -> list[tuple[str, np.ndarray]]:
    out = []
    for r in zxingcpp.read_barcodes(img, formats=zxingcpp.BarcodeFormat.QRCode):
        p = r.position
        quad = np.float32([[p.top_left.x, p.top_left.y], [p.top_right.x, p.top_right.y],
                           [p.bottom_right.x, p.bottom_right.y], [p.bottom_left.x, p.bottom_left.y]])
        out.append((r.text, quad))
    return out


def _valid_quad(q: np.ndarray, w: int, h: int) -> bool:
    if q.shape != (4, 2) or not np.isfinite(q).all():
        return False
    if (q[:, 0] < -w * 0.1).any() or (q[:, 0] > w * 1.1).any() or (q[:, 1] < -h * 0.1).any() or (q[:, 1] > h * 1.1).any():
        return False
    return cv2.contourArea(q) > 100


def find_codes(img: np.ndarray, hint: list[list[float]] | None = None) -> list[tuple[str, np.ndarray]]:
    """Returns (text, corners in image coordinates) for every QR code read."""
    h, w = img.shape[:2]
    if hint is not None:
        try:
            q = np.float32(hint)
        except (TypeError, ValueError):
            q = np.zeros(0, np.float32)
        if _valid_quad(q, w, h):
            # rectify a square around the hinted code (with a quiet zone) at ~8 px/module
            S = 512
            pad = 0.18
            dst = np.float32([[S * pad, S * pad], [S * (1 - pad), S * pad], [S * (1 - pad), S * (1 - pad)], [S * pad, S * (1 - pad)]])
            try:
                M = cv2.getPerspectiveTransform(q, dst)
                crop = cv2.warpPerspective(img, M, (S, S), flags=cv2.INTER_CUBIC, borderValue=(255, 255, 255))
                found = _decode(crop)
                if found:
                    inv = np.linalg.inv(M)
                    return [(t, cv2.perspectiveTransform(c[None], inv)[0]) for t, c in found]
            except (cv2.error, np.linalg.LinAlgError):
                pass    # a twisted or degenerate hint: search the whole photo instead
    return _decode(img)


def mask_out(mask: np.ndarray, quad: np.ndarray) -> None:
    """Blanks the code and the projected page around it from feature matching.

    Every QR code shares the same finder patterns, and the projected page (title
    above, counter and buttons below) is identical in every session, so neither
    may count as evidence about the *room*.
    """
    c = quad.mean(axis=0)
    side = float(np.sqrt(cv2.contourArea(quad)))
    x0, x1 = int(c[0] - 0.95 * side), int(c[0] + 0.95 * side)
    y0, y1 = int(c[1] - 1.0 * side), int(c[1] + 1.3 * side)
    h, w = mask.shape
    mask[max(0, y0):min(h, y1), max(0, x0):min(w, x1)] = 0

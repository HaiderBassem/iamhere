"""Compact, versioned serialisation of a photo's features.

Used to remember a room from attendees' verified check-ins without keeping
their photos: only keypoint positions and descriptors are stored (half
precision descriptors), with the code and the app's page already masked out.

Layout (little-endian): b"IAHF1" | n:uint32 | width:float32 | height:float32 |
keypoints: n*2 float32 | descriptors: n*64 float16
"""
from __future__ import annotations

import struct

import numpy as np
import torch

from .matcher import Feats

MAGIC = b"IAHF1"
DIM = 64
MAX_POINTS = 4096
_HEAD = struct.Struct("<5sIff")


def pack(f: Feats) -> bytes:
    kp = f.keypoints.numpy().astype("<f4")   # positions stay exact: geometry is checked to 1.5 px
    desc = f.descriptors.numpy().astype("<f2")
    w, h = (float(x) for x in f.image_size[0].tolist())
    return _HEAD.pack(MAGIC, kp.shape[0], w, h) + kp.tobytes() + desc.tobytes()


def unpack(b: bytes) -> Feats:
    """Strictly validates: this data comes back from the database via the API."""
    if len(b) < _HEAD.size:
        raise ValueError("features: too short")
    magic, n, w, h = _HEAD.unpack_from(b)
    if magic != MAGIC or n > MAX_POINTS or not (0 < w <= 10000 and 0 < h <= 10000):
        raise ValueError("features: bad header")
    need = _HEAD.size + n * 2 * 4 + n * DIM * 2
    if len(b) != need:
        raise ValueError("features: wrong length")
    off = _HEAD.size
    kp = np.frombuffer(b, "<f4", n * 2, off).reshape(n, 2).copy()
    desc = np.frombuffer(b, "<f2", n * DIM, off + n * 8).reshape(n, DIM).astype(np.float32)
    if not (np.isfinite(kp).all() and np.isfinite(desc).all()):
        raise ValueError("features: not finite")
    if n and (kp.min() < -1 or kp[:, 0].max() > w + 1 or kp[:, 1].max() > h + 1):
        raise ValueError("features: keypoints outside the image")
    d = torch.from_numpy(desc)
    d = torch.nn.functional.normalize(d, dim=-1) if n else d   # half precision drifts slightly off unit length
    return Feats(torch.from_numpy(kp), d, torch.tensor([[w, h]], dtype=torch.float32))

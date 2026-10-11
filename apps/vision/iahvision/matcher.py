"""Recognising a room from two photos taken from different places in it.

Pipeline (chosen and calibrated on real indoor and landmark photos, see
docs/VISION.md):

1. XFeat local features (Potje et al., CVPR 2024; Apache-2.0), designed to run
   in real time on a CPU.
2. Correspondences, two ways:
   - fast: mutual nearest neighbours of the descriptors that also pass Lowe's
     ratio test (~40 ms per pair). The ratio test drops look-alike features
     (identical frames, tiles, signs) that a room has many of;
   - thorough: LighterGlue, the XFeat-trained LightGlue matcher (Apache-2.0),
     on the 1024 strongest features (~230 ms per pair). It only runs on the
     references the fast pass ranked closest, when the fast pass was not
     already conclusive.
3. Geometric verification: MAGSAC++ fits the fundamental matrix that any two
   views of one static scene must satisfy.
4. Score: the number of *distinct* places in both photos (grid cells) holding a
   geometrically consistent correspondence. Counting cells rather than
   correspondences means one straight edge (a door frame, blinds) or one
   cluster of texture cannot add up to a match: points along a single line
   satisfy the two-view constraint almost for free.

Same room = fast score >= FAST_THRESHOLD or thorough score >= THOROUGH_THRESHOLD.
Both sit well above the highest score measured between photos of different
places: 1601 real pairs, and synthetic rooms that share identical signs.

The sparse extraction below is adapted from the upstream XFeat inference code
(verlab/accelerated_features, Apache-2.0; see xfeat/LICENSE).
"""
from __future__ import annotations

import os
from dataclasses import dataclass

import cv2
import numpy as np
import torch
import torch.nn.functional as F
from kornia.feature.lightglue import LightGlue

from .xfeat.interpolator import InterpolateSparse2d
from .xfeat.model import XFeatModel

MODELS = os.environ.get("IAH_VISION_MODELS", os.path.join(os.path.dirname(__file__), "..", "models"))

# Calibrated values (docs/VISION.md); changing SIDE or TOP_K invalidates them.
SIDE = 640                # long side images are resized to before extraction
TOP_K = 2048              # features per photo (fast pass)
TOP_K_THOROUGH = 1024     # strongest features given to LighterGlue
MNN_MIN_SIM = 0.82        # cosine similarity floor for a fast-pass correspondence
RATIO = 0.9               # Lowe's ratio test: the best candidate must clearly beat the second
FAST_CELL, THOROUGH_CELL = 24, 32       # grid cell size (px at SIDE) for distinct places
FAST_THRESHOLD = 30       # highest between different places: 11 (1601 real pairs), 25 (synthetic rooms with identical signs)
THOROUGH_THRESHOLD = 24   # highest between different places: 16 (1601 real pairs), 19 (synthetic rooms with identical signs)
THOROUGH_CANDIDATES = 3   # references the thorough pass looks at

LIGHTGLUE_CONF = {
    "name": "xfeat", "input_dim": 64, "descriptor_dim": 96, "add_scale_ori": False, "add_laf": False,
    "scale_coef": 1.0, "n_layers": 6, "num_heads": 1, "flash": False, "mp": False,
    "depth_confidence": -1, "width_confidence": 0.95, "filter_threshold": 0.1, "weights": None,
}


@dataclass
class Feats:
    keypoints: torch.Tensor     # (N, 2) in resized-image pixels
    descriptors: torch.Tensor   # (N, 64), L2-normalised
    image_size: torch.Tensor    # (1, 2) width, height

    @property
    def count(self) -> int:
        return int(self.keypoints.shape[0])

    def top(self, k: int) -> "Feats":
        """The k strongest features (extract() orders them by strength)."""
        return Feats(self.keypoints[:k], self.descriptors[:k], self.image_size)


class Matcher:
    name = "xfeat-mnn+lighterglue-magsac-cells/1"

    # Scores are reported as a percentage of the threshold: 100 = just enough.
    threshold = 100

    def __init__(self, models_dir: str = MODELS):
        # parallelism comes from serving several requests at once, one core each
        torch.set_num_threads(int(os.environ.get("IAH_VISION_TORCH_THREADS", "1")))
        self.side, self.top_k = SIDE, TOP_K
        self.net = XFeatModel().eval()
        self.net.load_state_dict(torch.load(os.path.join(models_dir, "xfeat.pt"), map_location="cpu", weights_only=True))
        LightGlue.default_conf = LIGHTGLUE_CONF
        self.lg = LightGlue(None).eval()
        sd = torch.load(os.path.join(models_dir, "xfeat-lighterglue.pt"), map_location="cpu", weights_only=True)
        for i in range(LIGHTGLUE_CONF["n_layers"]):
            for a, b in ((f"self_attn.{i}", f"transformers.{i}.self_attn"), (f"cross_attn.{i}", f"transformers.{i}.cross_attn")):
                sd = {k.replace(a, b): v for k, v in sd.items()}
        sd = {k.replace("matcher.", ""): v for k, v in sd.items()}
        missing, unexpected = self.lg.load_state_dict(sd, strict=False)
        # only the computed confidence thresholds may be absent; anything else
        # means the weights did not load and the matcher would be random
        if [k for k in missing if k != "confidence_thresholds"] or unexpected:
            raise RuntimeError(f"LighterGlue weights did not load: missing={missing} unexpected={unexpected}")
        self.interp = InterpolateSparse2d("bicubic")
        self.nearest = InterpolateSparse2d("nearest")
        self.bilinear = InterpolateSparse2d("bilinear")

    # ---- preprocessing ------------------------------------------------------------
    def resize(self, img: np.ndarray) -> np.ndarray:
        h, w = img.shape[:2]
        s = self.side / max(h, w)
        return cv2.resize(img, (round(w * s), round(h * s)), interpolation=cv2.INTER_AREA) if s < 1 else img

    def valid_mask(self, img: np.ndarray) -> np.ndarray:
        """Where features may come from: not black borders (letterboxing,
        undistortion), not a thin margin at the very edge. Full-size coordinates."""
        g = cv2.cvtColor(img, cv2.COLOR_BGR2GRAY)
        m = ((g > 8) * 255).astype(np.uint8)
        k = max(3, int(0.012 * max(g.shape)) | 1)
        m = cv2.erode(m, np.ones((k, k), np.uint8))
        e = int(0.015 * max(g.shape))
        m[:e, :] = 0
        m[-e:, :] = 0
        m[:, :e] = 0
        m[:, -e:] = 0
        return m

    # ---- XFeat sparse extraction ---------------------------------------------------
    @torch.inference_mode()
    def extract(self, img: np.ndarray, mask: np.ndarray) -> Feats:
        small = self.resize(img)
        h0, w0 = small.shape[:2]
        mask = cv2.resize(mask, (w0, h0), interpolation=cv2.INTER_NEAREST)
        x = torch.from_numpy(small).permute(2, 0, 1)[None].float()
        H, W = (h0 // 32) * 32, (w0 // 32) * 32
        rh, rw = h0 / H, w0 / W
        x = F.interpolate(x, (H, W), mode="bilinear", align_corners=False)

        M1, K1, H1 = self.net(x)
        M1 = F.normalize(M1, dim=1)
        heat = F.softmax(K1, 1)[:, :64]
        B, _, h, w = heat.shape
        heat = heat.permute(0, 2, 3, 1).reshape(B, h, w, 8, 8).permute(0, 1, 3, 2, 4).reshape(B, 1, h * 8, w * 8)
        local_max = torch.nn.functional.max_pool2d(heat, 5, stride=1, padding=2)
        pos = ((heat == local_max) & (heat > 0.05))[0, 0].nonzero().flip(-1)[None]       # (1, N, 2) x,y
        if pos.shape[1] == 0:
            return Feats(torch.zeros(0, 2), torch.zeros(0, 64), torch.tensor([[w0, h0]], dtype=torch.float32))
        scores = (self.nearest(heat, pos, H, W) * self.bilinear(H1, pos, H, W)).squeeze(-1)[0]
        kp = pos[0].float() * torch.tensor([rw, rh])
        # drop keypoints outside the allowed area (borders, masked screen/QR)
        xi = kp[:, 0].long().clamp(0, w0 - 1)
        yi = kp[:, 1].long().clamp(0, h0 - 1)
        keep = torch.from_numpy(mask[yi.numpy(), xi.numpy()] > 0) & (scores > 0)
        idx = torch.nonzero(keep).squeeze(1)
        idx = idx[torch.argsort(-scores[idx])][: self.top_k]
        desc = F.normalize(self.interp(M1, pos[:, idx], H, W)[0], dim=-1)
        return Feats(kp[idx], desc, torch.tensor([[w0, h0]], dtype=torch.float32))

    # ---- matching + geometric verification ----------------------------------------
    @staticmethod
    def distinct(p0: np.ndarray, p1: np.ndarray, cell: int) -> int:
        """Geometrically consistent correspondences counted once per grid cell,
        taking the smaller count of the two photos."""
        if len(p0) < 8:
            return 0
        try:
            _, inl = cv2.findFundamentalMat(p0, p1, cv2.USAC_MAGSAC, 1.5, 0.999, 10000)
        except cv2.error:      # degenerate correspondences: no valid two-view geometry
            return 0
        if inl is None:
            return 0
        inl = inl.ravel().astype(bool)
        cells = lambda p: len(set(map(tuple, (p[inl] // cell).astype(np.int32).tolist())))
        return min(cells(p0), cells(p1))

    @torch.inference_mode()
    def fast_score(self, a: Feats, b: Feats) -> int:
        if a.count < 8 or b.count < 8:
            return 0
        sim = a.descriptors @ b.descriptors.T
        top2 = sim.topk(2, dim=1)
        best, i = top2.values[:, 0], top2.indices[:, 0]
        j = sim.argmax(0)
        # descriptor distances of the best and second-best candidate (unit vectors)
        d1, d2 = torch.sqrt(torch.clamp(2 - 2 * top2.values, min=0)).unbind(1)
        keep = (j[i] == torch.arange(len(i))) & (best > MNN_MIN_SIM) & (d1 < RATIO * d2)
        keep = torch.nonzero(keep).squeeze(1)
        p0 = a.keypoints[keep].numpy().astype(np.float32)
        p1 = b.keypoints[i[keep]].numpy().astype(np.float32)
        return self.distinct(p0, p1, FAST_CELL)

    @torch.inference_mode()
    def thorough_score(self, a: Feats, b: Feats) -> int:
        a, b = a.top(TOP_K_THOROUGH), b.top(TOP_K_THOROUGH)
        if a.count < 8 or b.count < 8:
            return 0
        out = self.lg({"image0": {"keypoints": a.keypoints[None], "descriptors": a.descriptors[None], "image_size": a.image_size},
                       "image1": {"keypoints": b.keypoints[None], "descriptors": b.descriptors[None], "image_size": b.image_size}})
        m = out["matches"][0].numpy()
        p0 = a.keypoints[m[:, 0]].numpy().astype(np.float32)
        p1 = b.keypoints[m[:, 1]].numpy().astype(np.float32)
        return self.distinct(p0, p1, THOROUGH_CELL)

    def compare(self, probe: Feats, refs: list[Feats]) -> list[int]:
        """Score (percent of threshold, 100 = same room) of the probe against each
        reference. Stops looking once one reference is conclusive."""
        pct = lambda s, t: int(round(100 * s / t))
        scores = [0] * len(refs)
        for k, r in enumerate(refs):
            scores[k] = pct(self.fast_score(probe, r), FAST_THRESHOLD)
            if scores[k] >= self.threshold:
                return scores
        for k in sorted(range(len(refs)), key=lambda k: -scores[k])[:THOROUGH_CANDIDATES]:
            scores[k] = max(scores[k], pct(self.thorough_score(probe, refs[k]), THOROUGH_THRESHOLD))
            if scores[k] >= self.threshold:
                break
        return scores

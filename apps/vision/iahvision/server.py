"""HTTP API of the vision service. Internal only: the Go API is its sole client.

POST /v1/reference {image}                    -> 200 {width,height,keypoints} | 422 {codes,message}
POST /v1/verify {probe, hint?, refs:[{sha256, image?}]}
                                              -> 200 {codes, matched, score, refs, model}
                                               | 409 {missing:[sha256]}  (POST those to /v1/reference, or
                                                                          inline as image, then ask again)
                                               | 422 {codes, message}    (retake the photo)
GET  /healthz                                 -> 200

Reference features are cached by the SHA-256 of the image bytes, so the API
only sends a reference image the first time (or after this service restarts).
Attendee photos are processed in memory and never written anywhere.
"""
from __future__ import annotations

import base64
import binascii
import hashlib
import json
import logging
import os
import threading
import time
from collections import OrderedDict
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import cv2
import numpy as np

from . import qr, quality
from .matcher import Matcher

log = logging.getLogger("iahvision")

MAX_BODY = 32 << 20          # a probe plus up to ~16 reference images (base64)
MAX_IMAGE = 2_000_000
MAX_REFS = 16


class Cache:
    """A small LRU of extracted reference features keyed by image SHA-256."""

    def __init__(self, size: int):
        self.size, self.d, self.lock = size, OrderedDict(), threading.Lock()

    def get(self, k):
        with self.lock:
            v = self.d.get(k)
            if v is not None:
                self.d.move_to_end(k)
            return v

    def put(self, k, v):
        with self.lock:
            self.d[k] = v
            self.d.move_to_end(k)
            while len(self.d) > self.size:
                self.d.popitem(last=False)


class HTTPError(Exception):
    def __init__(self, status: int, body: dict):
        super().__init__(body.get("error", "error"))
        self.status, self.body = status, body


def decode_image(b64: str) -> tuple[np.ndarray, bytes]:
    try:
        raw = base64.b64decode(b64, validate=True)
    except (binascii.Error, ValueError):
        raise HTTPError(400, {"error": "bad_image", "message": "Malformed image."})
    if not raw or len(raw) > MAX_IMAGE:
        raise HTTPError(422, {"error": "photo_unusable", "codes": ["too_large"], "message": "That photo is too large."})
    img = cv2.imdecode(np.frombuffer(raw, np.uint8), cv2.IMREAD_COLOR)
    if img is None:
        raise HTTPError(422, {"error": "photo_unusable", "codes": ["not_an_image"], "message": "That file is not a usable photo."})
    if img.shape[0] * img.shape[1] > 24_000_000:
        raise HTTPError(422, {"error": "photo_unusable", "codes": ["too_large"], "message": "That photo is too large."})
    return img, raw


class Service:
    def __init__(self, matcher: Matcher, cache_size: int = 256, workers: int | None = None):
        self.m = matcher
        self.cache = Cache(cache_size)
        # bound concurrent heavy work; extra requests wait their turn
        self.slots = threading.BoundedSemaphore(workers or max(1, (os.cpu_count() or 2)))

    def reference_features(self, img: np.ndarray, raw: bytes):
        """Features of a reference photo; any check-in code shown on its screen is masked out."""
        mask = self.m.valid_mask(img)
        for _, quad in qr.find_codes(img):
            qr.mask_out(mask, quad)
        return self.m.extract(img, mask)

    def reference(self, body: dict) -> dict:
        img, raw = decode_image(body.get("image", ""))
        with self.slots:
            feats = self.reference_features(img, raw)
        # cached even when it is criticised below: the API also uses this to
        # re-send references it already stores (after a restart), whatever the
        # quality gate says today
        self.cache.put(hashlib.sha256(raw).hexdigest(), feats)
        probs = quality.problems(img, feats.count)
        if probs:
            raise HTTPError(422, {"error": "photo_unusable", "codes": probs,
                                  "message": " ".join(quality.MESSAGES[p] for p in probs)})
        return {"width": int(img.shape[1]), "height": int(img.shape[0]), "keypoints": int(feats.count)}

    def verify(self, body: dict) -> dict:
        refs = body.get("refs") or []
        if not isinstance(refs, list) or len(refs) > MAX_REFS:
            raise HTTPError(400, {"error": "bad_refs", "message": "Too many references."})
        # collect reference features; ask for any image we do not have cached
        feats, missing = [], []
        for r in refs:
            sha = str(r.get("sha256", "")).lower()
            f = self.cache.get(sha)
            if f is None and r.get("image"):
                img, raw = decode_image(r["image"])
                if hashlib.sha256(raw).hexdigest() != sha:
                    raise HTTPError(400, {"error": "bad_refs", "message": "Reference image does not match its hash."})
                with self.slots:
                    f = self.reference_features(img, raw)
                self.cache.put(sha, f)
            if f is None:
                missing.append(sha)
            feats.append((sha, f))
        if missing:
            raise HTTPError(409, {"error": "missing_refs", "missing": missing})

        probe, _ = decode_image(body.get("probe", ""))
        hint = body.get("hint")
        t0 = time.monotonic()
        with self.slots:
            codes = qr.find_codes(probe, hint)
            mask = self.m.valid_mask(probe)
            for _, quad in codes:
                qr.mask_out(mask, quad)
            pf = self.m.extract(probe, mask)
            if pf.count < 30:
                raise HTTPError(422, {"error": "photo_unusable", "codes": ["too_little_detail"],
                                      "message": "The photo shows too little of the room. Include the screen and its surroundings."})
            per = self.m.compare(pf, [f for _, f in feats])
        scores = [{"sha256": sha, "score": s, "matched": s >= self.m.threshold} for (sha, _), s in zip(feats, per)]
        best = max(per, default=0)
        log.info("verify refs=%d best=%d codes=%d ms=%d", len(feats), best, len(codes), (time.monotonic() - t0) * 1000)
        return {"codes": [t for t, _ in codes], "matched": best >= self.m.threshold, "score": int(best),
                "refs": scores, "threshold": self.m.threshold, "model": self.m.name}


def make_handler(svc: Service):
    class Handler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def log_message(self, fmt, *args):  # quiet default access log; we log per request ourselves
            pass

        def _send(self, status: int, body: dict):
            b = json.dumps(body).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(b)))
            self.end_headers()
            self.wfile.write(b)

        def do_GET(self):
            if self.path == "/healthz":
                return self._send(200, {"ok": True, "model": svc.m.name})
            self._send(404, {"error": "not_found"})

        def do_POST(self):
            try:
                n = int(self.headers.get("Content-Length") or 0)
                if n <= 0 or n > MAX_BODY:
                    self.close_connection = True   # the unread body must not be taken for the next request
                    raise HTTPError(413, {"error": "too_large", "message": "Request too large."})
                body = json.loads(self.rfile.read(n))
                if not isinstance(body, dict):
                    raise HTTPError(400, {"error": "bad_request", "message": "Malformed request."})
                if self.path == "/v1/reference":
                    return self._send(200, svc.reference(body))
                if self.path == "/v1/verify":
                    return self._send(200, svc.verify(body))
                raise HTTPError(404, {"error": "not_found"})
            except HTTPError as e:
                self._send(e.status, e.body)
            except (ValueError, json.JSONDecodeError):
                self._send(400, {"error": "bad_request", "message": "Malformed request."})
            except Exception:  # never leak internals; the API treats 5xx as "check unavailable"
                log.exception("request failed")
                self._send(500, {"error": "internal"})

    return Handler


def main():
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
    svc = Service(Matcher(), cache_size=int(os.environ.get("IAH_VISION_CACHE", "256")),
                  workers=int(os.environ.get("IAH_VISION_WORKERS", "0")) or None)
    addr = os.environ.get("IAH_VISION_LISTEN", "0.0.0.0:8090")
    host, port = addr.rsplit(":", 1)
    srv = ThreadingHTTPServer((host, int(port)), make_handler(svc))
    srv.daemon_threads = True
    log.info("vision service listening on %s (model %s)", addr, svc.m.name)
    srv.serve_forever()


if __name__ == "__main__":
    main()

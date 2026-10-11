import base64
import hashlib
import json
import threading
import urllib.error
import urllib.request

import cv2
import numpy as np
import pytest

from iahvision import qr, quality
from iahvision.matcher import Matcher
from iahvision.server import Service, ThreadingHTTPServer, make_handler

from scenes import jpeg, view, wall, with_screen

b64 = lambda b: base64.b64encode(b).decode()


@pytest.fixture(scope="module")
def matcher():
    return Matcher()


@pytest.fixture(scope="module")
def server(matcher):
    svc = Service(matcher, cache_size=8, workers=2)
    srv = ThreadingHTTPServer(("127.0.0.1", 0), make_handler(svc))
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    yield f"http://127.0.0.1:{srv.server_address[1]}"
    srv.shutdown()


def post(base, path, body):
    req = urllib.request.Request(base + path, json.dumps(body).encode(), {"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=60) as r:
            return r.status, json.loads(r.read())
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read() or b"{}")


def score(m, a, b):
    return m.compare(m.extract(a, m.valid_mask(a)), [m.extract(b, m.valid_mask(b))])[0]


def test_same_room_from_other_seats_matches(matcher):
    room = wall(1)
    ref = view(room, 100, angle=0.0)
    for seed, angle, light, blur in [(1, 0.15, 1.0, 0), (2, 0.25, 0.7, 0), (3, 0.2, 1.25, 0.8), (4, 0.3, 0.85, 1.2)]:
        s = score(matcher, view(room, seed, angle, light, blur), ref)
        assert s >= matcher.threshold, (seed, s)


def test_different_rooms_do_not_match(matcher):
    ref = view(wall(1), 100, angle=0.0)
    for seed in range(2, 22):
        s = score(matcher, view(wall(seed), seed, 0.2), ref)
        assert s < matcher.threshold, (seed, s)


def test_same_code_and_page_in_two_rooms_is_not_a_match(matcher):
    """A cheater at home photographing the app's page on their own screen must
    not borrow the QR code's and the page's features as 'room' evidence."""
    text = "https://attend.example.org/#/c/" + "A" * 54
    ref_img, ref_q = with_screen(view(wall(1), 100, 0.0), text)
    probe_img, probe_q = with_screen(view(wall(7), 7, 0.15), text, center=(600, 360), side=260)
    def masked(img, quad):
        m = matcher.valid_mask(img)
        qr.mask_out(m, np.float32(quad))
        return matcher.extract(img, m)
    assert matcher.compare(masked(probe_img, probe_q), [masked(ref_img, ref_q)])[0] < matcher.threshold


def test_code_is_read_inside_a_photo():
    text = "https://attend.example.org/#/c/" + "Zm9vYmFy" * 6 + "AbCdEf"
    img, corners = with_screen(view(wall(3), 3, 0.15), text, side=160)
    small = cv2.resize(img, (960, 720))
    hint = (np.float32(corners) * 0.75).tolist()
    assert [t for t, _ in qr.find_codes(small, hint)] == [text]
    assert [t for t, _ in qr.find_codes(small, None)] == [text]
    # a useless, twisted or malformed hint falls back to searching the whole photo
    for bad in ([[0, 0], [1, 0], [1, 1], [0, 1]], [hint[0], hint[2], hint[1], hint[3]], [[1e30, 0]] * 4, [[0, 0]] * 4,
                [["x", 1]] * 4, [1, 2, 3]):
        assert [t for t, _ in qr.find_codes(small, bad)] == [text], bad


def test_reference_quality_gate(matcher):
    ok = view(wall(2), 2, 0.0)
    assert quality.problems(ok, matcher.extract(ok, matcher.valid_mask(ok)).count) == []
    dark = (ok * 0.08).astype(np.uint8)
    assert "too_dark" in quality.problems(dark, 999)
    blurry = cv2.GaussianBlur(ok, (0, 0), 6)
    assert "blurry" in quality.problems(blurry, 999)
    plain = np.full((960, 1280, 3), 200, np.uint8)
    assert "too_little_detail" in quality.problems(plain, matcher.extract(plain, matcher.valid_mask(plain)).count)


def test_http_protocol(server, matcher):
    room = wall(5)
    ref = jpeg(view(room, 50, 0.0))
    sha = hashlib.sha256(ref).hexdigest()
    text = "https://attend.example.org/#/c/" + "B" * 54
    probe_img, corners = with_screen(view(room, 51, 0.2), text, center=(640, 300), side=200)
    probe = jpeg(probe_img)

    st, body = post(server, "/v1/reference", {"image": b64(ref)})
    assert st == 200 and body["keypoints"] > 0, body
    plain = jpeg(np.full((960, 1280, 3), 190, np.uint8))
    st, body = post(server, "/v1/reference", {"image": b64(plain)})
    assert st == 422 and "too_little_detail" in body["codes"], body
    # ...but its features are cached: the API re-sends stored references this way after a restart
    st, body = post(server, "/v1/verify", {"probe": b64(probe), "refs": [{"sha256": hashlib.sha256(plain).hexdigest()}]})
    assert st == 200 and body["matched"] is False, body

    # unknown reference -> 409 with its hash; then it is accepted with the image
    other = jpeg(view(wall(6), 60, 0.0))
    osha = hashlib.sha256(other).hexdigest()
    st, body = post(server, "/v1/verify", {"probe": b64(probe), "refs": [{"sha256": osha}]})
    assert st == 409 and body["missing"] == [osha]
    st, body = post(server, "/v1/verify", {"probe": b64(probe), "refs": [{"sha256": osha, "image": b64(other)}]})
    assert st == 200 and body["matched"] is False
    # or uploaded on its own first (what the API does)
    third = jpeg(view(wall(8), 80, 0.0))
    tsha = hashlib.sha256(third).hexdigest()
    st, body = post(server, "/v1/verify", {"probe": b64(probe), "refs": [{"sha256": tsha}, {"sha256": osha}]})
    assert st == 409 and body["missing"] == [tsha]
    assert post(server, "/v1/reference", {"image": b64(third)})[0] == 200
    st, body = post(server, "/v1/verify", {"probe": b64(probe), "refs": [{"sha256": tsha}, {"sha256": osha}]})
    assert st == 200 and body["matched"] is False

    st, body = post(server, "/v1/verify", {"probe": b64(probe), "hint": corners, "refs": [{"sha256": sha}]})
    assert st == 200 and body["matched"] is True and body["codes"] == [text], body

    # an image that does not match its claimed hash is refused
    st, _ = post(server, "/v1/verify", {"probe": b64(probe), "refs": [{"sha256": "0" * 64, "image": b64(other)}]})
    assert st == 400
    # garbage
    st, _ = post(server, "/v1/verify", {"probe": "!!!", "refs": []})
    assert st == 400
    st, body = post(server, "/v1/verify", {"probe": b64(b"not an image"), "refs": []})
    assert st == 422

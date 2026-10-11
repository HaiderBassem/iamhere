# Strict mode's photo check

Strict mode asks each attendee for a photo of the screen showing the check-in code. Two things are checked on
that photo, by the vision service (`apps/vision`, Python, CPU only, internal to the server):

1. **The current code is in the photo.** The service reads every QR code in the photo; the API accepts it only if
   it is a check-in code of *this* session from the last 3 rotations (~15 s). This proves the photo was taken
   live, in view of the organizer's screen: an old photo, a photo of another session or a photo without the code
   cannot pass.
2. **The room around the screen is this room.** The photo is compared with reference photos of the room: the
   place's saved photos (up to 12) and the session's "today's photo" (up to 4, taken by the organizer when the
   session starts). This catches the code being shown elsewhere (shared to someone at home, a stream).

Only both together, plus GPS inside the place, give `verified`. Anything short of that goes to the organizer's
review with a plain reason; the photo check alone never rejects anyone.

| Situation | Result | Reason code |
|---|---|---|
| Right room, current code, GPS inside | verified | |
| Photo of another room (with the current code) | needs review | `visual_check_required` |
| Photo without the current code (old photo, code unreadable) | needs review | `photo_without_current_code` |
| No photo (skipped) | needs review | `visual_check_required` |
| Vision service down or overloaded | needs review | `visual_check_unavailable` |
| No reference photos at all | needs review | `visual_not_configured` |
| Too dark / blurry / shows almost nothing | the attendee is asked to retake it | (HTTP 400 `photo_unusable`) |

## How the room is recognised

1. **Features.** XFeat (Potje et al., CVPR 2024), a learned local-feature network made to run on a CPU; up to
   2048 features per photo, long side resized to 640 px. Weights: `apps/vision/models`, Apache-2.0
   (`iahvision/xfeat/LICENSE`, upstream commit in `iahvision/xfeat/UPSTREAM`).
2. **Masking.** The QR code and the app's page around it are blanked in every photo (references too) before
   features are taken, so the code and page, which are identical in every room, never count as evidence of the
   room. The browser reports where it saw the code; the service also finds it itself.
3. **Correspondences, two passes.**
   *Fast*: mutual nearest neighbours of the descriptors that also pass Lowe's ratio test (0.9); ~40 ms per
   reference. *Thorough*: LighterGlue, the XFeat-trained LightGlue matcher, on the 1024 strongest features;
   ~230 ms per reference. The thorough pass only runs, on the 3 references the fast pass ranked closest, when the
   fast pass was not already conclusive.
4. **Geometry.** MAGSAC++ fits the fundamental matrix that any two views of one static scene must satisfy.
5. **Score.** Consistent correspondences counted once per grid cell (24 px fast, 32 px thorough) in both photos,
   taking the smaller. Counting places instead of points means one straight edge (a door frame, blinds) or one
   textured patch cannot add up to a match.
6. **Decision.** Same room when fast ≥ 30 or thorough ≥ 24. Reported as a percentage of the threshold
   (100 = just enough), which is what "Test from a seat" shows.

## Measurements

Real photos: 60 images in 20 places (ScanNet indoor rooms, Phototourism landmarks, the TUM `freiburg` hand-held
office sequence; the SuperGlue repository's sample sets, not redistributed here). All 1601 pairs of photos of
*different* places are the negatives; same-place pairs are the positives (TUM: frames at least 3 apart).

**Different places (false matches).** 0 of 1601 at the chosen thresholds. Highest scores seen: fast 11, thorough
16 (thresholds 30 and 24). Synthetic rooms that share identical signs, frames and fonts (`tests/scenes.py`, the
hardest case we could construct) reach fast 25 and thorough 19, still below.

**Same place (found)**, per attendee photo, by number of reference photos of the place:

| | 1 reference | 2 | 3 |
|---|---|---|---|
| Landmarks (Phototourism, 13 photos, 4 places) | 68% | 77% | 87% |
| Office, hand-held (TUM, 17 frames) | 46% | 71% | 83% |
| Indoor, very wide baseline (ScanNet pairs) | 13% | n/a | n/a |

These benchmark pairs are much harder than daily use: they include opposite viewing directions, large zoom and
motion blur, while a check-in photo is aimed at the front of the room, the same part the reference photos show.
The practical levers are, in order: **3–4 reference photos from different seats**, a **photo of today** (same
light, same layout), and attendees including the wall around the screen. The web app asks for exactly this.

**Earlier approaches measured and rejected:** a perceptual difference hash (the previous implementation) does not
separate rooms from different seats at all; SIFT with the same verification found fewer same-place pairs (TUM
35–38/105 vs 46–53); XFeat without the ratio test let different places reach 24 (and 50+ in the synthetic rooms).

**Reading the code in the photo.** zxing-cpp with the browser's corner hint reads the current check-in code in
100% of test photos down to 2.7 px per QR module and 90% at 2 px (OpenCV's reader needs ≥ 6 px). The web app
asks the camera for its highest practical resolution (up to 2560×1440) so a projected code keeps enough pixels;
it has not been measured in real rooms yet, so "Test from a seat" from the back row is worth doing once.

**Speed** (one CPU core): extraction 75 ms per photo; a check that matches ~0.1 s; the worst case (16
references, none matching, three thorough comparisons) ~2 s. The service runs one check per core at a time
(`IAH_VISION_WORKERS`); the API waits up to 20 s and otherwise sends the attempt to review
(`visual_check_unavailable`), never to verified.

## Running it

`infra/docker-compose.yml` runs it as the `vision` service (internal network only; `IAH_VISION_URL` on the API).
Without `IAH_VISION_URL` the API logs a warning and strict check-ins go to review. Locally:

```sh
cd apps/vision
pip install --index-url https://download.pytorch.org/whl/cpu torch==2.14.1
pip install -r requirements-dev.txt
python -m pytest -q                      # QR reading, rooms, masking, quality gate, HTTP protocol
python -m iahvision.server               # listens on :8090 (IAH_VISION_LISTEN)
```

Protocol: `POST /v1/reference {image}` validates a reference (light, sharpness, detail) and caches its features
(even when it complains about them); `POST /v1/verify {probe, hint?, refs:[{sha256}]}` answers 409 with the
hashes it has no features for (first use, or after a restart). The API then uploads each missing reference to
`/v1/reference`, one at a time and shared between check-ins that need the same one, and asks again. Features
are cached by image hash (LRU, `IAH_VISION_CACHE`, default 256).

## Data kept

- Reference photos are stored in PostgreSQL (`reference_photos`, ≤ 2 MB each, at least 320×240). Session photos
  are deleted 30 days after the session ends; a place's photos live until removed (or the place is deleted).
  Only organizers of the organization can see them.
- Attendee photos are never stored, by the API or the service. The attempt keeps the evidence only: the score,
  whether the room matched, whether the current code was in the photo, and the model version.

## Limits

- A browser cannot prove presence. Someone in the room can stream the screen and the room to someone elsewhere,
  who photographs their own screen: the code is current and the room is right. GPS (fresh fix, inside the place)
  is the check against that; together they raise the cost of cheating, they do not make it impossible.
- Two rooms that really look the same (same furniture, same posters, same layout) may be recognised as each
  other. GPS and the radius tell neighbouring buildings apart, not neighbouring rooms.
- A room that changed a lot since its saved photos (rearranged, decorated) is recognised less often; today's photo
  fixes that for the session. "Test from a seat" on the Places screen shows how strongly a seat matches.
- The thresholds come from the measurements above, not from your rooms. If organizers see many genuine
  attendees in review, look at the scores in the attempts' evidence before changing anything.

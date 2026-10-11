// Camera feeds for Chromium's fake camera (Y4M). Rooms come from
// apps/vision/tests/camfeed.py; this one is a QR code held up to the lens.
import { writeFileSync, readFileSync } from 'node:fs';

// A camera feed that shows a QR code held in front of the lens (Y4M, 640x480).
export function writeQRVideo(path, text, W = 640, H = 480, frames = 6) {
  // the vendored library is a UMD script: evaluate it as CommonJS (this folder is "type": "module")
  const mod = { exports: {} };
  new Function('module', 'exports', readFileSync(new URL('../public/vendor/qrcode.js', import.meta.url), 'utf8'))(mod, mod.exports);
  const qrcode = mod.exports;
  const q = qrcode(0, 'M'); q.addData(text); q.make();
  const n = q.getModuleCount(), cell = Math.floor((H - 40) / (n + 8)), size = cell * (n + 8);
  const ox = Math.floor((W - size) / 2), oy = Math.floor((H - size) / 2);
  const y = Buffer.alloc(W * H, 120);                         // mid-grey table
  for (let j = 0; j < size; j++) for (let i = 0; i < size; i++) {   // white card with quiet zone
    const r = Math.floor(j / cell) - 4, c = Math.floor(i / cell) - 4;
    const dark = r >= 0 && c >= 0 && r < n && c < n && q.isDark(r, c);
    y[(oy + j) * W + ox + i] = dark ? 16 : 235;
  }
  const chroma = Buffer.alloc((W / 2) * (H / 2), 128);
  const chunks = [Buffer.from(`YUV4MPEG2 W${W} H${H} F10:1 Ip A1:1 C420jpeg\n`)];
  for (let f = 0; f < frames; f++) chunks.push(Buffer.from('FRAME\n'), y, chroma, chroma);
  writeFileSync(path, Buffer.concat(chunks));
}

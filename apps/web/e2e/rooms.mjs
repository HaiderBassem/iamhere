// Generates two different synthetic "rooms" as Y4M video files that Chromium's
// fake camera can play, so the strict-mode photo check sees a real scene
// (room A) or a different place (room B).
import { writeFileSync, readFileSync } from 'node:fs';

function lcg(seed) { let s = seed >>> 0; return () => ((s = (Math.imul(s, 1664525) + 1013904223) >>> 0) / 2 ** 32); }

export function roomFrame(seed, W, H, frame = 0) {
  const r = lcg(seed);
  const blocks = Array.from({ length: 14 }, () => {
    const x0 = Math.floor(r() * W * 0.75), y0 = Math.floor(r() * H * 0.75);
    return { x0, y0, x1: x0 + 40 + Math.floor(r() * W / 3), y1: y0 + 40 + Math.floor(r() * H / 3), l: Math.floor(r() * 256) };
  });
  const base = 40 + Math.floor(r() * 120);
  const n = lcg(seed * 7 + frame);
  const y = Buffer.alloc(W * H);
  for (let j = 0; j < H; j++) for (let i = 0; i < W; i++) {
    let l = base + Math.floor(j * 60 / H);
    for (const b of blocks) if (i >= b.x0 && i < b.x1 && j >= b.y0 && j < b.y1) l = b.l;
    l += Math.floor(n() * 9) - 4;                 // a little sensor noise, different every frame
    y[j * W + i] = Math.max(16, Math.min(235, l));
  }
  return y;
}

export function writeY4M(path, seed, W = 640, H = 480, frames = 6) {
  const chunks = [Buffer.from(`YUV4MPEG2 W${W} H${H} F10:1 Ip A1:1 C420jpeg\n`)];
  const chroma = Buffer.alloc((W / 2) * (H / 2), 128);
  for (let f = 0; f < frames; f++) chunks.push(Buffer.from('FRAME\n'), roomFrame(seed, W, H, f), chroma, chroma);
  writeFileSync(path, Buffer.concat(chunks));
}

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

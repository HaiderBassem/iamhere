// Tiny DOM helpers. Everything is built with textContent / attributes, never
// innerHTML, so user-supplied names cannot inject markup.
import { t } from './i18n.js';

export function h(tag, attrs, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === false || v == null) continue;
    if (k === 'class') el.className = v;
    else if (k.startsWith('on') && typeof v === 'function') el.addEventListener(k.slice(2).toLowerCase(), v);
    else if (k === 'value') el.value = v;
    else if (v === true) el.setAttribute(k, '');
    else el.setAttribute(k, v);
  }
  add(el, kids);
  return el;
}
function add(el, kids) {
  for (const k of kids.flat(Infinity)) {
    if (k == null || k === false) continue;
    el.append(k.nodeType ? k : document.createTextNode(String(k)));
  }
}
export function clear(el) { el.replaceChildren(); return el; }
export function mount(el, ...kids) { clear(el); add(el, kids); return el; }

const SVG = 'http://www.w3.org/2000/svg';
const PATHS = {
  home: 'M3 11.5 12 4l9 7.5M5.5 10v9.500h13V10',
  scan: 'M4 8V5.500A1.500 1.500 0 0 1 5.500 4H8M16 4h2.500A1.500 1.500 0 0 1 20 5.500V8M20 16v2.500a1.500 1.500 0 0 1-1.500 1.500H16M8 20H5.500A1.500 1.500 0 0 1 4 18.500V16M4 12h16',
  user: 'M12 12a4 4 0 1 0 0-8 4 4 0 0 0 0 8ZM4.500 20c.8-3.600 3.800-5.500 7.500-5.500s6.700 1.900 7.500 5.500',
  check: 'm5 12.500 4.500 4.500L19 7.500',
  x: 'M6 6l12 12M18 6 6 18',
  warn: 'M12 8v5M12 16.500v.01M10.300 4.200 2.700 17.500A2 2 0 0 0 4.400 20.500h15.200a2 2 0 0 0 1.700-3L13.700 4.200a2 2 0 0 0-3.400 0Z',
  plus: 'M12 5v14M5 12h14',
  back: 'm15 6-6 6 6 6',
  copy: 'M9 9h10v11H9zM5 15V4h10',
  share: 'M12 4v11M8 8l4-4 4 4M5 13v6h14v-6',
  download: 'M12 4v11M8 11l4 4 4-4M5 19h14',
  expand: 'M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5',
  pin: 'M12 21s7-6 7-11a7 7 0 1 0-14 0c0 5 7 11 7 11Zm0-8.500a2.500 2.500 0 1 0 0-5 2.500 2.500 0 0 0 0 5Z',
  camera: 'M4 8h3l1.500-2h7L17 8h3v11H4zM12 17a3.500 3.500 0 1 0 0-7 3.500 3.500 0 0 0 0 7Z',
  people: 'M9 11a3.500 3.500 0 1 0 0-7 3.500 3.500 0 0 0 0 7ZM2.500 20c.6-3.200 3-5 6.500-5s5.900 1.800 6.500 5M17 11a3 3 0 1 0 0-6M17.500 15c2.300.3 3.600 1.800 4 4.500',
  logout: 'M15 4h3a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2h-3M10 16l-4-4 4-4M6 12h10',
  mail: 'M4 6h16v12H4zM4 7l8 6 8-6',
  eye: 'M2.500 12S6 5.500 12 5.500 21.500 12 21.500 12 18 18.500 12 18.500 2.500 12 2.500 12ZM12 14.500a2.500 2.500 0 1 0 0-5 2.500 2.500 0 0 0 0 5Z',
  eyeOff: 'M3 3l18 18M10.600 6a9.800 9.800 0 0 1 1.400-.1c6 0 9.500 6.100 9.500 6.100a17 17 0 0 1-2.600 3.300M6.600 6.700C4 8.300 2.500 12 2.500 12S6 18.500 12 18.500a9 9 0 0 0 4.600-1.300M9.900 9.900a3 3 0 0 0 4.200 4.200',
  shield: 'M12 3l7.500 3v5.500c0 4.600-3.200 8.300-7.500 9.500-4.300-1.200-7.500-4.900-7.500-9.500V6z',
  arrow: 'M5 12h14M13 6l6 6-6 6',
  qr: 'M4 4h6v6H4zM14 4h6v6h-6zM4 14h6v6H4zM14 14h2v2h-2zM18 14h2M14 18v2M18 18h2v2',
  building: 'M4 20V5l8-2v17M12 9h8v11M7.500 8h1M7.500 12h1M7.500 16h1M15.500 13h1M15.500 16.500h1M2.500 20h19',
  calendar: 'M4 6h16v14H4zM4 10h16M8 3v4M16 3v4',
  sparkle: 'M12 3l1.800 5.200L19 10l-5.200 1.800L12 17l-1.800-5.200L5 10l5.200-1.800z',
};
export function icon(name) {
  const s = document.createElementNS(SVG, 'svg');
  s.setAttribute('viewBox', '0 0 24 24');
  s.setAttribute('width', '24');   // sensible default everywhere; CSS overrides where a size is wanted
  s.setAttribute('height', '24');
  s.setAttribute('fill', 'none');
  s.setAttribute('stroke', 'currentColor');
  s.setAttribute('stroke-width', '2');
  s.setAttribute('stroke-linecap', 'round');
  s.setAttribute('stroke-linejoin', 'round');
  s.setAttribute('aria-hidden', 'true');
  const p = document.createElementNS(SVG, 'path');
  p.setAttribute('d', PATHS[name] || '');
  s.append(p);
  return s;
}

export const spinner = () => h('span', { class: 'spinner', role: 'status', 'aria-label': t('Loading') });
export const loading = () => h('div', { class: 'loading' }, spinner());

// ---- toasts & dialogs ------------------------------------------------------
let toastHost;
export function toast(message, kind = '') {
  if (!toastHost) {
    toastHost = h('div', { class: 'toasts', role: 'status' });
    document.body.append(toastHost);
  }
  const t = h('div', { class: 'toast ' + kind }, message);
  toastHost.append(t);
  setTimeout(() => t.remove(), kind === 'bad' ? 6000 : 3500);
}

export function confirmDialog({ title, body, confirm = t('Confirm'), cancel = t('Cancel'), danger = false, extra = null }) {
  return new Promise((resolve) => {
    const d = h('dialog', { 'aria-labelledby': 'dlg-title' },
      h('form', { method: 'dialog', class: 'stack' },
        h('h2', { id: 'dlg-title' }, title),
        body && h('p', { class: 'muted' }, body),
        extra,
        h('div', { class: 'row', style: 'justify-content:flex-end' },
          cancel && h('button', { class: 'btn secondary', value: 'no' }, cancel),
          h('button', { class: 'btn ' + (danger ? 'danger' : ''), value: 'yes', autofocus: true }, confirm))));
    d.addEventListener('close', () => { resolve(d.returnValue === 'yes'); d.remove(); });
    document.body.append(d);
    d.showModal();
  });
}

// ---- formatting ------------------------------------------------------------
// in the browser's own conventions (date order, 12/24 h, numerals)
const tf = () => new Intl.DateTimeFormat(undefined, { hour: 'numeric', minute: '2-digit' });
const df = () => new Intl.DateTimeFormat(undefined, { weekday: 'short', day: 'numeric', month: 'short' });
export const fmtTime = (d) => tf().format(new Date(d));
export const fmtDay = (d) => df().format(new Date(d));
export const fmtWhen = (d) => `${fmtDay(d)}, ${fmtTime(d)}`;

export function initials(name) {
  const parts = String(name || '?').trim().split(/\s+/);
  return ([...(parts[0] || '?')][0] + (parts.length > 1 ? [...parts[parts.length - 1]][0] : '')).toUpperCase();
}

export async function copyText(text) {
  try { await navigator.clipboard.writeText(text); return true; } catch { /* fall through */ }
  const ta = h('textarea', { style: 'position:fixed;opacity:0' }, text);
  document.body.append(ta); ta.select();
  let ok = false;
  try { ok = document.execCommand('copy'); } catch { /* ignore */ }
  ta.remove();
  return ok;
}

export const uuid = () => crypto.randomUUID();

// Loads a vendored classic script once, on demand (the QR libraries are only
// needed on the scanner and the live screen, not on every page load).
const scripts = new Map();
export function loadScript(src) {
  if (!scripts.has(src)) {
    scripts.set(src, new Promise((resolve, reject) => {
      const s = document.createElement('script');
      s.src = src;
      s.onload = resolve;
      s.onerror = () => { scripts.delete(src); reject(new Error(t('Could not load part of the app. Check your connection.'))); };
      document.head.append(s);
    }));
  }
  return scripts.get(src);
}

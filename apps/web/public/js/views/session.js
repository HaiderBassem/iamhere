import { h, mount, icon, confirmDialog, fmtTime, loading, loadScript } from '../ui.js';
import { get, post, stream, download } from '../api.js';
import { can, STATUS, modeLabel, reasonText, fail } from '../lib.js';
import { photoPanel } from '../photos.js';
import { t, tn } from '../i18n.js';

const REVIEW_REASON = { verified: t('Approved by reviewer'), rejected: t('Rejected by reviewer'), revoked: t('Revoked by reviewer') };

export function qrSvg(text) {
  const q = window.qrcode(0, 'M');
  q.addData(text);
  q.make();
  const doc = new DOMParser().parseFromString(q.createSvgTag({ cellSize: 4, margin: 0, scalable: true }), 'image/svg+xml');
  const svg = document.importNode(doc.documentElement, true);
  svg.setAttribute('shape-rendering', 'crispEdges');
  svg.setAttribute('role', 'img');
  svg.setAttribute('aria-label', t('Attendance QR code'));
  return svg;
}

export default async function session({ page, params: [orgId, sid], orgById, refreshOrgs, state, setTitle, onCleanup, stale }) {
  let org = orgById(orgId);
  if (!org) { await refreshOrgs(); org = orgById(orgId); }
  if (!org) throw new Error(t('Organization not found.'));
  const base = `/v1/orgs/${orgId}/sessions/${sid}`;
  let [s] = await Promise.all([get(base), loadScript('/vendor/qrcode.js')]);
  if (stale()) return;
  setTitle(s.title);

  let alive = true;
  const timers = new Set();
  const later = (fn, ms) => { const t = setTimeout(() => { timers.delete(t); fn(); }, ms); timers.add(t); return t; };
  const ac = new AbortController();
  let wake = null;
  onCleanup(() => {
    alive = false; timers.forEach(clearTimeout); ac.abort(); wake?.release().catch(() => {});
    document.body.classList.remove('present'); if (document.fullscreenElement) document.exitFullscreen().catch(() => {});
  });

  const live = () => s.status === 'active';
  const showQR = () => live() && can.start(org.role);
  // organizers may end their own sessions; admins and owners any session
  const canEnd = () => live() && can.start(org.role) && (s.created_by === state.me.id || can.manage(org.role));

  // ---- static pieces ----------------------------------------------------------
  const verified = h('b', {}, s.verified_count), review = h('b', {}, s.needs_review_count);
  const pVerified = h('b', {}, s.verified_count);
  const stats = h('div', { class: 'stats' },
    h('div', { class: 'stat ok' }, verified, h('span', {}, t('Checked in'))),
    h('div', { class: 'stat warn' }, review, h('span', {}, t('To review'))));
  const left = h('span', { class: 'small muted' });
  const status = h('span', { class: 'chip' });
  const rowsHost = h('div', { class: 'list' }, loading());
  const qrBox = h('div', { class: 'qr-box stale' });
  const bar = h('i', { style: 'transform:scaleX(0)' });
  const hint = h('p', { class: 'muted center small no-present' }, t('Scan with any camera. The code changes every few seconds.'));

  const present = () => {
    document.body.classList.add('present');
    document.documentElement.requestFullscreen?.().catch(() => {});
  };
  const unpresent = () => document.body.classList.remove('present');
  const onFs = () => { if (!document.fullscreenElement) unpresent(); };
  document.addEventListener('fullscreenchange', onFs);
  const onKey = (e) => { if (e.key === 'Escape') unpresent(); };
  document.addEventListener('keydown', onKey);
  onCleanup(() => { document.removeEventListener('fullscreenchange', onFs); document.removeEventListener('keydown', onKey); });

  const qrCard = h('section', { class: 'card qr-card' },
    h('div', { class: 'present-only present-title' }, s.title),
    qrBox, h('div', { class: 'timer', 'aria-hidden': 'true' }, bar), hint,
    h('div', { class: 'row wrap no-present', style: 'justify-content:center' },
      h('button', { class: 'btn secondary', type: 'button', onClick: present }, icon('expand'), t('Show on projector'))),
    h('div', { class: 'present-only' }, h('div', { class: 'present-stats' }, pVerified, t(' checked in'))),
    h('button', { class: 'btn secondary small present-only', type: 'button', onClick: () => { unpresent(); document.exitFullscreen?.().catch(() => {}); } }, t('Exit')));

  const actions = h('div', { class: 'row wrap' });
  const closedTitle = h('h2', {}), closedText = h('p', { class: 'muted' });
  const closed = h('div', { class: 'card stack-sm' }, closedTitle, closedText);

  mount(page, h('div', { class: 'stack' },
    h('div', {},
      h('a', { class: 'back no-present', href: `#/o/${orgId}` }, org.name),
      h('div', { class: 'page-head' }, h('h1', {}, s.title),
        h('div', { class: 'row wrap' }, status, h('span', { class: 'chip brand' }, modeLabel(s.verification_mode)), left))),
    h('div', { class: 'two-col' },
      h('div', { class: 'stack' }, showQR() ? qrCard : closed),
      h('div', { class: 'stack no-present' }, stats, actions, todayCard())),
    h('section', { class: 'stack-sm no-present' }, h('h2', {}, t('Who is here')), rowsHost)));

  // Strict sessions: "today's photo" of the room, the best reference there is
  // (same day, same light, same layout). The place's saved photos are used too.
  function todayCard() {
    if (s.verification_mode === 'standard' || !canEnd()) return null;
    const note = h('p', { class: 'hint' });
    const panel = photoPanel({
      base: `${base}/references`, orgId, max: 4, canEdit: true,
      cameraTitle: t('Today’s photo of the room'),
      cameraHint: t('Stand where attendees sit and photograph the front with the screen showing the code. Take 2–3 from different seats if you can.'),
      empty: t('No photo of today yet.'),
      onChange: (n) => {
        note.className = n || s.venue_id ? 'hint' : 'banner';
        note.textContent = n
          ? t('Attendees’ photos are compared with this') + (s.venue_id ? t(' and the place’s saved photos.') : '.')
          : s.venue_id
            ? t('The place’s saved photos are used. A photo from today makes the check more reliable.')
            : t('Strict mode needs photos of the room to recognize it: take 2–3 now from where attendees sit.');
      },
    });
    onCleanup(() => panel.dispose());
    return h('section', { class: 'card stack-sm' }, h('h2', {}, t('Today’s photo')), note, panel);
  }

  // Text is updated in place every second; controls are rebuilt only when the
  // session's state changes, so focus and in-flight clicks are never lost.
  let shownState = null;
  function paintHeader() {
    status.className = 'chip ' + (live() ? 'ok' : '');
    status.textContent = live() ? t('Live') : t('Ended');
    verified.textContent = s.verified_count; pVerified.textContent = s.verified_count; review.textContent = s.needs_review_count;
    const ms = new Date(s.ends_at) - Date.now();
    left.textContent = live() ? (ms > 0 ? t('Ends in {time}', { time: `${Math.floor(ms / 60000)}:${String(Math.floor(ms / 1000) % 60).padStart(2, '0')}` }) : t('Ending\u2026')) : '';
    closedTitle.textContent = live() ? t('Live now') : t('This session has ended');
    closedText.textContent = (live() ? t('So far: {checked}', { checked: tn('{n} checked in', '{n} checked in', s.verified_count) }) : tn('{n} checked in', '{n} checked in', s.verified_count))
      + (s.needs_review_count ? ' · ' + tn('{n} waiting for review', '{n} waiting for review', s.needs_review_count) : '');

    if (shownState === s.status) return;
    shownState = s.status;
    mount(actions,
      canEnd() && h('button', { class: 'btn secondary', type: 'button', onClick: close }, t('End session')),
      can.export(org.role) && h('button', { class: 'btn secondary', type: 'button', onClick: exportCsv }, icon('download'), t('Download list')),
      !live() && can.start(org.role) && h('a', { class: 'btn', href: `#/o/${orgId}` }, t('Start a new one')));
    if (!showQR()) {
      // either the session ended, or this person can watch but not show the QR
      qrCard.remove();
      if (!closed.isConnected) page.querySelector('.two-col > div').append(closed);
      unpresent();
    }
  }
  paintHeader();
  later(function tick() { if (!alive) return; paintHeader(); later(tick, 1000); }, 1000);

  async function close() {
    if (!(await confirmDialog({ title: t('End this session?'), body: t('People will no longer be able to check in.'), confirm: t('End session'), danger: true }))) return;
    try { s = await post(`${base}/close`); paintHeader(); refreshList(); } catch (err) { fail(err); }
  }
  async function exportCsv() {
    try { await download(`${base}/attendance.csv`, `${s.title.replace(/[^\p{L}\p{N}]+/gu, '-').slice(0, 40) || 'attendance'}.csv`); } catch (err) { fail(err); }
  }

  // ---- rotating QR ------------------------------------------------------------
  async function qrLoop() {
    while (alive && showQR() && live()) {
      let wait = 1500;
      try {
        const q = await get(`${base}/qr`);
        if (!alive) return;
        qrBox.classList.remove('stale');
        mount(qrBox, qrSvg(`${location.origin}/#/c/${q.token}`));
        wait = Math.max(150, q.next_rotation_in_ms + 30);
        bar.style.transition = 'none'; bar.style.transform = 'scaleX(1)'; void bar.offsetWidth;
        bar.style.transition = `transform ${q.next_rotation_in_ms}ms linear`; bar.style.transform = 'scaleX(0)';
      } catch (err) {
        qrBox.classList.add('stale');
        if (err.code === 'session_ended') { s = await get(base); paintHeader(); refreshList(); return; }
        if (err.status === 404 || err.status === 403) return fail(err);
      }
      await new Promise((r) => later(r, wait));
    }
  }
  if (showQR()) qrLoop();
  if (live() && 'wakeLock' in navigator) {
    const lock = () => navigator.wakeLock.request('screen').then((w) => { wake = w; }).catch(() => {});
    lock();
    const vis = () => { if (document.visibilityState === 'visible' && alive && live()) lock(); };
    document.addEventListener('visibilitychange', vis);
    onCleanup(() => document.removeEventListener('visibilitychange', vis));
  }

  // ---- attendee list ------------------------------------------------------------
  let listTimer = null;
  async function refreshList() {
    clearTimeout(listTimer);
    try {
      let all = [], cursor = '';
      do {
        const r = await get(`${base}/attendance?limit=500${cursor ? '&cursor=' + encodeURIComponent(cursor) : ''}`);
        all = all.concat(r.items); cursor = r.next_cursor || '';
      } while (cursor);
      if (!alive) return;
      paintRows(all.reverse());
    } catch (err) { if (alive) mount(rowsHost, h('div', { class: 'empty' }, err.message)); }
  }
  const scheduleList = () => { clearTimeout(listTimer); listTimer = setTimeout(refreshList, 700); };
  onCleanup(() => clearTimeout(listTimer));

  function paintRows(rows) {
    if (!rows.length) return mount(rowsHost, h('div', { class: 'empty' }, live() ? t('Waiting for the first person…') : t('Nobody checked in.')));
    mount(rowsHost, rows.map((r) => {
      const st = STATUS[r.status] || STATUS.pending;
      const act = (to, label, cls) => h('button', { class: `btn small ${cls}`, type: 'button', onClick: () => review_(r.id, to) }, label);
      return h('div', { class: 'item', 'data-status': r.status },
        h('span', { class: 'dot ' + st.cls }),
        h('div', { class: 'grow who', style: 'display:grid' },
          h('div', { class: 'name' }, r.full_name),
          h('div', { class: 'small muted' }, fmtTime(r.checked_in_at), r.method === 'manual' && t(' · added by hand')),
          r.status === 'needs_review' && reasonText(r.reasons) && h('div', { class: 'reasons' }, reasonText(r.reasons))),
        can.review(org.role)
          ? h('div', { class: 'row' },
              r.status === 'needs_review' && [act('verified', t('Approve'), 'ok'), act('rejected', t('Reject'), 'secondary')],
              r.status === 'verified' && act('revoked', t('Revoke'), 'ghost'),
              (r.status === 'rejected' || r.status === 'revoked') && act('verified', t('Approve'), 'secondary'))
          : h('span', { class: 'chip ' + st.cls }, st.label));
    }));
  }
  async function review_(rid, to) {
    try {
      await post(`/v1/orgs/${orgId}/attendance/${rid}/review`, { status: to, reason: REVIEW_REASON[to] });
      s = await get(base); paintHeader(); refreshList();
    } catch (err) { fail(err); refreshList(); }
  }

  // ---- live counts ----------------------------------------------------------------
  refreshList();
  (async function live_() {
    let backoff = 1000;
    while (alive && live()) {
      try {
        await stream(`${base}/events`, (name, c) => {
          if (name !== 'counts') return;
          backoff = 1000;
          const changed = c.verified !== s.verified_count || c.needs_review !== s.needs_review_count;
          s.verified_count = c.verified; s.needs_review_count = c.needs_review;
          if (c.status !== 'active') { s.status = c.status; }
          paintHeader();
          if (changed || c.status !== 'active') scheduleList();
        }, ac.signal);
      } catch (err) { if (!alive) return; if (err.status === 404 || err.status === 403) return; }
      if (!alive) return;
      await new Promise((r) => later(r, backoff)); backoff = Math.min(backoff * 2, 15000);
      try { s = await get(base); paintHeader(); } catch { /* retry on next loop */ }
    }
  })();
}

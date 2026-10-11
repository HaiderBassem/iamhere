import { h, mount, icon } from '../ui.js';
import { brand } from '../../brand.js';
import { t } from '../i18n.js';

const SVG = 'http://www.w3.org/2000/svg';

// A decorative QR-like pattern for the illustration (not a real code).
function fakeQR(size = 21) {
  const s = document.createElementNS(SVG, 'svg');
  s.setAttribute('viewBox', `0 0 ${size} ${size}`);
  s.setAttribute('aria-hidden', 'true');
  let seed = 7;
  const rnd = () => ((seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff);
  const finder = (x, y) => (x >= 0 && x < 7 && y >= 0 && y < 7);
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      const inF = [[0, 0], [size - 7, 0], [0, size - 7]].some(([fx, fy]) => finder(x - fx, y - fy));
      let on;
      if (inF) {
        const [fx, fy] = [[0, 0], [size - 7, 0], [0, size - 7]].find(([a, b]) => finder(x - a, y - b));
        const dx = x - fx, dy = y - fy;
        on = dx === 0 || dy === 0 || dx === 6 || dy === 6 || (dx >= 2 && dx <= 4 && dy >= 2 && dy <= 4);
      } else on = rnd() > 0.52;
      if (!on) continue;
      const r = document.createElementNS(SVG, 'rect');
      r.setAttribute('x', x); r.setAttribute('y', y); r.setAttribute('width', 1); r.setAttribute('height', 1);
      s.append(r);
    }
  }
  return s;
}

export default async function landing({ page, setTitle }) {
  setTitle('');
  const step = (n, ic, title, text) => h('li', { class: 'step-card' },
    h('span', { class: 'step-num' }, n), h('span', { class: 'feature-icon' }, icon(ic)), h('h3', {}, title), h('p', { class: 'muted' }, text));
  const mode = (title, badge, text, points) => h('div', { class: 'mode-card' },
    h('div', { class: 'row between' }, h('h3', {}, title), h('span', { class: 'chip brand' }, badge)),
    h('p', { class: 'muted' }, text),
    h('ul', { class: 'ticks' }, points.map((p) => h('li', {}, icon('check'), p))));

  mount(page, h('div', { class: 'landing' },
    h('header', { class: 'land-nav' },
      h('a', { class: 'brand', href: '#/' }, h('img', { src: brand.logo, alt: '' }), h('span', {}, brand.name)),
      h('span', { class: 'grow' }),
      h('a', { class: 'btn ghost small', href: '#/signin' }, t('Sign in')),
      h('a', { class: 'btn small', href: '#/signup' }, t('Get started'))),

    h('section', { class: 'land-hero' },
      h('div', { class: 'hero-text' },
        h('span', { class: 'eyebrow' }, t('For classes, workshops and events')),
        h('h1', {}, t('Attendance in one scan.')),
        h('p', { class: 'lead' }, t('Show a code that changes every 5 seconds. People scan it with their phone, and the list fills itself in. No app to install, no paper, no queue.')),
        h('div', { class: 'row wrap' },
          h('a', { class: 'btn big-inline', href: '#/signup' }, t('Create a free account'), icon('arrow')),
          h('a', { class: 'btn secondary big-inline', href: '#/signin' }, t('Sign in'))),
        h('p', { class: 'small muted' }, t('Works on any phone with a camera · One account for everything'))),
      h('div', { class: 'hero-art', 'aria-hidden': 'true' },
        h('div', { class: 'screen' },
          h('div', { class: 'screen-title' }, t('Software Engineering')),
          h('div', { class: 'screen-count' }, h('b', {}, '42'), ' ', t('checked in')),
          h('div', { class: 'screen-qr' }, fakeQR()),
          h('div', { class: 'screen-bar' }, h('i', {}))),
        h('div', { class: 'phone' },
          h('div', { class: 'phone-ok' }, icon('check')),
          h('b', {}, t('You’re checked in')),
          h('span', { class: 'small muted' }, t('Software Engineering · 9:02'))))),

    h('section', { class: 'land-section' },
      h('h2', {}, t('How it works')),
      h('ol', { class: 'steps-grid' },
        step('1', 'calendar', t('Start a session'), t('Give it a name and tap Start. Choose Normal or Strict.')),
        step('2', 'qr', t('Show the code'), t('Put it on the projector or your phone. It changes every 5 seconds, so a photo of it is useless.')),
        step('3', 'people', t('Watch the list fill in'), t('People scan with their phone camera. You see each name arrive, and download the list when you’re done.')))),

    h('section', { class: 'land-section' },
      h('h2', {}, t('Two modes, one tap apart')),
      h('div', { class: 'modes-grid' },
        mode(t('Normal'), t('Fastest'), t('For everyday classes and meetings where trust is high.'),
          [t('Scan and done, in about a second'), t('Nothing to set up'), t('Location and photos are never asked for')]),
        mode(t('Strict'), t('Exams & paid events'), t('For when it really matters who was in the room.'),
          [t('Location checked against the place'), t('A photo of the screen proves the code was seen live'), t('Recognises the room itself, and learns it better with every session')]))),

    h('section', { class: 'land-section band' },
      h('div', { class: 'band-inner' },
        h('span', { class: 'feature-icon' }, icon('user')),
        h('div', { class: 'stack-sm' },
          h('h2', {}, t('One account. Every session, everywhere.')),
          h('p', { class: 'muted' }, t('Students, staff and guests use the same account at every class, workshop and event, in any organization. All their attendance in one place; nothing to register again.'))),
        h('a', { class: 'btn', href: '#/signup' }, t('Get started'), icon('arrow')))),

    h('section', { class: 'land-section' },
      h('div', { class: 'facts' },
        h('div', {}, icon('shield'), h('b', {}, t('Private by design')), h('span', { class: 'muted small' }, t('Attendee photos are never stored. Locations are kept as a distance only.'))),
        h('div', {}, icon('sparkle'), h('b', {}, t('Hard to cheat')), h('span', { class: 'muted small' }, t('A forwarded code expires in seconds. Strict mode checks place and room.'))),
        h('div', {}, icon('download'), h('b', {}, t('Your data, your way')), h('span', { class: 'muted small' }, t('Export any session as a spreadsheet (CSV) in one click.'))))),

    h('footer', { class: 'land-foot' },
      h('span', {}, `© ${new Date().getFullYear()} ${brand.name}`),
      h('span', { class: 'grow' }),
      h('a', { href: '#/signin' }, t('Sign in')), h('a', { href: '#/signup' }, t('Create account')))));
}

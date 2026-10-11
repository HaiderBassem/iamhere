import { h, mount, icon, loadScript, qrSvg } from '../ui.js';
import { landNav, landFoot } from '../public.js';
import { t } from '../i18n.js';

// Demo names for the live feed in the hero (clearly a demo: no real people).
const DEMO = ['Sara A.', 'Omar K.', 'Lana H.', 'Yusuf M.', 'Zainab R.', 'Ali S.', 'Noor F.', 'Karim B.', 'Huda J.', 'Rami T.', 'Maya D.', 'Hassan Q.'];

export default async function landing({ page, setTitle, onCleanup }) {
  setTitle('');
  const list = (cls, items) => h('ul', { class: cls }, items);
  const step = (n, ic, title, text) => h('li', { class: 'step-card' },
    h('span', { class: 'step-num' }, t('Step {n}', { n })), h('span', { class: 'row', style: 'gap:10px' }, icon(ic), h('h3', {}, title)), h('p', { class: 'muted' }, text));
  const who = (ic, title, text) => h('li', {}, icon(ic), h('b', {}, title), h('span', { class: 'small muted' }, text));
  const problem = (text) => h('li', {}, icon('x'), h('span', {}, text));
  const mode = (title, badge, text, points) => h('div', { class: 'mode-card' },
    h('div', { class: 'row between' }, h('h3', {}, title), h('span', { class: 'chip brand' }, badge)),
    h('p', { class: 'muted' }, text),
    h('ul', { class: 'ticks' }, points.map((p) => h('li', {}, icon('check'), p))));

  // ---- the hero demo: a real QR code that changes every 5 seconds, and names arriving
  const qr = h('div', { class: 'demo-qr', role: 'img', 'aria-label': t('A QR code that changes every 5 seconds') });
  const bar = h('i', { style: 'transform:scaleX(0)' });
  const count = h('b', {}, '0');
  const feed = h('ul', { class: 'demo-feed', 'aria-hidden': 'true' });
  const demo = h('div', { class: 'demo' },
    h('div', { class: 'row between', style: 'inline-size:100%' }, h('span', { class: 'chip ok' }, t('Live')), h('span', { class: 'chip' }, t('Demo'))),
    h('div', { class: 'demo-title' }, t('Software Engineering · Week 6')),
    qr, h('div', { class: 'timer', 'aria-hidden': 'true' }, bar),
    h('div', { class: 'demo-count' }, count, ' ', t('checked in')),
    feed);

  mount(page, h('div', { class: 'landing' }, landNav(),
    h('section', { class: 'land-hero' },
      h('div', { class: 'hero-text' },
        h('span', { class: 'eyebrow' }, t('Attendance for classes, teams and events')),
        h('h1', {}, t('One title. One QR code.'), ' ', h('em', {}, t('Everyone checked in.'))),
        h('p', { class: 'lead' }, t('Type a title and press Start. People scan the code on your screen with their phone, and the list fills itself in. No app, no paper, no queue.')),
        h('div', { class: 'row wrap' },
          h('a', { class: 'btn big-inline', href: '#/signup' }, t('Start free'), icon('arrow')),
          h('a', { class: 'btn secondary big-inline', href: '#/signin' }, t('Sign in'))),
        h('p', { class: 'small muted' }, t('Free · Nothing to install · Works on any phone with a camera'))),
      h('div', { class: 'hero-art' }, demo)),

    h('section', { class: 'land-section alt' },
      h('div', { class: 'section-head' }, h('span', { class: 'eyebrow' }, t('The problem')), h('h2', {}, t('Taking attendance is still broken'))),
      list('problem-grid', [
        problem(t('Paper sheets take ten minutes of every class.')),
        problem(t('Friends sign for friends who are not there.')),
        problem(t('A fixed QR code is in the group chat within seconds.')),
        problem(t('Lists get lost in a drawer or a spreadsheet.')),
        problem(t('Reports take hours of copying by hand.')),
      ])),

    h('section', { class: 'land-section' },
      h('div', { class: 'section-head' }, h('span', { class: 'eyebrow' }, t('How it works')), h('h2', {}, t('From title to full list in a minute'))),
      h('ol', { class: 'steps-grid' },
        step(1, 'pen', t('Create'), t('Type a title, or keep the one we suggest, and press Start.')),
        step(2, 'qr', t('Display'), t('Show the code on a projector or your phone. It changes every 5 seconds.')),
        step(3, 'scan', t('Scan'), t('People scan it with their phone camera. No app to install.')),
        step(4, 'shield', t('Verify'), t('A forwarded code expires in seconds. Strict mode also checks the place and the room.')),
        step(5, 'check', t('Done'), t('Names arrive live. Reports and spreadsheets are ready when you are.')))),

    h('section', { class: 'land-section alt' },
      h('div', { class: 'section-head' }, h('span', { class: 'eyebrow' }, t('Who it’s for')), h('h2', {}, t('Anyone who takes attendance every week'))),
      list('who-grid', [
        who('building', t('Universities and schools'), t('Lectures, labs and exams.')),
        who('calendar', t('Training centres'), t('Courses with attendance requirements.')),
        who('people', t('Companies'), t('Trainings, meetings and onboarding.')),
        who('sparkle', t('Events and conferences'), t('Sessions, workshops and guests.')),
        who('chat', t('Communities'), t('Clubs, volunteers and meetups.')),
      ])),

    h('section', { class: 'land-section' },
      h('div', { class: 'section-head' }, h('span', { class: 'eyebrow' }, t('Two modes')), h('h2', {}, t('As strict as the moment needs'))),
      h('div', { class: 'modes-grid' },
        mode(t('Normal'), t('The default'), t('For everyday classes and meetings.'),
          [t('Scan and done, in about a second'), t('Nothing to set up'), t('Never asks for location or photos')]),
        mode(t('Strict'), t('Exams and paid events'), t('For when it really matters who was in the room.'),
          [t('Location checked against the place'), t('A photo of the screen proves the code was seen live'), t('Recognises the room, and learns it better with every session'), t('Anything doubtful goes to you to decide')]))),

    h('section', { class: 'promise' },
      h('h2', {}, t('One title. One QR code.'), ' ', h('span', { class: 'here' }, t('Everyone checked in.'))),
      h('p', {}, t('One account for every attendee, in every organization. They sign up once, in 20 seconds, during their first check-in, and never again.')),
      h('a', { class: 'btn light big-inline', href: '#/signup' }, t('Start your first session'), icon('arrow'))),

    h('section', { class: 'land-section' },
      h('div', { class: 'facts' },
        h('div', {}, icon('sparkle'), h('b', {}, t('Free')), h('span', { class: 'muted small' }, t('No plans and no limits while we grow.'))),
        h('div', {}, icon('shield'), h('b', {}, t('Private by design')), h('span', { class: 'muted small' }, [t('Photos are not kept. Locations are kept as a distance only.'), ' ', h('a', { href: '#/privacy' }, t('What we keep'))])),
        h('div', {}, icon('server'), h('b', {}, t('Your own server, if you want')), h('span', { class: 'muted small' }, t('Institutions can run it themselves and keep every record in-house.'))))),
    landFoot()));

  // ---- run the demo (stops when the page changes; still under reduced motion: the code must change)
  const timers = [];
  onCleanup(() => timers.forEach(clearInterval));
  try { await loadScript('/vendor/qrcode.js'); } catch { return; }
  let n = 0;
  const rotate = () => {
    mount(qr, qrSvg(`I Am Here demo code ${Date.now().toString(36)}`));
    bar.style.transition = 'none'; bar.style.transform = 'scaleX(1)'; void bar.offsetWidth;
    bar.style.transition = 'transform 5000ms linear'; bar.style.transform = 'scaleX(0)';
  };
  const arrive = () => {
    if (n >= 48) { n = 0; feed.replaceChildren(); }
    n += 1;
    count.textContent = String(n);
    feed.prepend(h('li', { class: 'item' }, h('span', { class: 'dot ok' }), h('span', { class: 'grow' }, DEMO[n % DEMO.length]), h('span', { class: 'chip ok' }, t('Checked in'))));
    while (feed.children.length > 3) feed.lastChild.remove();
  };
  rotate(); arrive();
  timers.push(setInterval(rotate, 5000), setInterval(arrive, 1600));
}

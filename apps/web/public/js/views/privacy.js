import { h, mount } from '../ui.js';
import { landNav, landFoot } from '../public.js';
import { t } from '../i18n.js';

// What is stored, who sees it and for how long. Plain words; the numbers are
// the ones the server enforces (see docs/BACKEND.md).
export default async function privacy({ page, setTitle }) {
  setTitle(t('Privacy'));
  const sec = (title, ...items) => h('section', { class: 'stack-sm' }, h('h2', {}, title), h('ul', {}, items.map((x) => h('li', {}, x))));
  mount(page, h('div', { class: 'landing' }, landNav(),
    h('article', { class: 'land-section' }, h('div', { class: 'narrow prose' },
      h('div', { class: 'stack-sm' }, h('span', { class: 'eyebrow' }, t('Privacy')), h('h1', {}, t('What we keep, and why'))),
      h('p', { class: 'lead' }, t('I Am Here records attendance. It keeps what an attendance list needs, and as little else as it can.')),
      sec(t('Your account'),
        t('Your name, your email and your password (stored only as a one-way hash). A phone number only if you add one.'),
        t('Signing in on a device keeps you signed in for 30 days from the last time you use it. You can sign out other devices from your account page.')),
      sec(t('When you check in'),
        t('The session, the time and the result (checked in, waiting for review, or not accepted).'),
        t('In Strict sessions: how far you were from the place and how precise your phone’s location was. Never your exact position.'),
        t('In Strict sessions: the photo of the screen is compared with the room and then discarded. It is not stored.'),
        t('From strong, verified check-ins, the room is learned as a set of numbers that describe it (not the photo). These are deleted after 30 days.')),
      sec(t('Who sees it'),
        t('The organizers of the organization you checked in to see your name and your check-ins with them. Organizers with permission also see your email (and phone, if you added one).'),
        t('Organizations cannot see each other’s attendance. Your full history is visible only to you.')),
      sec(t('Organizers’ photos'),
        t('Photos of a room that organizers save for a place stay until they delete them. “Today’s photos” of a session are deleted 30 days after it ends.')),
      sec(t('What we never do'),
        t('No advertising, no tracking, no selling of data. The app loads nothing from other companies, not even fonts.')),
      sec(t('Your choices'),
        t('You can change your name, or delete your account, from your account page. Deleting it removes your sign-in details; organizations keep their past attendance lists with your name replaced by “Deleted user”.'),
        t('Questions about your data go to whoever runs this service for your organization.'))),
    ), landFoot()));
}

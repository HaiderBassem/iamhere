// Building blocks shared by the account screens and organization creation.
import { h, icon, toast } from './ui.js';
import { post } from './api.js';
import { brand } from '../brand.js';
import { t } from './i18n.js';

/** A centered card page for signing in and friends, like most products use. */
export function authLayout({ title, subtitle, body, footer }) {
  return h('div', { class: 'auth' },
    h('a', { class: 'auth-brand', href: '#/' }, h('img', { src: brand.logo, alt: '', width: 40, height: 40 }), h('span', {}, brand.name)),
    h('div', { class: 'auth-card' },
      h('div', { class: 'stack-sm' }, h('h1', {}, title), subtitle && h('p', { class: 'muted' }, subtitle)),
      body),
    footer && h('div', { class: 'auth-foot' }, footer));
}

/** A labelled input. */
export function field(id, label, input, hint) {
  return h('div', { class: 'field' }, h('label', { for: id }, label), input, hint && h('span', { class: 'hint', id: id + '-hint' }, hint));
}

/** A password input with a show/hide button. `fresh` = choosing a new password. */
export function passwordField(id, label, { fresh = false, labelExtra = null } = {}) {
  const input = h('input', {
    class: 'input', id, type: 'password', required: true, maxlength: 128,
    autocomplete: fresh ? 'new-password' : 'current-password', 'aria-describedby': fresh ? id + '-hint' : false,
    minlength: fresh ? 8 : false, spellcheck: 'false', autocapitalize: 'off',
  });
  const toggle = h('button', { class: 'pw-toggle', type: 'button', 'aria-label': t('Show password'), 'aria-pressed': 'false' }, icon('eye'));
  toggle.addEventListener('click', () => {
    const show = input.type === 'password';
    input.type = show ? 'text' : 'password';
    toggle.setAttribute('aria-pressed', String(show));
    toggle.setAttribute('aria-label', show ? t('Hide password') : t('Show password'));
    toggle.replaceChildren(icon(show ? 'eyeOff' : 'eye'));
    input.focus();
  });
  const hint = fresh && h('span', { class: 'hint', id: id + '-hint' }, t('At least 8 characters. A few words together are easy to remember and hard to guess.'));
  const wrap = h('div', { class: 'field' },
    h('div', { class: 'row between' }, h('label', { for: id }, label), labelExtra),
    h('div', { class: 'pw' }, input, toggle), hint);
  wrap.input = input;
  return wrap;
}

/** An error area at the top of a form, announced to screen readers. */
export function formError() {
  const el = h('div', { class: 'banner bad', role: 'alert', hidden: true });
  el.show = (msg) => { el.textContent = msg; el.hidden = false; };
  el.hide = () => { el.hidden = true; };
  return el;
}

/** Puts a submit button into a busy state while fn runs. */
export async function busy(btn, label, fn) {
  const old = [...btn.childNodes];
  btn.disabled = true;
  btn.replaceChildren(h('span', { class: 'spinner small', 'aria-hidden': 'true' }), label);
  try { return await fn(); } finally { btn.disabled = false; btn.replaceChildren(...old); }
}

const slugify = (s) => s.toLowerCase().normalize('NFKD').replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 40);
const suffix = () => Math.random().toString(36).slice(2, 7);
const KINDS = [['Class', 'e.g. Software Engineering 3B'], ['Workshop', 'e.g. Design Thinking Workshop'], ['Event', 'e.g. Tech Summit 2026'],
  ['Team', 'e.g. Product Team'], ['School', 'e.g. University of Technology']];

/** The "create an organization" form. Resolves via onCreated(org). */
export function orgForm({ onCreated, refreshOrgs, submitLabel = t('Create organization') }) {
  const name = h('input', { class: 'input', id: 'org-name', required: true, maxlength: 200, autocomplete: 'organization',
    placeholder: t('e.g. Software Engineering 3B') });
  const kinds = h('div', { class: 'chips', role: 'group', 'aria-label': t('What is it?') },
    KINDS.map(([k, ex]) => h('button', {
      type: 'button', class: 'chip-btn', 'aria-pressed': 'false',
      onClick: (e) => {
        kinds.querySelectorAll('button').forEach((b) => b.setAttribute('aria-pressed', String(b === e.currentTarget)));
        name.placeholder = t(ex);
        name.focus();
      },
    }, t(k))));
  const err = formError();
  const btn = h('button', { class: 'btn big', type: 'submit' }, submitLabel, icon('arrow'));
  const form = h('form', {
    class: 'stack',
    onSubmit: async (e) => {
      e.preventDefault();
      err.hide();
      if (!name.value.trim()) { err.show(t('Give it a name.')); return name.focus(); }
      await busy(btn, t('Creating…'), async () => {
        const base = slugify(name.value);
        const tz = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
        try {
          // The identifier is internal; the name is what people see. Retry on a clash.
          for (let i = 0; ; i++) {
            const slug = (base.length >= 2 && i === 0) ? base : `${base.length >= 2 ? base : 'org'}-${suffix()}`;
            try {
              const org = await post('/v1/orgs', { name: name.value.trim(), slug, timezone: tz });
              await refreshOrgs();
              toast(t('{name} is ready', { name: org.name }), 'ok');
              return onCreated(org);
            } catch (e2) { if (e2.code !== 'slug_taken' || i >= 3) throw e2; }
          }
        } catch (e3) { err.show(e3.message); }
      });
    },
  }, err,
  h('div', { class: 'field' }, h('span', { class: 'label' }, t('What is it?')), kinds),
  field('org-name', t('Name'), name, t('People see this name when they check in. You can change it later.')),
  btn);
  form.focusFirst = () => name.focus();
  return form;
}

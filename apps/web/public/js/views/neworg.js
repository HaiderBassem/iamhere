import { h, mount, toast } from '../ui.js';
import { post } from '../api.js';
import { fail } from '../lib.js';

const slugify = (s) => s.toLowerCase().normalize('NFKD').replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 40);
const suffix = () => Math.random().toString(36).slice(2, 7);

export default async function neworg({ page, navigate, refreshOrgs, setTitle }) {
  setTitle('New organization');
  const name = h('input', { class: 'input', id: 'n', required: true, maxlength: 200, placeholder: 'e.g. Software Engineering, Class 3B, Our Team', autocomplete: 'organization' });
  const btn = h('button', { class: 'btn big', type: 'submit' }, 'Create');
  mount(page, h('div', { class: 'stack' },
    h('a', { class: 'back', href: '#/' }, 'Back'),
    h('div', { class: 'page-head' }, h('h1', {}, 'Set up your organization'),
      h('p', { class: 'muted' }, 'Just a name. You can invite people and add places later.')),
    h('form', {
      class: 'card stack',
      onSubmit: async (e) => {
        e.preventDefault();
        btn.disabled = true;
        const base = slugify(name.value);
        const tz = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
        try {
          // The identifier is internal; the name is what people see. Retry on a clash.
          for (let i = 0; ; i++) {
            const slug = (base.length >= 2 && i === 0) ? base : `${base.length >= 2 ? base : 'org'}-${suffix()}`;
            try {
              const org = await post('/v1/orgs', { name: name.value.trim(), slug, timezone: tz });
              await refreshOrgs();
              toast('Organization created', 'ok');
              return navigate(`#/o/${org.id}`);
            } catch (err) { if (err.code !== 'slug_taken' || i >= 3) throw err; }
          }
        } catch (err) { fail(err); btn.disabled = false; }
      },
    }, h('div', { class: 'field' }, h('label', { for: 'n' }, 'Organization name'), name), btn)));
  name.focus();
}

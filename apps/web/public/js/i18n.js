// Translation, gettext style: the English text is the key, so the app reads
// naturally in English and a translation is a dictionary from English to the
// other language. To add a language, add its dictionary to CATALOGS (and its
// text direction to RTL if it is written right to left); nothing else changes.
//
//   t('Sign in')                          -> "Sign in"
//   t('Hi {name}', { name })              -> "Hi Sara"
//   tn('{n} photo', '{n} photos', count)  -> "1 photo" / "3 photos" (CLDR plural rules)

const CATALOGS = {
  en: {},
};
const RTL = new Set(['ar', 'ckb', 'fa', 'he', 'ur']);

let lang = 'en';
let catalog = CATALOGS.en;

const fill = (s, vars) => (vars ? s.replace(/\{(\w+)\}/g, (m, k) => (k in vars ? String(vars[k]) : m)) : s);

/** Translates a text; {name} placeholders are filled from vars. */
export function t(text, vars) {
  return fill(catalog[text] ?? text, vars);
}

/** Plural-aware translation; {n} is the count. */
export function tn(one, other, n, vars) {
  const form = new Intl.PluralRules(lang).select(n);           // "one", "other", or more in some languages
  const entry = catalog[other];
  const text = entry && typeof entry === 'object' ? entry[form] ?? entry.other : (form === 'one' ? one : other);
  return fill(text, { n: new Intl.NumberFormat(lang).format(n), ...vars });
}

/** Picks the best supported language for this browser and sets lang/dir on the page. */
export function initI18n(preferred = navigator.languages || [navigator.language]) {
  for (const p of preferred) {
    const base = String(p || '').toLowerCase().split('-')[0];
    if (CATALOGS[base]) { lang = base; break; }
  }
  catalog = CATALOGS[lang];
  document.documentElement.lang = lang;
  document.documentElement.dir = RTL.has(lang) ? 'rtl' : 'ltr';
}

export const locale = () => lang;

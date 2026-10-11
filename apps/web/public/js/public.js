// The frame of the public pages (landing, privacy): top bar and footer.
import { h } from './ui.js';
import { brand } from '../brand.js';
import { t } from './i18n.js';

export const landNav = () => h('header', { class: 'land-nav' },
  h('a', { class: 'brand', href: '#/' }, h('img', { src: brand.logo, alt: '', width: 32, height: 32 }), h('span', {}, brand.name)),
  h('span', { class: 'grow' }),
  h('a', { class: 'btn ghost small', href: '#/signin' }, t('Sign in')),
  h('a', { class: 'btn small', href: '#/signup' }, t('Start free')));

export const landFoot = () => h('footer', { class: 'land-foot' },
  h('a', { class: 'brand', href: '#/' }, h('img', { src: brand.logo, alt: '', width: 28, height: 28 }), h('span', {}, brand.name)),
  h('span', { class: 'small' }, t(brand.promise)),
  h('span', { class: 'grow' }),
  h('a', { class: 'small', href: '#/privacy' }, t('Privacy')),
  h('span', { class: 'small' }, t('Starting in Iraq. Built for everywhere.')));

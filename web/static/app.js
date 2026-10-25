// Точка входа SPA: маршрутизация по hash, вход, шапка. Без сборки и сторонних зависимостей.
import { api, esc, session, toast, initials } from './lib.js';
import { renderOnboarding, renderClientHome, renderWizard, renderClientApp } from './client.js';
import { renderQueue, renderDossier } from './underwriter.js';

const app = document.getElementById('app');

const DEMO = [
  { email: 'anna@romashka.demo', name: 'Анна, ООО «Ромашка»', hint: 'Хороший заёмщик → автоодобрение', color: '#e6f6ee', fg: '#138a4e' },
  { email: 'dmitry@vector.demo', name: 'Дмитрий, ООО «Вектор»', hint: 'Просит много → контрпредложение', color: '#e9f0fd', fg: '#1d5fd1' },
  { email: 'igor@severov.demo', name: 'Игорь, ИП Северов', hint: 'Были просрочки → ручной разбор', color: '#fff4e0', fg: '#b45309' },
  { email: 'sergey@gorizont.demo', name: 'Сергей, ООО «Горизонт»', hint: 'Просрочка 120 дней → отказ', color: '#fdeaea', fg: '#c5221f' },
  { email: 'irina@tuman.demo', name: 'Ирина, ООО «Туман»', hint: 'БКИ не отвечает → ручной разбор', color: '#f1f1f4', fg: '#4b5563' },
  { email: 'alexey@ladoga.demo', name: 'Алексей, ООО «Ладога»', hint: 'Новый клиент → онбординг по ИНН', color: '#f1f1f4', fg: '#4b5563' },
  { email: 'olga@bank.demo', name: 'Ольга, кредитный аналитик', hint: 'Сотрудник банка → очередь и решения', color: '#16181d', fg: '#fff', bank: true },
];

// ---------- поллинг: один активный таймер на экран ----------
let pollTimer = null;
const ctx = {
  startPolling(fn, ms = 2000) {
    clearInterval(pollTimer);
    pollTimer = setInterval(() => { if (!document.hidden) fn().catch(() => {}); }, ms);
  },
  stopPolling() { clearInterval(pollTimer); pollTimer = null; },
};

async function loadSession() {
  if (!session.token) { session.user = null; return; }
  try {
    const me = await api('GET', '/me');
    session.user = me.user;
    session.company = me.company || null;
  } catch {
    session.token = null; session.user = null;
  }
}

function topbar() {
  const u = session.user;
  const isBank = u.role === 'UNDERWRITER';
  const h = location.hash;
  return `<header class="topbar"><div class="topbar-inner">
    <a class="brand" href="#/"><span class="brand-mark">
      <svg width="16" height="16" viewBox="0 0 32 32"><path d="M7 25l9-18 9 18" stroke="white" stroke-width="4" fill="none" stroke-linecap="round" stroke-linejoin="round"/></svg></span>
      <span>Кредитный кабинет<small>${isBank ? 'Рабочее место специалиста' : 'для бизнеса'}</small></span></a>
    <nav>${isBank
      ? `<a href="#/bank" class="${h.startsWith('#/bank') ? 'active' : ''}">Очередь заявок</a>`
      : session.company ? `<a href="#/client" class="${h === '#/client' ? 'active' : ''}">Мои заявки</a><a href="#/client/new" class="${h === '#/client/new' ? 'active' : ''}">Новая заявка</a>` : ''}</nav>
    <span class="spacer"></span>
    <div class="userbox"><div class="who">${esc(u.full_name)}<small>${esc(isBank ? u.position : (session.company?.name || u.position))}</small></div>
      <button class="btn btn-secondary btn-sm" id="logout">Выйти</button></div>
  </div></header><main id="view"></main>`;
}

function renderLogin() {
  app.innerHTML = `
    <div class="login-wrap">
      <aside class="login-side">
        <div class="brand"><span class="brand-mark"><svg width="16" height="16" viewBox="0 0 32 32"><path d="M7 25l9-18 9 18" stroke="white" stroke-width="4" fill="none" stroke-linecap="round" stroke-linejoin="round"/></svg></span>Кредитный кабинет</div>
        <div>
          <h1>Кредит для бизнеса — от задачи до денег на счёте, в одном окне</h1>
          <ul>
            <li><span class="ico">🎯</span><div><b>Начните с задачи, а не с продукта</b>Подберём подходящий кредит под вашу цель</div></li>
            <li><span class="ico">⚡</span><div><b>Решение за минуты</b>Данные о компании подтягиваем сами из реестров</div></li>
            <li><span class="ico">👁️</span><div><b>Прозрачный статус</b>Видно, что происходит с заявкой и что нужно от вас</div></li>
          </ul>
        </div>
        <p class="small" style="color:#9ca3af">Прототип для хакатона. Все данные синтетические, внешние сервисы смоделированы.</p>
      </aside>
      <main class="login-main"><div class="login-box">
        <h2>Вход</h2>
        <form id="login" novalidate>
          <label class="field"><span>Email</span><input class="input" name="email" type="email" autocomplete="username" required></label>
          <label class="field"><span>Пароль</span><input class="input" name="password" type="password" autocomplete="current-password" required></label>
          <button class="btn btn-primary btn-block btn-lg">Войти</button>
        </form>
        <hr>
        <div class="small muted">Демо-учётки (пароль <b>demo1234</b>) — нажмите, чтобы войти:</div>
        <div class="demo-list">${DEMO.map(d => `
          <button class="demo-item" data-email="${d.email}">
            <span class="av" style="background:${d.color};color:${d.fg}">${d.bank ? '🏦' : esc(initials(d.name))}</span>
            <span class="txt"><b>${esc(d.name)}</b><small>${esc(d.hint)}</small></span>
          </button>`).join('')}</div>
      </div></main>
    </div>`;
  const form = app.querySelector('#login');
  const doLogin = async (email, password) => {
    try {
      const r = await api('POST', '/auth/login', { email, password });
      session.token = r.access_token;
      await loadSession();
      location.hash = session.user.role === 'UNDERWRITER' ? '#/bank' : '#/client';
    } catch (e) { toast(e.message, 'error'); }
  };
  form.onsubmit = e => { e.preventDefault(); doLogin(form.email.value, form.password.value); };
  app.querySelectorAll('[data-email]').forEach(b => b.onclick = () => doLogin(b.dataset.email, 'demo1234'));
}

async function route() {
  ctx.stopPolling();
  const hash = location.hash || '#/';
  if (!session.user) await loadSession();
  if (!session.user) { if (hash !== '#/login') location.hash = '#/login'; renderLogin(); return; }
  if (hash === '#/login' || hash === '#/') {
    location.hash = session.user.role === 'UNDERWRITER' ? '#/bank' : '#/client';
    return;
  }
  app.innerHTML = topbar();
  app.querySelector('#logout').onclick = () => { session.token = null; session.user = null; session.company = null; location.hash = '#/login'; };
  const view = app.querySelector('#view');
  view.innerHTML = '<div class="loading"><span class="spinner"></span></div>';
  const isBank = session.user.role === 'UNDERWRITER';
  const parts = hash.slice(2).split('/');
  try {
    if (isBank) {
      if (parts[0] !== 'bank') { location.hash = '#/bank'; return; }
      if (parts[1] === 'apps' && parts[2]) await renderDossier(view, parts[2], ctx);
      else await renderQueue(view, ctx);
    } else {
      if (parts[0] !== 'client') { location.hash = '#/client'; return; }
      if (!session.company) { await renderOnboarding(view); return; }
      if (parts[1] === 'new') await renderWizard(view);
      else if (parts[1] === 'apps' && parts[2] && parts[3] === 'edit') await renderWizard(view, parts[2]);
      else if (parts[1] === 'apps' && parts[2]) await renderClientApp(view, parts[2], ctx);
      else await renderClientHome(view);
    }
  } catch (e) {
    view.innerHTML = `<div class="container"><div class="card"><div class="empty"><div class="big">⚠️</div><p>${esc(e.message)}</p>
      <a class="btn btn-secondary" href="#/">На главную</a></div></div></div>`;
  }
}

window.addEventListener('hashchange', route);
window.addEventListener('session-changed', async () => { await loadSession(); route(); });
route();

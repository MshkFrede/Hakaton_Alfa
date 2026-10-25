// Общие помощники интерфейса: запросы к API, форматирование, безопасная вставка HTML.

const TOKEN_KEY = 'cc_token';

export const session = {
  get token() { try { return localStorage.getItem(TOKEN_KEY); } catch { return null; } },
  set token(v) { try { v ? localStorage.setItem(TOKEN_KEY, v) : localStorage.removeItem(TOKEN_KEY); } catch { /* ignore */ } },
  user: null,
  company: null,
};

export class ApiError extends Error {
  constructor(status, body) {
    super(body?.error?.message || `Ошибка ${status}`);
    this.status = status;
    this.code = body?.error?.code;
    this.fields = body?.error?.fields || {};
  }
}

// api — обёртка над fetch: JSON, токен, единая обработка ошибок.
export async function api(method, path, body, opts = {}) {
  const headers = {};
  if (session.token) headers.Authorization = `Bearer ${session.token}`;
  let payload;
  if (body instanceof FormData) payload = body;
  else if (body !== undefined) { headers['Content-Type'] = 'application/json'; payload = JSON.stringify(body); }
  let res;
  try {
    res = await fetch(`/api/v1${path}`, { method, headers, body: payload });
  } catch {
    throw new ApiError(0, { error: { message: 'Нет связи с сервером' } });
  }
  if (opts.raw) return res;
  const data = res.status === 204 ? null : await res.json().catch(() => null);
  if (!res.ok) {
    if (res.status === 401 && path !== '/auth/login') {
      session.token = null;
      location.hash = '#/login';
    }
    throw new ApiError(res.status, data);
  }
  return data;
}

// esc — экранирование пользовательских данных перед вставкой в разметку.
export function esc(v) {
  return String(v ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

const nf = new Intl.NumberFormat('ru-RU');
export const rub = v => (v === null || v === undefined || v === '') ? '—' : `${nf.format(Math.round(v))} ₽`;
export const num = v => nf.format(v);
export const pct = v => `${(v * 100).toFixed(0)}%`;
export function shortRub(v) {
  if (v >= 1e6) return `${(v / 1e6).toLocaleString('ru-RU', { maximumFractionDigits: 1 })} млн ₽`;
  if (v >= 1e3) return `${Math.round(v / 1e3)} тыс. ₽`;
  return rub(v);
}
export function months(n) {
  const m10 = n % 10, m100 = n % 100;
  const w = (m10 === 1 && m100 !== 11) ? 'месяц' : (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) ? 'месяца' : 'месяцев';
  return `${n} ${w}`;
}
export function dt(s) {
  if (!s) return '—';
  return new Date(s).toLocaleString('ru-RU', { day: '2-digit', month: 'short', hour: '2-digit', minute: '2-digit' });
}
export function date(s) {
  if (!s) return '—';
  return new Date(s).toLocaleDateString('ru-RU', { day: '2-digit', month: 'long', year: 'numeric' });
}
export function ago(s) {
  const sec = Math.max(0, (Date.now() - new Date(s).getTime()) / 1000);
  if (sec < 60) return 'только что';
  if (sec < 3600) return `${Math.floor(sec / 60)} мин назад`;
  if (sec < 86400) return `${Math.floor(sec / 3600)} ч назад`;
  return dt(s);
}
// parseMoney — «1 500 000» → 1500000
export const parseMoney = s => Number(String(s).replace(/[^\d]/g, '')) || 0;
export const fmtInput = v => v ? nf.format(v) : '';

let toastTimer;
export function toast(msg, kind = '') {
  const el = document.getElementById('toast');
  el.textContent = msg;
  el.className = `show ${kind}`;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { el.className = ''; }, 3500);
}

// Статусы: оформление для бейджей.
export const STATUS_KIND = {
  DRAFT: '', SUBMITTED: 'info', ENRICHMENT: 'info', SCORING: 'info', MANUAL_REVIEW: 'warn', DOCS_REQUESTED: 'warn',
  APPROVED: 'ok', COUNTER_OFFER: 'ok', SIGNING: 'ok', SIGNED: 'ok', ISSUED: 'ok',
  REJECTED: 'bad', DECLINED_BY_CLIENT: '', CANCELLED: '',
};
export const statusBadge = (code, title) => `<span class="badge ${STATUS_KIND[code] ?? ''}">${esc(title)}</span>`;

export const PROCESSING = ['SUBMITTED', 'ENRICHMENT', 'SCORING'];
export const FINAL = ['ISSUED', 'REJECTED', 'DECLINED_BY_CLIENT', 'CANCELLED'];

// Загрузка документа с авторизацией (обычная ссылка не передаёт токен).
export async function downloadDoc(appId, doc) {
  const res = await api('GET', `/applications/${appId}/documents/${doc.id}`, undefined, { raw: true });
  if (!res.ok) { toast('Не удалось скачать файл', 'error'); return; }
  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url; a.download = doc.file_name; a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

export function initials(name) {
  return String(name).split(/\s+/).slice(0, 2).map(w => w[0] || '').join('').toUpperCase();
}

// showFieldErrors подсвечивает поля формы по ответу API (422).
export function showFieldErrors(root, fields) {
  root.querySelectorAll('.err').forEach(e => e.remove());
  root.querySelectorAll('.invalid').forEach(e => e.classList.remove('invalid'));
  for (const [name, msg] of Object.entries(fields || {})) {
    const input = root.querySelector(`[name="${name}"]`);
    if (!input) continue;
    input.classList.add('invalid');
    const wrap = input.closest('.field') || input.parentElement;
    const e = document.createElement('span');
    e.className = 'err';
    e.textContent = msg;
    wrap.appendChild(e);
  }
}

export function timeline(events, { bank = false } = {}) {
  if (!events.length) return '<p class="muted">Событий пока нет</p>';
  const cls = e => e.actor_type === 'SYSTEM' ? 'sys' : e.actor_type === 'UNDERWRITER' ? 'uw' : 'cl';
  return `<ul class="timeline">${[...events].reverse().map(e => `
    <li class="${cls(e)} ${e.visibility === 'BANK' ? 'bank' : ''}">
      <div>${esc(e.message || e.type)}</div>
      <div class="meta">${dt(e.created_at)} · ${esc(e.actor_name || '')}${bank && e.visibility === 'BANK' ? ' · <b>видно только банку</b>' : ''}</div>
      ${bank && e.internal_comment ? `<div class="internal">${esc(e.internal_comment)}</div>` : ''}
    </li>`).join('')}</ul>`;
}

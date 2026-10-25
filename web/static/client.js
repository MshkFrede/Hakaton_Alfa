// Кабинет клиента: онбординг компании, список заявок, мастер заявки, страница заявки.
import {
  api, esc, rub, shortRub, months, dt, date, ago, toast, session, parseMoney, fmtInput, statusBadge,
  PROCESSING, FINAL, showFieldErrors, timeline, downloadDoc, ApiError,
} from './lib.js';

const PURPOSE_ICON = { EQUIPMENT: '🛠️', WORKING_CAPITAL: '📦', CASH_GAP: '⏳', SEASONAL: '📈', NEW_DIRECTION: '🚀', OTHER: '✏️' };

// ======================= Онбординг: привязка компании =======================
export async function renderOnboarding(root) {
  root.innerHTML = `
    <div class="container narrow">
      <h1>Добро пожаловать, ${esc(session.user.full_name.split(' ')[1] || session.user.full_name)}!</h1>
      <p class="muted">Чтобы подать заявку, укажите ИНН компании или ИП. Остальные данные мы заполним сами из ЕГРЮЛ/ЕГРИП — вводить их вручную не нужно.</p>
      <div class="card">
        <form id="inn-form" class="actions" style="align-items:flex-start">
          <label class="field" style="flex:1;min-width:240px;margin:0">
            <span>ИНН</span>
            <input class="input" name="inn" inputmode="numeric" maxlength="12" placeholder="10 или 12 цифр" autocomplete="off" required>
            <span class="hint">Для демо: <a href="#" data-inn="7713056785">7713056785</a> — ООО «Ладога»</span>
          </label>
          <button class="btn btn-primary" style="margin-top:28px">Найти</button>
        </form>
        <div id="preview"></div>
      </div>
    </div>`;
  const form = root.querySelector('#inn-form');
  const preview = root.querySelector('#preview');
  root.querySelector('[data-inn]').onclick = e => { e.preventDefault(); form.inn.value = e.target.dataset.inn; form.requestSubmit(); };
  form.onsubmit = async e => {
    e.preventDefault();
    preview.innerHTML = '<div class="loading"><span class="spinner"></span> Запрашиваем ЕГРЮЛ…</div>';
    try {
      const c = await api('GET', `/companies/lookup?inn=${encodeURIComponent(form.inn.value.trim())}`);
      showFieldErrors(form, {});
      preview.innerHTML = `
        <hr>
        <div class="card-head"><h2>${esc(c.name)}</h2><span class="badge ${c.status === 'ACTIVE' ? 'ok' : 'bad'}">${c.status === 'ACTIVE' ? 'Действующая' : esc(c.status)}</span></div>
        <dl class="kv">
          <dt>Полное наименование</dt><dd>${esc(c.full_name)}</dd>
          <dt>ИНН / ОГРН</dt><dd class="num">${esc(c.inn)} / ${esc(c.ogrn)}</dd>
          <dt>Руководитель</dt><dd>${esc(c.director.name)}</dd>
          <dt>Дата регистрации</dt><dd>${date(c.registration_date)}</dd>
          <dt>Основной вид деятельности</dt><dd>${esc(c.okved.code)} — ${esc(c.okved.name)}</dd>
          <dt>Адрес</dt><dd>${esc(c.address)}</dd>
        </dl>
        <p class="small muted" style="margin-top:14px">Источник: ЕГРЮЛ/ЕГРИП (симулятор). Нажимая «Это моя компания», вы подтверждаете полномочия действовать от её имени.</p>
        <button class="btn btn-primary" id="link">Это моя компания</button>`;
      preview.querySelector('#link').onclick = async () => {
        try {
          await api('POST', '/me/company', { inn: c.inn });
          toast('Компания добавлена');
          location.hash = '#/client';
          window.dispatchEvent(new Event('session-changed'));
        } catch (err) { toast(err.message, 'error'); }
      };
    } catch (err) {
      preview.innerHTML = '';
      if (err instanceof ApiError && err.status === 422) showFieldErrors(form, err.fields);
      else toast(err.message, 'error');
    }
  };
}

// ======================= Главная клиента =======================
export async function renderClientHome(root) {
  const apps = await api('GET', '/applications');
  const c = session.company;
  const active = apps.filter(a => !FINAL.includes(a.status));
  const needAction = apps.filter(a => ['APPROVED', 'COUNTER_OFFER', 'SIGNING', 'DOCS_REQUESTED', 'DRAFT'].includes(a.status));
  root.innerHTML = `
    <div class="container">
      <div class="page-head">
        <div class="grow">
          <h1>${esc(c.name)}</h1>
          <div class="muted small">ИНН ${esc(c.inn)} · ${esc(c.okved_name)}</div>
        </div>
        <a class="btn btn-primary btn-lg" href="#/client/new">+ Новая заявка на кредит</a>
      </div>
      ${needAction.length ? `<div class="notice warn" style="margin-bottom:16px">⚡ <div><b>Требуется ваше действие:</b> ${needAction.map(a =>
        `<a href="#/client/apps/${a.id}">${esc(a.number)} — ${esc(actionHint(a.status))}</a>`).join(', ')}</div></div>` : ''}
      <div class="grid grid-3" style="margin-bottom:20px">
        <div class="tile"><div class="label">Активные заявки</div><div class="value">${active.length}</div></div>
        <div class="tile"><div class="label">Выдано кредитов</div><div class="value">${apps.filter(a => a.status === 'ISSUED').length}</div>
          <div class="sub">${shortRub(apps.filter(a => a.status === 'ISSUED').reduce((s, a) => s + (a.offer?.amount || a.amount), 0))}</div></div>
        <div class="tile"><div class="label">Всего заявок</div><div class="value">${apps.length}</div></div>
      </div>
      <div class="card">
        <div class="card-head"><h2>Мои заявки</h2></div>
        ${apps.length ? `<div class="table-wrap"><table class="table">
          <thead><tr><th>Заявка</th><th>Продукт</th><th>Сумма</th><th>Срок</th><th>Статус</th><th>Обновлена</th></tr></thead>
          <tbody>${apps.map(a => `
            <tr class="row-link" data-href="#/client/apps/${a.id}">
              <td><b>${esc(a.number)}</b></td>
              <td>${esc(a.product_name)}</td>
              <td class="num nowrap">${rub(a.offer?.amount ?? a.amount)}</td>
              <td class="nowrap">${a.offer?.term_months ?? a.term_months} мес.</td>
              <td>${statusBadge(a.status, a.status_title)}</td>
              <td class="muted small nowrap">${ago(a.status_changed_at)}</td>
            </tr>`).join('')}</tbody></table></div>`
        : `<div class="empty"><div class="big">📄</div><p>Заявок пока нет.</p><a class="btn btn-primary" href="#/client/new">Подать первую заявку</a></div>`}
      </div>
    </div>`;
  root.querySelectorAll('[data-href]').forEach(tr => tr.onclick = () => { location.hash = tr.dataset.href; });
}

function actionHint(status) {
  return {
    DRAFT: 'завершите заполнение', APPROVED: 'примите предложение', COUNTER_OFFER: 'рассмотрите предложение банка',
    SIGNING: 'подпишите договор', DOCS_REQUESTED: 'загрузите документы',
  }[status] || '';
}

// ======================= Мастер заявки =======================
export async function renderWizard(root, appId) {
  const [purposes, products] = await Promise.all([api('GET', '/catalog/purposes'), api('GET', '/catalog/products')]);
  const s = {
    step: 0, id: null, version: 0,
    purpose: '', purpose_comment: '', product_code: '', amount: 1_000_000, term_months: 12,
    annual_revenue: 0, existing_monthly_payments: 0, has_collateral: false, collateral_description: '',
    contact_phone: '', bki_consent: false,
  };
  if (appId) {
    const v = await api('GET', `/applications/${appId}`);
    const a = v.application;
    if (a.status !== 'DRAFT') { location.hash = `#/client/apps/${appId}`; return; }
    Object.assign(s, {
      id: a.id, version: a.version, purpose: a.purpose, purpose_comment: a.purpose_comment, product_code: a.product_code,
      amount: a.amount || s.amount, term_months: a.term_months || s.term_months, annual_revenue: a.annual_revenue,
      existing_monthly_payments: a.existing_monthly_payments, has_collateral: a.has_collateral,
      collateral_description: a.collateral_description, contact_phone: a.contact_phone, bki_consent: a.bki_consent, step: 2,
    });
  }
  const STEPS = ['Задача', 'Продукт', 'Параметры', 'Проверка и отправка'];
  const product = () => products.find(p => p.code === s.product_code);

  const draftBody = () => ({
    product_code: s.product_code, purpose: s.purpose, purpose_comment: s.purpose_comment, amount: s.amount,
    term_months: s.term_months, annual_revenue: s.annual_revenue, existing_monthly_payments: s.existing_monthly_payments,
    has_collateral: s.has_collateral, collateral_description: s.collateral_description, contact_phone: s.contact_phone,
    bki_consent: s.bki_consent,
  });

  async function saveDraft() {
    const v = s.id
      ? await api('PATCH', `/applications/${s.id}`, { ...draftBody(), version: s.version })
      : await api('POST', '/applications', draftBody());
    s.id = v.application.id;
    s.version = v.application.version;
    return v;
  }

  function render() {
    root.innerHTML = `
      <div class="container narrow">
        <a class="back" href="#/client">← К заявкам</a>
        <div class="wizard-label">Шаг ${s.step + 1} из ${STEPS.length} · ${STEPS[s.step]}</div>
        <div class="wizard-steps">${STEPS.map((_, i) => `<div class="${i <= s.step ? 'done' : ''}"></div>`).join('')}</div>
        <div id="step"></div>
      </div>`;
    const el = root.querySelector('#step');
    [stepPurpose, stepProduct, stepParams, stepReview][s.step](el);
    window.scrollTo({ top: 0 });
  }

  // --- Шаг 1: задача бизнеса ---
  function stepPurpose(el) {
    el.innerHTML = `
      <h1>Какую задачу бизнеса нужно решить?</h1>
      <p class="muted">Не нужно разбираться в кредитных продуктах — выберите задачу, и мы подберём подходящий вариант.</p>
      <div class="choice-grid">${purposes.map(p => `
        <button class="choice ${s.purpose === p.code ? 'selected' : ''}" data-code="${p.code}">
          <div class="icon">${PURPOSE_ICON[p.code] || '•'}</div>
          <strong>${esc(p.title)}</strong>
          <div class="muted">${esc(p.hint)}</div>
        </button>`).join('')}</div>
      <div id="other" style="margin-top:16px;${s.purpose === 'OTHER' ? '' : 'display:none'}">
        <label class="field"><span>Опишите задачу</span><textarea class="input" name="purpose_comment" maxlength="1000">${esc(s.purpose_comment)}</textarea></label>
      </div>
      <div class="actions" style="margin-top:20px"><button class="btn btn-primary btn-lg" id="next" ${s.purpose ? '' : 'disabled'}>Продолжить</button></div>`;
    el.querySelectorAll('.choice').forEach(b => b.onclick = () => {
      s.purpose = b.dataset.code;
      if (s.purpose !== 'OTHER') { s.step = 1; s.product_code = ''; render(); return; }
      el.querySelectorAll('.choice').forEach(x => x.classList.toggle('selected', x === b));
      el.querySelector('#other').style.display = '';
      el.querySelector('#next').disabled = false;
    });
    el.querySelector('#next').onclick = () => {
      s.purpose_comment = el.querySelector('[name=purpose_comment]')?.value || '';
      s.step = 1; render();
    };
  }

  // --- Шаг 2: подбор продукта ---
  async function stepProduct(el) {
    const purpose = purposes.find(p => p.code === s.purpose);
    el.innerHTML = `
      <h1>Подобрали продукты</h1>
      <p class="muted">Задача: <b>${esc(purpose?.title)}</b>. Укажите примерную сумму — покажем, что подходит.</p>
      <label class="field" style="max-width:320px"><span>Сколько нужно?</span>
        <div class="input-suffix"><input class="input num" name="amount" inputmode="numeric" value="${fmtInput(s.amount)}"><em>₽</em></div></label>
      <div id="recs" class="grid" style="margin-top:8px"></div>
      <div class="actions" style="margin-top:20px">
        <button class="btn btn-secondary" id="prev">Назад</button>
      </div>`;
    const recsEl = el.querySelector('#recs');
    const input = el.querySelector('[name=amount]');
    let timer;
    async function load() {
      const recs = await api('GET', `/catalog/recommendations?purpose=${s.purpose}&amount=${s.amount}`);
      recsEl.innerHTML = recs.map(r => {
        const p = r.product;
        return `
        <button class="choice product-card ${r.fits ? '' : 'disabled'} ${s.product_code === p.code ? 'selected' : ''}" data-code="${p.code}" ${r.fits ? '' : 'disabled'}>
          ${r.recommended ? '<span class="badge ok plain tag">Рекомендуем</span>' : ''}
          <strong style="font-size:17px">${esc(p.name)}</strong>
          <div class="muted">${esc(p.tagline)}</div>
          <div class="rate">от ${p.base_rate}% <span class="muted small" style="font-weight:400">годовых</span></div>
          <div class="muted small">${shortRub(p.min_amount)} – ${shortRub(p.max_amount)} · ${p.min_term_months}–${p.max_term_months} мес. · ${esc(p.collateral_note)}</div>
          <div class="notice ${r.fits ? 'info' : 'warn'}" style="margin-top:10px">${esc(r.reason)}</div>
        </button>`;
      }).join('');
      recsEl.querySelectorAll('.choice:not(.disabled)').forEach(b => b.onclick = () => {
        s.product_code = b.dataset.code;
        const p = product();
        s.term_months = Math.min(Math.max(s.term_months, p.min_term_months), p.max_term_months);
        s.step = 2; render();
      });
    }
    input.oninput = () => {
      s.amount = parseMoney(input.value);
      input.value = fmtInput(s.amount);
      clearTimeout(timer); timer = setTimeout(load, 300);
    };
    el.querySelector('#prev').onclick = () => { s.step = 0; render(); };
    await load();
  }

  // --- Шаг 3: параметры + живой калькулятор ---
  function stepParams(el) {
    const p = product();
    el.innerHTML = `
      <h1>${esc(p.name)}: параметры</h1>
      <div class="split" style="grid-template-columns:minmax(0,1fr) 320px">
        <form id="f" class="card" novalidate>
          <label class="field"><span>Сумма кредита</span>
            <div class="input-suffix"><input class="input num" name="amount" inputmode="numeric" value="${fmtInput(s.amount)}"><em>₽</em></div>
            <span class="hint">От ${rub(p.min_amount)} до ${rub(p.max_amount)}</span></label>
          <label class="field"><span>Срок: <b id="term-label">${months(s.term_months)}</b></span>
            <input type="range" name="term_months" min="${p.min_term_months}" max="${p.max_term_months}" step="1" value="${s.term_months}">
            <span class="hint">От ${p.min_term_months} до ${p.max_term_months} мес.</span></label>
          <hr>
          <h3>О бизнесе</h3>
          <p class="small muted">Остальное — реквизиты, руководитель, вид деятельности — уже подтянуто из ЕГРЮЛ.</p>
          <label class="field"><span>Выручка за последние 12 месяцев</span>
            <div class="input-suffix"><input class="input num" name="annual_revenue" inputmode="numeric" value="${fmtInput(s.annual_revenue)}" placeholder="например, 25 000 000"><em>₽</em></div>
            <span class="hint">Сверим с данными ФНС — указывайте фактическую выручку</span></label>
          <label class="field"><span>Платежи по действующим кредитам и лизингу в месяц</span>
            <div class="input-suffix"><input class="input num" name="existing_monthly_payments" inputmode="numeric" value="${fmtInput(s.existing_monthly_payments)}" placeholder="0"><em>₽</em></div>
            <span class="hint">Если кредитов нет — оставьте пустым</span></label>
          <label class="check" style="margin-bottom:12px"><input type="checkbox" name="has_collateral" ${s.has_collateral ? 'checked' : ''}>
            <span>Готов предоставить залог или поручительство <span class="muted">— это улучшает условия</span></span></label>
          <label class="field" id="coll" style="${s.has_collateral ? '' : 'display:none'}"><span>Что предлагаете в обеспечение?</span>
            <input class="input" name="collateral_description" value="${esc(s.collateral_description)}" placeholder="Например: грузовой автомобиль 2022 г., поручительство директора"></label>
        </form>
        <div class="card sticky" id="calc"></div>
      </div>
      <div class="actions" style="margin-top:20px">
        <button class="btn btn-secondary" id="prev">Назад</button>
        <button class="btn btn-primary btn-lg" id="next">Продолжить</button>
      </div>`;
    const f = el.querySelector('#f');
    const calcEl = el.querySelector('#calc');
    let timer;
    const money = ['amount', 'annual_revenue', 'existing_monthly_payments'];
    async function recalc() {
      try {
        const c = await api('POST', '/catalog/calculate', {
          product_code: s.product_code, amount: s.amount, term_months: s.term_months,
          annual_revenue: s.annual_revenue, existing_monthly_payments: s.existing_monthly_payments,
        });
        const load = c.debt_to_revenue;
        calcEl.innerHTML = `
          <div class="muted small">Ежемесячный платёж</div>
          <div class="big">${rub(c.payment_from)}${c.payment_to !== c.payment_from ? `<span class="muted" style="font-size:16px"> – ${rub(c.payment_to)}</span>` : ''}</div>
          <div class="muted small">Ставка ${c.rate_from}–${c.rate_to}% — точная зависит от оценки</div>
          <hr>
          ${load !== undefined ? `
            <div class="small"><b>Нагрузка на выручку</b></div>
            <div class="meter ${c.load_level}"><div style="width:${Math.min(100, load / 0.5 * 100)}%"></div></div>
            <div class="small muted">${Math.round(load * 100)}% месячной выручки уйдёт на кредиты (комфортно — до 30%)</div>
            <div class="notice ${c.load_level === 'comfortable' ? 'ok' : c.load_level === 'elevated' ? 'warn' : 'bad'}" style="margin-top:10px">${esc(c.hint)}</div>
            ${c.load_level !== 'comfortable' && c.max_amount_estimate > 0 ? `<p class="small" style="margin-top:10px">Комфортная сумма на этот срок: <b>до ${rub(c.max_amount_estimate)}</b></p>` : ''}
          ` : '<p class="small muted">Укажите выручку — покажем, насколько комфортен платёж для бизнеса.</p>'}
          ${!c.within_limits ? '<div class="notice warn" style="margin-top:10px">Сумма или срок вне условий продукта</div>' : ''}
          <p class="small muted" style="margin-top:12px">Предварительный расчёт, не является офертой.</p>`;
      } catch { /* калькулятор не критичен */ }
    }
    f.oninput = e => {
      const t = e.target;
      if (money.includes(t.name)) { s[t.name] = parseMoney(t.value); t.value = fmtInput(s[t.name]); }
      if (t.name === 'term_months') { s.term_months = Number(t.value); el.querySelector('#term-label').textContent = months(s.term_months); }
      if (t.name === 'has_collateral') { s.has_collateral = t.checked; el.querySelector('#coll').style.display = t.checked ? '' : 'none'; }
      if (t.name === 'collateral_description') s.collateral_description = t.value;
      clearTimeout(timer); timer = setTimeout(recalc, 200);
    };
    el.querySelector('#prev').onclick = () => { s.step = 1; render(); };
    el.querySelector('#next').onclick = () => {
      const errs = {};
      if (s.amount < p.min_amount || s.amount > p.max_amount) errs.amount = `От ${rub(p.min_amount)} до ${rub(p.max_amount)}`;
      if (!s.annual_revenue) errs.annual_revenue = 'Укажите выручку';
      if (s.has_collateral && !s.collateral_description.trim()) errs.collateral_description = 'Опишите обеспечение';
      showFieldErrors(f, errs);
      if (Object.keys(errs).length) return;
      s.step = 3; render();
    };
    recalc();
  }

  // --- Шаг 4: проверка, согласие, отправка ---
  function stepReview(el) {
    const p = product();
    const purpose = purposes.find(x => x.code === s.purpose);
    const c = session.company;
    el.innerHTML = `
      <h1>Проверьте заявку</h1>
      <div class="card">
        <div class="card-head"><h3>Кредит</h3><button class="btn btn-ghost btn-sm" data-go="2">Изменить</button></div>
        <dl class="kv">
          <dt>Задача</dt><dd>${esc(purpose?.title)}${s.purpose_comment ? ` — ${esc(s.purpose_comment)}` : ''}</dd>
          <dt>Продукт</dt><dd>${esc(p.name)}</dd>
          <dt>Сумма</dt><dd class="num">${rub(s.amount)}</dd>
          <dt>Срок</dt><dd>${months(s.term_months)}</dd>
          <dt>Обеспечение</dt><dd>${s.has_collateral ? esc(s.collateral_description) : 'Без обеспечения'}</dd>
        </dl>
        <hr>
        <div class="card-head"><h3>Бизнес</h3></div>
        <dl class="kv">
          <dt>Компания</dt><dd>${esc(c.name)}, ИНН ${esc(c.inn)}</dd>
          <dt>Руководитель</dt><dd>${esc(c.director_name)}</dd>
          <dt>Выручка за 12 мес.</dt><dd class="num">${rub(s.annual_revenue)}</dd>
          <dt>Текущие платежи по кредитам</dt><dd class="num">${rub(s.existing_monthly_payments)} в мес.</dd>
        </dl>
        <hr>
        <form id="f" novalidate>
          <label class="field" style="max-width:320px"><span>Телефон для связи</span>
            <input class="input" name="contact_phone" type="tel" value="${esc(s.contact_phone)}" placeholder="+7 900 000-00-00"></label>
          <label class="check field"><input type="checkbox" name="bki_consent" ${s.bki_consent ? 'checked' : ''}>
            <span>Даю согласие на запрос кредитной истории в бюро кредитных историй и на обработку данных компании для рассмотрения заявки</span></label>
        </form>
        <div class="notice info">ℹ️ <div>После отправки мы проверим данные в ЕГРЮЛ, БКИ и ФНС и оценим заявку. По небольшим суммам решение приходит за пару минут — статус обновится на этой странице, никуда переходить не нужно.</div></div>
      </div>
      <div class="actions" style="margin-top:20px">
        <button class="btn btn-secondary" id="prev">Назад</button>
        <span class="spacer"></span>
        <button class="btn btn-ghost" id="save">Сохранить черновик</button>
        <button class="btn btn-primary btn-lg" id="submit">Отправить заявку</button>
      </div>`;
    const f = el.querySelector('#f');
    const sync = () => { s.contact_phone = f.contact_phone.value; s.bki_consent = f.bki_consent.checked; };
    el.querySelector('[data-go]').onclick = () => { s.step = 2; render(); };
    el.querySelector('#prev').onclick = () => { sync(); s.step = 2; render(); };
    el.querySelector('#save').onclick = async () => {
      sync();
      try { await saveDraft(); toast('Черновик сохранён'); location.hash = '#/client'; } catch (e) { toast(e.message, 'error'); }
    };
    el.querySelector('#submit').onclick = async ev => {
      sync();
      const btn = ev.currentTarget;
      btn.disabled = true;
      try {
        await saveDraft();
        await api('POST', `/applications/${s.id}/submit`);
        toast('Заявка отправлена');
        location.hash = `#/client/apps/${s.id}`;
      } catch (e) {
        btn.disabled = false;
        if (e instanceof ApiError && e.status === 422) {
          showFieldErrors(f, e.fields);
          const other = Object.keys(e.fields).filter(k => !['contact_phone', 'bki_consent'].includes(k));
          toast(other.length ? `${e.message}: ${other.map(k => e.fields[k]).join('; ')}` : e.message, 'error');
        } else toast(e.message, 'error');
      }
    };
  }

  render();
}

// ======================= Страница заявки клиента =======================
const TRACK = [
  { key: 'SUBMIT', title: 'Подача' },
  { key: 'CHECK', title: 'Проверка данных' },
  { key: 'DECISION', title: 'Решение' },
  { key: 'SIGN', title: 'Подписание' },
  { key: 'ISSUE', title: 'Выдача' },
];
function trackIndex(status) {
  return {
    DRAFT: 0, SUBMITTED: 1, ENRICHMENT: 1, SCORING: 1, MANUAL_REVIEW: 2, DOCS_REQUESTED: 2,
    APPROVED: 3, COUNTER_OFFER: 3, SIGNING: 3, SIGNED: 4, ISSUED: 5, REJECTED: 2, DECLINED_BY_CLIENT: 3, CANCELLED: 0,
  }[status] ?? 0;
}

export function tracker(status) {
  const cur = trackIndex(status);
  const failed = ['REJECTED', 'DECLINED_BY_CLIENT', 'CANCELLED'].includes(status);
  return `<div class="tracker">${TRACK.map((t, i) => {
    let cls = i < cur ? 'done' : i === cur ? 'current' : '';
    if (i === cur && failed) cls = 'failed';
    if (i === cur && (PROCESSING.includes(status) || status === 'MANUAL_REVIEW' || status === 'SIGNED')) cls += ' pulse';
    return `<div class="step ${cls}"><div class="dot">${i < cur ? '✓' : (i === cur && failed ? '×' : i + 1)}</div><span>${t.title}</span></div>`;
  }).join('')}</div>`;
}

export async function renderClientApp(root, id, ctx) {
  let lastKey = '';
  async function refresh() {
    const v = await api('GET', `/applications/${id}`);
    const key = `${v.application.status}:${v.application.version}:${v.events.length}`;
    if (key !== lastKey) { lastKey = key; draw(v); }
    if (FINAL.includes(v.application.status)) ctx.stopPolling();
  }

  function draw(v) {
    const a = v.application;
    root.innerHTML = `
      <div class="container">
        <a class="back" href="#/client">← К заявкам</a>
        <div class="page-head">
          <div class="grow">
            <h1>Заявка ${esc(a.number)}</h1>
            <div class="muted small">${esc(a.product_name)} · ${rub(a.amount)} на ${months(a.term_months)} · создана ${dt(a.created_at)}</div>
          </div>
          ${statusBadge(a.status, a.status_title)}
        </div>
        <div class="card" style="margin-bottom:16px">${tracker(a.status)}</div>
        <div class="split">
          <div class="stack">
            ${hero(v)}
            ${docsBlock(v)}
            <div class="card">
              <div class="card-head"><h3>Параметры заявки</h3></div>
              <dl class="kv">
                <dt>Продукт</dt><dd>${esc(a.product_name)}</dd>
                <dt>Запрошенная сумма</dt><dd class="num">${rub(a.amount)}</dd>
                <dt>Срок</dt><dd>${months(a.term_months)}</dd>
                <dt>Выручка за 12 мес.</dt><dd class="num">${rub(a.annual_revenue)}</dd>
                <dt>Обеспечение</dt><dd>${a.has_collateral ? esc(a.collateral_description) : 'Без обеспечения'}</dd>
                ${a.submitted_at ? `<dt>Подана</dt><dd>${dt(a.submitted_at)}</dd>` : ''}
              </dl>
            </div>
          </div>
          <div class="card sticky">
            <div class="card-head"><h3>История</h3></div>
            ${timeline(v.events)}
          </div>
        </div>
      </div>`;
    bind(v);
  }

  function offerGrid(a) {
    const o = a.offer;
    if (!o) return '';
    const changedAmount = o.amount !== a.amount, changedTerm = o.term_months !== a.term_months;
    return `<div class="offer">
      <div><div class="label">Сумма</div><div class="value">${rub(o.amount)}</div>${changedAmount ? `<div class="was">${rub(a.amount)}</div>` : ''}</div>
      <div><div class="label">Срок</div><div class="value">${o.term_months} мес.</div>${changedTerm ? `<div class="was">${a.term_months} мес.</div>` : ''}</div>
      <div><div class="label">Ставка</div><div class="value">${o.rate}%</div></div>
      <div><div class="label">Платёж в месяц</div><div class="value">${rub(o.monthly_payment)}</div></div>
    </div>`;
  }

  function hero(v) {
    const a = v.application;
    const msg = a.client_message ? `<p>${esc(a.client_message)}</p>` : '';
    switch (a.status) {
      case 'DRAFT': {
        const missing = Object.values(v.missing_fields || {});
        return `<div class="status-hero"><h2>Черновик</h2>
          <p class="muted">Заявка ещё не отправлена в банк.</p>
          ${missing.length ? `<div class="notice warn" style="margin-bottom:12px">Осталось заполнить: ${missing.map(esc).join('; ')}</div>` : ''}
          <div class="actions"><a class="btn btn-primary" href="#/client/apps/${a.id}/edit">Продолжить заполнение</a>
          <button class="btn btn-danger" data-act="cancel">Удалить черновик</button></div></div>`;
      }
      case 'SUBMITTED': case 'ENRICHMENT': case 'SCORING':
        return `<div class="status-hero info"><h2><span class="spinner"></span> ${esc(a.status_title)}</h2>
          <p>${a.status === 'SCORING' ? 'Оцениваем заявку по данным о компании.' : 'Запрашиваем данные в ЕГРЮЛ, бюро кредитных историй и ФНС.'}
          Обычно это занимает меньше минуты — страница обновится сама.</p>
          <p class="small muted">От вас ничего не требуется.</p></div>`;
      case 'MANUAL_REVIEW':
        return `<div class="status-hero warn"><h2>Заявку рассматривает специалист</h2>
          ${msg || '<p>Кредитный специалист изучает заявку.</p>'}
          <p class="small muted">${a.assigned_underwriter_name ? `Специалист: ${esc(a.assigned_underwriter_name)}. ` : ''}Если понадобятся документы — запрос появится здесь.</p></div>`;
      case 'DOCS_REQUESTED':
        return `<div class="status-hero warn"><h2>Нужны документы</h2>${msg}
          <p class="small muted">Загрузите документы ниже — после загрузки всех файлов заявка автоматически вернётся к специалисту.</p></div>`;
      case 'APPROVED': case 'COUNTER_OFFER':
        return `<div class="status-hero ok"><h2>${a.status === 'APPROVED' ? '🎉 Заявка одобрена' : 'Банк готов выдать кредит на других условиях'}</h2>
          ${msg}${offerGrid(a)}
          <p class="small muted">Переплата за весь срок: ${rub(a.offer.monthly_payment * a.offer.term_months - a.offer.amount)}. Предложение действует 30 дней.</p>
          <div class="actions"><button class="btn btn-primary btn-lg" data-act="accept">Принять и перейти к подписанию</button>
          <button class="btn btn-danger" data-act="decline">Отказаться</button></div></div>`;
      case 'SIGNING':
        return `<div class="status-hero ok"><h2>Подписание договора</h2>
          ${offerGrid(a)}
          <p>Подпишите кредитный договор простой электронной подписью. В реальном банке код пришёл бы в защищённом канале; в прототипе он показан здесь:</p>
          <p><span class="code-box">${esc(v.sign_code_demo)}</span></p>
          <form id="sign" class="actions" novalidate>
            <label class="field" style="margin:0"><input class="input num" name="code" inputmode="numeric" maxlength="6" placeholder="Код из 6 цифр" style="width:180px"></label>
            <button class="btn btn-primary">Подписать договор</button>
          </form>
          <p class="small muted" style="margin-top:10px">Можно и отказаться: <button class="file-link" data-act="decline">отказаться от кредита</button></p></div>`;
      case 'SIGNED':
        return `<div class="status-hero ok"><h2><span class="spinner"></span> Договор подписан</h2><p>Открываем ссудный счёт и зачисляем средства.</p></div>`;
      case 'ISSUED':
        return `<div class="status-hero ok"><h2>✅ Средства выданы</h2>
          ${offerGrid(a)}
          <p>Деньги зачислены ${dt(a.issued_at)}. Счёт зачисления: <b class="num">${esc(a.account_number.replace(/^(\d{5})\d+(\d{4})$/, '$1•••$2'))}</b>.</p>
          <p class="small muted">Первый платёж — через месяц. График платежей доступен в интернет-банке.</p></div>`;
      case 'REJECTED':
        return `<div class="status-hero bad"><h2>Банк не может выдать кредит</h2>${msg}
          <p class="small muted">Вы можете подать новую заявку позже — например, когда изменится финансовое положение компании, или выбрать меньшую сумму.</p>
          <a class="btn btn-secondary" href="#/client/new">Подать новую заявку</a></div>`;
      case 'DECLINED_BY_CLIENT':
        return `<div class="status-hero"><h2>Вы отказались от предложения</h2><p class="muted">Если передумаете — подайте новую заявку.</p>
          <a class="btn btn-secondary" href="#/client/new">Новая заявка</a></div>`;
      case 'CANCELLED':
        return `<div class="status-hero"><h2>Заявка отозвана</h2></div>`;
    }
    return '';
  }

  function docsBlock(v) {
    if (!v.document_requests.length) return '';
    const open = v.application.status === 'DOCS_REQUESTED';
    return `<div class="card"><div class="card-head"><h3>Документы по запросу банка</h3></div>
      <div class="stack">${v.document_requests.map(r => `
        <div class="doc-req">
          <div class="grow"><b>${esc(r.title)}</b>
            ${r.comment ? `<div class="small muted">Комментарий банка: ${esc(r.comment)}</div>` : ''}
            ${r.documents.map(d => `<div><button class="file-link" data-doc="${d.id}">📎 ${esc(d.file_name)}</button> <span class="small muted">${Math.ceil(d.size / 1024)} КБ</span></div>`).join('')}
          </div>
          ${r.status === 'UPLOADED' ? '<span class="badge ok">Загружено</span>' : open ? `
            <label class="btn btn-primary btn-sm">Загрузить файл<input type="file" hidden data-req="${r.id}" accept=".pdf,.jpg,.jpeg,.png,.xlsx,.xls,.docx,.doc,.csv,.txt,.zip"></label>` : '<span class="badge warn">Ожидается</span>'}
        </div>`).join('')}</div>
      ${open ? '<p class="small muted" style="margin-top:10px">PDF, изображения, Office, CSV, ZIP — до 10 МБ.</p>' : ''}</div>`;
  }

  function bind(v) {
    const a = v.application;
    const act = async (path, okMsg) => {
      try { await api('POST', `/applications/${a.id}/${path}`); toast(okMsg); lastKey = ''; await refresh(); ctx.startPolling(refresh); } catch (e) { toast(e.message, 'error'); }
    };
    root.querySelectorAll('[data-act]').forEach(b => b.onclick = () => {
      const k = b.dataset.act;
      if (k === 'accept') act('offer/accept', 'Условия приняты');
      if (k === 'decline' && confirmInline(b, 'Точно отказаться?')) act('offer/decline', 'Вы отказались от предложения');
      if (k === 'cancel' && confirmInline(b, 'Точно удалить?')) act('cancel', 'Черновик удалён');
    });
    const sign = root.querySelector('#sign');
    if (sign) sign.onsubmit = async e => {
      e.preventDefault();
      try {
        await api('POST', `/applications/${a.id}/sign`, { code: sign.code.value });
        toast('Договор подписан'); lastKey = ''; await refresh(); ctx.startPolling(refresh);
      } catch (err) { showFieldErrors(sign, err.fields); toast(err.message, 'error'); }
    };
    root.querySelectorAll('input[type=file][data-req]').forEach(inp => inp.onchange = async () => {
      const file = inp.files[0];
      if (!file) return;
      const fd = new FormData();
      fd.append('request_id', inp.dataset.req);
      fd.append('file', file);
      inp.closest('label').textContent = 'Загружаем…';
      try { await api('POST', `/applications/${a.id}/documents`, fd); toast('Документ загружен'); }
      catch (e) { toast(e.message, 'error'); }
      lastKey = ''; await refresh();
    });
    root.querySelectorAll('[data-doc]').forEach(b => b.onclick = () => {
      const d = v.document_requests.flatMap(r => r.documents).find(x => x.id === b.dataset.doc);
      downloadDoc(a.id, d);
    });
  }

  await refresh();
  ctx.startPolling(refresh);
}

// Двойное нажатие вместо модального confirm(): первое нажатие меняет подпись кнопки.
function confirmInline(btn, text) {
  if (btn.dataset.armed) return true;
  btn.dataset.armed = '1';
  const old = btn.textContent;
  btn.textContent = text;
  setTimeout(() => { delete btn.dataset.armed; btn.textContent = old; }, 3000);
  return false;
}

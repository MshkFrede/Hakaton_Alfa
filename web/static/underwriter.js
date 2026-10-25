// Рабочее место кредитного специалиста (андеррайтера): очередь заявок и досье с принятием решения.
import {
  api, esc, rub, shortRub, months, dt, date, ago, toast, session, parseMoney, fmtInput, statusBadge, pct,
  PROCESSING, FINAL, showFieldErrors, timeline, downloadDoc, ApiError,
} from './lib.js';
import { tracker } from './client.js';

const QUEUES = [
  { key: 'review', title: 'На разборе', statuses: ['MANUAL_REVIEW'] },
  { key: 'waiting', title: 'Ждём клиента', statuses: ['DOCS_REQUESTED', 'APPROVED', 'COUNTER_OFFER', 'SIGNING'] },
  { key: 'processing', title: 'В обработке', statuses: ['SUBMITTED', 'ENRICHMENT', 'SCORING', 'SIGNED'] },
  { key: 'done', title: 'Завершённые', statuses: ['ISSUED', 'REJECTED', 'DECLINED_BY_CLIENT', 'CANCELLED'] },
  { key: 'all', title: 'Все', statuses: [] },
];

let currentQueue = 'review';

export async function renderQueue(root, ctx) {
  let search = '';
  async function refresh() {
    const [apps, stats] = await Promise.all([
      api('GET', `/applications?q=${encodeURIComponent(search)}`),
      api('GET', '/stats'),
    ]);
    draw(apps.filter(a => a.status !== 'DRAFT'), stats);
  }

  function draw(apps, stats) {
    const q = QUEUES.find(x => x.key === currentQueue);
    const count = qq => qq.statuses.length ? apps.filter(a => qq.statuses.includes(a.status)).length : apps.length;
    const list = q.statuses.length ? apps.filter(a => q.statuses.includes(a.status)) : apps;
    const autoShare = stats.decisions ? stats.auto_decisions / stats.decisions : 0;
    const focused = document.activeElement?.name === 'q';
    root.innerHTML = `
      <div class="container">
        <div class="page-head"><div class="grow"><h1>Кредитный конвейер</h1>
          <div class="muted small">Заявки малого и микробизнеса · обновляется автоматически</div></div></div>
        <div class="grid grid-4" style="margin-bottom:20px">
          <div class="tile"><div class="label">Ждут решения специалиста</div><div class="value">${stats.by_status.MANUAL_REVIEW || 0}</div></div>
          <div class="tile"><div class="label">Решений принято</div><div class="value">${stats.decisions}</div></div>
          <div class="tile"><div class="label">Доля автоматических решений</div><div class="value">${stats.decisions ? pct(autoShare) : '—'}</div>
            <div class="sub">без участия специалиста</div></div>
          <div class="tile"><div class="label">Время до решения (среднее)</div><div class="value">${stats.decisions ? fmtDur(stats.avg_decision_seconds) : '—'}</div>
            <div class="sub">от подачи до первого решения</div></div>
        </div>
        <div class="card">
          <div class="actions" style="justify-content:space-between;margin-bottom:4px">
            <div class="tabs" style="margin:0;border:0">${QUEUES.map(x => `<button data-q="${x.key}" class="${x.key === currentQueue ? 'active' : ''}">${x.title}<span class="count">${count(x)}</span></button>`).join('')}</div>
            <input class="input" name="q" placeholder="Поиск: компания, ИНН, номер" value="${esc(search)}" style="max-width:280px">
          </div>
          ${list.length ? `<div class="table-wrap"><table class="table">
            <thead><tr><th>Заявка</th><th>Клиент</th><th>Продукт</th><th>Сумма / срок</th><th>Скоринг</th><th>Статус</th><th>Специалист</th><th>Изменена</th></tr></thead>
            <tbody>${list.map(a => `
              <tr class="row-link" data-href="#/bank/apps/${a.id}">
                <td><b>${esc(a.number)}</b></td>
                <td>${esc(a.company_name)}<div class="small muted num">ИНН ${esc(a.company_inn)}</div></td>
                <td>${esc(a.product_name)}</td>
                <td class="num nowrap">${rub(a.amount)}<div class="small muted">${a.term_months} мес.</div></td>
                <td>${a.grade ? `<span class="grade grade-${a.grade}">${a.grade}</span> <span class="small muted">${a.score}</span>` : '<span class="muted">—</span>'}</td>
                <td>${statusBadge(a.status, a.status_title)}</td>
                <td class="small">${esc(a.assigned_underwriter_name || '—')}</td>
                <td class="small muted nowrap">${ago(a.status_changed_at)}</td>
              </tr>`).join('')}</tbody></table></div>`
          : `<div class="empty"><div class="big">✨</div><p>${currentQueue === 'review' ? 'Очередь пуста — все заявки обработаны.' : 'Нет заявок'}</p></div>`}
        </div>
      </div>`;
    root.querySelectorAll('[data-q]').forEach(b => b.onclick = () => { currentQueue = b.dataset.q; draw(apps, stats); });
    root.querySelectorAll('[data-href]').forEach(tr => tr.onclick = () => { location.hash = tr.dataset.href; });
    const inp = root.querySelector('[name=q]');
    let t;
    inp.oninput = () => { search = inp.value; clearTimeout(t); t = setTimeout(refresh, 300); };
    if (focused) { inp.focus(); inp.setSelectionRange(inp.value.length, inp.value.length); }
  }

  await refresh();
  ctx.startPolling(async () => { if (document.activeElement?.name !== 'q') await refresh(); }, 4000);
}

function fmtDur(s) {
  if (s < 60) return `${Math.round(s)} с`;
  if (s < 3600) return `${Math.round(s / 60)} мин`;
  return `${(s / 3600).toFixed(1)} ч`;
}

const OUTCOME = {
  APPROVE: ['Одобрить', 'ok'], COUNTER_OFFER: ['Контрпредложение', 'ok'], MANUAL_REVIEW: ['Ручной разбор', 'warn'], REJECT: ['Отказ', 'bad'],
};
const SOURCE_TITLE = { EGRUL: 'ЕГРЮЛ / ЕГРИП', BKI: 'Бюро кредитных историй', FNS: 'ФНС «Прозрачный бизнес»' };

export async function renderDossier(root, id, ctx) {
  const docTypes = await api('GET', '/catalog/document-types');
  let lastKey = '';
  let tab = 'scoring';

  async function refresh(force) {
    const v = await api('GET', `/applications/${id}`);
    const key = `${v.application.status}:${v.application.version}:${v.events.length}`;
    if (key !== lastKey || force) {
      // не перерисовываем, пока специалист заполняет форму решения
      if (!force && root.querySelector('#decision')?.contains(document.activeElement)) return;
      lastKey = key; draw(v);
    }
  }

  function draw(v) {
    const a = v.application;
    const d = v.dossier;
    const rec = d.scoring ? d.scoring.recommendation : null;
    root.innerHTML = `
      <div class="container">
        <a class="back" href="#/bank">← К очереди</a>
        <div class="page-head">
          <div class="grow">
            <h1>${esc(a.number)} · ${esc(a.company_name)}</h1>
            <div class="muted small">ИНН ${esc(a.company_inn)} · ${esc(a.product_name)} · ${rub(a.amount)} на ${months(a.term_months)} · подана ${dt(a.submitted_at)}</div>
          </div>
          ${statusBadge(a.status, a.status_title)}
        </div>
        <div class="card" style="margin-bottom:16px">${tracker(a.status)}</div>
        <div class="split">
          <div>
            <div class="tabs">
              ${[['scoring', 'Скоринг и политика'], ['sources', 'Внешние данные'], ['request', 'Заявка'], ['docs', `Документы${v.document_requests.length ? ` <span class="count">${v.document_requests.length}</span>` : ''}`], ['history', 'Журнал']]
                .map(([k, t]) => `<button data-tab="${k}" class="${tab === k ? 'active' : ''}">${t}</button>`).join('')}
            </div>
            <div id="tab"></div>
          </div>
          <div class="stack sticky">${decisionPanel(v, rec)}</div>
        </div>
      </div>`;
    const tabEl = root.querySelector('#tab');
    const tabs = { scoring: scoringTab, sources: sourcesTab, request: requestTab, docs: docsTab, history: historyTab };
    tabEl.innerHTML = tabs[tab](v, rec);
    root.querySelectorAll('[data-tab]').forEach(b => b.onclick = () => { tab = b.dataset.tab; draw(v); });
    root.querySelectorAll('[data-doc]').forEach(b => b.onclick = () => {
      const doc = v.document_requests.flatMap(r => r.documents).find(x => x.id === b.dataset.doc);
      downloadDoc(a.id, doc);
    });
    bindDecision(v, rec);
  }

  // ---------- Скоринг ----------
  function scoringTab(v, rec) {
    const sc = v.dossier.scoring;
    if (!sc) {
      return `<div class="card"><div class="empty">${PROCESSING.includes(v.application.status) ? '<span class="spinner"></span><p>Скоринг ещё выполняется…</p>' : '<p>Скоринг не проводился</p>'}</div></div>`;
    }
    const factors = sc.factors;
    const maxAbs = Math.max(60, ...factors.map(f => Math.abs(f.points)));
    const [oTitle, oKind] = OUTCOME[rec.suggested] || [rec.suggested, ''];
    return `
      <div class="grid grid-2">
        <div class="card">
          <div class="muted small">Оценка риска заёмщика · ${esc(sc.model_version)}</div>
          <div class="score-head" style="margin:8px 0 4px">
            <span class="grade grade-${sc.grade}" style="width:48px;height:48px;font-size:22px">${sc.grade}</span>
            <div><div class="score-num">${sc.score}</div><div class="small muted">балл из 1000 · PD ≈ ${(sc.pd * 100).toFixed(0)}%</div></div>
          </div>
          <p class="small muted" style="margin:8px 0 0">Скоринг оценивает компанию (риск), а не размер кредита. Сумму и условия определяет кредитная политика →</p>
        </div>
        <div class="card">
          <div class="muted small">Рекомендация кредитной политики</div>
          <div style="margin:10px 0"><span class="badge ${oKind}" style="font-size:14px;padding:5px 12px">${esc(oTitle)}</span>
            ${rec.outcome === 'MANUAL_REVIEW' ? '<span class="badge warn plain">передано специалисту</span>' : '<span class="badge info plain">решение принято автоматически</span>'}</div>
          <dl class="kv small">
            <dt>Ставка по рейтингу</dt><dd>${rec.rate}%</dd>
            <dt>Нагрузка с новым платежом</dt><dd class="${rec.debt_to_revenue > 0.3 ? 'num' : 'num'}" style="color:${rec.debt_to_revenue > 0.3 ? 'var(--bad)' : 'inherit'}">${rec.debt_to_revenue >= 99 ? 'нет данных' : pct(rec.debt_to_revenue)} <span class="muted">(лимит 30%)</span></dd>
            <dt>Максимум по нагрузке</dt><dd class="num">${rec.max_amount > 0 ? rub(rec.max_amount) : '—'}</dd>
            ${rec.offer ? `<dt>Предложение системы</dt><dd class="num">${rub(rec.offer.amount)} · ${rec.offer.term_months} мес. · ${rub(rec.offer.monthly_payment)}/мес</dd>` : ''}
          </dl>
        </div>
      </div>
      ${sc.stop_factors.length ? `<div class="card" style="margin-top:16px"><div class="card-head"><h3>Стоп-факторы</h3></div>${sc.stop_factors.map(s => `<div class="stop">⛔ ${esc(s.description)}</div>`).join('')}</div>` : ''}
      <div class="card" style="margin-top:16px">
        <div class="card-head"><h3>Из чего сложился балл</h3><span class="small muted">База 500</span></div>
        ${factors.map(f => `
          <div class="factor">
            <div>${esc(f.name)}<div class="small muted">${esc(f.value)}</div></div>
            <div class="bar"><i class="${f.points >= 0 ? 'pos' : 'neg'}" style="width:${Math.abs(f.points) / maxAbs * 50}%"></i></div>
            <div class="pts ${f.points > 0 ? 'pos' : f.points < 0 ? 'neg' : ''}">${f.points > 0 ? '+' : ''}${f.points}</div>
          </div>`).join('')}
      </div>
      <div class="card" style="margin-top:16px">
        <div class="card-head"><h3>Обоснование</h3></div>
        <ul class="reasons">${rec.reasons.map(r => `<li>${esc(r)}</li>`).join('')}</ul>
        <p class="small muted" style="margin-top:10px">Клиент видит: «${esc(rec.client_message)}»</p>
      </div>`;
  }

  // ---------- Внешние источники ----------
  function sourcesTab(v) {
    const enr = v.dossier.enrichment;
    if (!enr.length) return `<div class="card"><div class="empty"><span class="spinner"></span><p>Данные запрашиваются…</p></div></div>`;
    const by = Object.fromEntries(enr.map(e => [e.source, e]));
    const head = (code, e) => `<div class="source-head"><strong>${SOURCE_TITLE[code]}</strong>
      ${e ? (e.status === 'OK' ? `<span class="badge ok">получено</span>` : `<span class="badge bad">ошибка</span>`) : ''}
      ${e ? `<span class="small muted">${e.duration_ms} мс · попыток: ${e.attempts}</span>` : ''}</div>`;
    const err = e => `<div class="notice bad">Источник недоступен: ${esc(e.error)}. Данные нужно проверить вручную или запросить у клиента.</div>`;

    const eg = by.EGRUL, bk = by.BKI, fn = by.FNS;
    let egHtml = '', bkHtml = '', fnHtml = '';
    if (eg?.status === 'OK') {
      const c = eg.data;
      egHtml = `<dl class="kv small">
        <dt>Наименование</dt><dd>${esc(c.full_name)}</dd><dt>Статус</dt><dd>${c.status === 'ACTIVE' ? 'Действующая' : esc(c.status)}</dd>
        <dt>ОГРН</dt><dd class="num">${esc(c.ogrn)}</dd><dt>Регистрация</dt><dd>${date(c.registration_date)}</dd>
        <dt>Руководитель</dt><dd>${esc(c.director.name)}, ${esc(c.director.position)}</dd>
        <dt>Учредители</dt><dd>${(c.founders || []).map(f => `${esc(f.name)} — ${f.share}%`).join('<br>') || '—'}</dd>
        <dt>ОКВЭД</dt><dd>${esc(c.okved.code)} ${esc(c.okved.name)}</dd><dt>Адрес</dt><dd>${esc(c.address)}</dd></dl>`;
    } else if (eg) egHtml = err(eg);
    if (bk?.status === 'OK') {
      const b = bk.data;
      bkHtml = `<dl class="kv small">
        <dt>Кредитная история</dt><dd>${b.credit_history_months} мес.</dd><dt>Закрытых кредитов</dt><dd>${b.closed_loans_count}</dd>
        <dt>Макс. просрочка за 12 мес.</dt><dd style="color:${b.max_overdue_days_12m > 0 ? 'var(--bad)' : 'inherit'}">${b.max_overdue_days_12m ? b.max_overdue_days_12m + ' дн.' : 'нет'}</dd>
        <dt>Текущая просроченная задолженность</dt><dd style="color:${b.current_overdue_amount ? 'var(--bad)' : 'inherit'}">${rub(b.current_overdue_amount)}</dd>
        <dt>Запросов за 30 дней</dt><dd>${b.inquiries_30d}</dd></dl>
        ${b.active_loans?.length ? `<div class="table-wrap" style="margin-top:12px"><table class="table small"><thead><tr><th>Кредитор</th><th>Тип</th><th>Остаток</th><th>Платёж</th><th>Просрочка</th></tr></thead>
          <tbody>${b.active_loans.map(l => `<tr><td>${esc(l.creditor)}</td><td>${esc(l.type)}</td><td class="num">${rub(l.balance)}</td><td class="num">${rub(l.monthly_payment)}</td>
          <td>${l.current_overdue_days ? `<span class="badge bad">${l.current_overdue_days} дн.</span>` : '—'}</td></tr>`).join('')}</tbody></table></div>` : '<p class="small muted" style="margin-top:8px">Действующих кредитов нет</p>'}`;
    } else if (bk) bkHtml = err(bk);
    if (fn?.status === 'OK') {
      const f = fn.data;
      const declared = v.application.annual_revenue;
      const diff = f.reported_revenue_last_year ? declared / f.reported_revenue_last_year - 1 : 0;
      fnHtml = `<dl class="kv small">
        <dt>Выручка по отчётности ${f.reporting_year}</dt><dd class="num">${rub(f.reported_revenue_last_year)}</dd>
        <dt>Заявлено клиентом</dt><dd class="num">${rub(declared)} ${Math.abs(diff) > 0.05 ? `<span class="badge ${diff > 0.3 ? 'bad' : 'plain'}">${diff > 0 ? '+' : ''}${Math.round(diff * 100)}%</span>` : ''}</dd>
        <dt>Сотрудников</dt><dd>${f.employees_count}</dd><dt>Режим налогообложения</dt><dd>${esc(f.tax_regime)}</dd>
        <dt>Налоговая задолженность</dt><dd style="color:${f.tax_arrears ? 'var(--bad)' : 'inherit'}">${rub(f.tax_arrears)}</dd>
        <dt>Признаки</dt><dd>${[f.mass_address && 'адрес массовой регистрации', f.mass_director && 'массовый руководитель', f.disqualified_director && 'дисквалифицированный руководитель'].filter(Boolean).join(', ') || 'не выявлены'}</dd></dl>`;
    } else if (fn) fnHtml = err(fn);
    return `<div class="stack">
      <div class="source">${head('EGRUL', eg)}${egHtml}</div>
      <div class="source">${head('BKI', bk)}${bkHtml}</div>
      <div class="source">${head('FNS', fn)}${fnHtml}</div>
      <p class="small muted">Источники смоделированы (синтетические данные). Запросы выполняются параллельно, с таймаутом и повторами.</p></div>`;
  }

  function requestTab(v) {
    const a = v.application, c = v.company;
    return `<div class="card"><dl class="kv">
      <dt>Продукт</dt><dd>${esc(a.product_name)}</dd><dt>Цель</dt><dd>${esc(a.purpose)}${a.purpose_comment ? ` — ${esc(a.purpose_comment)}` : ''}</dd>
      <dt>Сумма</dt><dd class="num">${rub(a.amount)}</dd><dt>Срок</dt><dd>${months(a.term_months)}</dd>
      <dt>Выручка (заявлено)</dt><dd class="num">${rub(a.annual_revenue)}</dd><dt>Текущие платежи (заявлено)</dt><dd class="num">${rub(a.existing_monthly_payments)}</dd>
      <dt>Обеспечение</dt><dd>${a.has_collateral ? esc(a.collateral_description) : 'нет'}</dd>
      <dt>Телефон</dt><dd>${esc(a.contact_phone)}</dd><dt>Согласие БКИ</dt><dd>${a.bki_consent ? 'получено' : 'нет'}</dd>
      <dt>Компания</dt><dd>${esc(c?.full_name)}</dd><dt>Руководитель</dt><dd>${esc(c?.director_name)}</dd>
    </dl></div>`;
  }

  function docsTab(v) {
    if (!v.document_requests.length) return '<div class="card"><div class="empty"><p>Документы не запрашивались</p></div></div>';
    return `<div class="stack">${v.document_requests.map(r => `
      <div class="doc-req"><div class="grow"><b>${esc(r.title)}</b><div class="small muted">Запрошено ${dt(r.created_at)}${r.comment ? ` · ${esc(r.comment)}` : ''}</div>
        ${r.documents.map(d => `<div><button class="file-link" data-doc="${d.id}">📎 ${esc(d.file_name)}</button> <span class="small muted">${Math.ceil(d.size / 1024)} КБ · ${dt(d.created_at)}</span></div>`).join('')}</div>
        ${r.status === 'UPLOADED' ? '<span class="badge ok">Получено</span>' : '<span class="badge warn">Ожидаем</span>'}</div>`).join('')}</div>`;
  }

  function historyTab(v) {
    return `<div class="card">${timeline(v.events, { bank: true })}</div>`;
  }

  // ---------- Панель решения ----------
  function decisionPanel(v, rec) {
    const a = v.application;
    if (a.status !== 'MANUAL_REVIEW') {
      let body = `<p class="muted">Решение по заявке в этом статусе не требуется.</p>`;
      if (a.offer) body = `<dl class="kv small"><dt>Сумма</dt><dd class="num">${rub(a.offer.amount)}</dd><dt>Срок</dt><dd>${a.offer.term_months} мес.</dd>
        <dt>Ставка</dt><dd>${a.offer.rate}%</dd><dt>Платёж</dt><dd class="num">${rub(a.offer.monthly_payment)}</dd></dl>`;
      return `<div class="card"><div class="card-head"><h3>Условия</h3></div>${body}
        ${a.assigned_underwriter_name ? `<p class="small muted" style="margin-top:10px">Специалист: ${esc(a.assigned_underwriter_name)}</p>` : ''}</div>`;
    }
    const mine = a.assigned_underwriter_id === session.user.id;
    if (!mine) {
      return `<div class="card"><div class="card-head"><h3>Решение</h3></div>
        ${rec?.manual_reasons?.length ? `<div class="notice warn" style="margin-bottom:12px"><div><b>Почему на ручном разборе:</b><ul class="reasons" style="margin-top:4px">${rec.manual_reasons.map(r => `<li>${esc(r)}</li>`).join('')}</ul></div></div>` : ''}
        ${a.assigned_underwriter_name ? `<p class="small">Сейчас у специалиста: <b>${esc(a.assigned_underwriter_name)}</b></p>` : ''}
        <button class="btn btn-primary btn-block" id="assign">${a.assigned_underwriter_name ? 'Забрать себе' : 'Взять в работу'}</button></div>`;
    }
    const o = rec?.offer || { amount: a.amount, term_months: a.term_months, rate: rec?.rate };
    const sugg = rec?.suggested === 'REJECT' ? 'REJECT' : rec?.suggested === 'COUNTER_OFFER' ? 'COUNTER_OFFER' : 'APPROVE';
    return `<div class="card" id="decision">
      <div class="card-head"><h3>Решение</h3><span class="small muted">рекомендация: ${esc(OUTCOME[rec?.suggested]?.[0] || '—')}</span></div>
      ${rec?.manual_reasons?.length ? `<div class="notice warn small" style="margin-bottom:12px"><div>${rec.manual_reasons.map(esc).join('<br>')}</div></div>` : ''}
      <div class="seg" style="margin-bottom:14px">
        ${[['APPROVE', 'Одобрить'], ['COUNTER_OFFER', 'Изменить условия'], ['REQUEST_DOCS', 'Запросить документы'], ['REJECT', 'Отказать']]
          .map(([k, t]) => `<button data-action="${k}" class="${k === sugg ? 'active' : ''}">${t}</button>`).join('')}
      </div>
      <form id="dform" novalidate>
        <input type="hidden" name="action" value="${sugg}">
        <div data-for="APPROVE COUNTER_OFFER">
          <div class="grid grid-2" style="gap:10px">
            <label class="field"><span>Сумма, ₽</span><input class="input num" name="amount" value="${fmtInput(o.amount)}" inputmode="numeric"></label>
            <label class="field"><span>Срок, мес.</span><input class="input num" name="term_months" value="${o.term_months}" inputmode="numeric"></label>
          </div>
          <label class="field"><span>Ставка, % годовых</span><input class="input num" name="rate" value="${o.rate ?? v.product.base_rate}" inputmode="decimal"></label>
          <div class="notice info small" id="pay" style="margin-bottom:14px"></div>
        </div>
        <div data-for="REQUEST_DOCS">
          <div class="field"><span>Какие документы нужны</span>
            ${docTypes.map(d => `<label class="check" style="margin-bottom:6px"><input type="checkbox" name="doc" value="${d.code}"> <span>${esc(d.title)}</span></label>`).join('')}
            <input type="hidden" name="documents"></div>
        </div>
        <label class="field"><span>Сообщение клиенту <span class="muted" style="font-weight:400">(необязательно)</span></span>
          <textarea class="input" name="client_message" placeholder="Будет показано в кабинете клиента"></textarea></label>
        <label class="field"><span>Внутренний комментарий <span class="muted" style="font-weight:400" data-for="APPROVE COUNTER_OFFER REQUEST_DOCS">(виден только банку)</span><span data-for="REJECT" style="color:var(--bad)">— обязательно для отказа</span></span>
          <textarea class="input" name="comment" placeholder="Обоснование решения для аудита"></textarea></label>
        <button class="btn btn-primary btn-block btn-lg" id="dsubmit">Принять решение</button>
      </form>
    </div>`;
  }

  function bindDecision(v, rec) {
    const a = v.application;
    const assign = root.querySelector('#assign');
    if (assign) assign.onclick = async () => {
      try { await api('POST', `/applications/${a.id}/assign`); toast('Заявка взята в работу'); await refresh(true); } catch (e) { toast(e.message, 'error'); }
    };
    const form = root.querySelector('#dform');
    if (!form) return;
    const setAction = act => {
      form.action.value = act;
      root.querySelectorAll('[data-action]').forEach(b => b.classList.toggle('active', b.dataset.action === act));
      form.querySelectorAll('[data-for]').forEach(el => { el.style.display = el.dataset.for.split(' ').includes(act) ? '' : 'none'; });
      const label = { APPROVE: 'Одобрить заявку', COUNTER_OFFER: 'Направить предложение', REQUEST_DOCS: 'Запросить документы', REJECT: 'Отказать' }[act];
      const btn = form.querySelector('#dsubmit');
      btn.textContent = label;
      btn.className = `btn btn-block btn-lg ${act === 'REJECT' ? 'btn-danger' : 'btn-primary'}`;
      if (act === 'APPROVE') { form.amount.value = fmtInput(a.amount); form.term_months.value = a.term_months; }
      if (act === 'COUNTER_OFFER' && rec?.offer) { form.amount.value = fmtInput(rec.offer.amount); form.term_months.value = rec.offer.term_months; }
      updatePay();
    };
    const updatePay = () => {
      const amount = parseMoney(form.amount.value), term = Number(form.term_months.value), rate = Number(String(form.rate.value).replace(',', '.'));
      const r = rate / 1200;
      const pay = r ? amount * r / (1 - Math.pow(1 + r, -term)) : amount / term;
      const monthlyRevenue = a.annual_revenue / 12;
      const el = form.querySelector('#pay');
      if (!amount || !term || !rate) { el.textContent = 'Укажите сумму, срок и ставку'; return; }
      el.innerHTML = `Платёж: <b>${rub(Math.ceil(pay))}</b>/мес · переплата ${rub(Math.ceil(pay) * term - amount)}`
        + (monthlyRevenue ? ` · ${Math.round((pay + a.existing_monthly_payments) / monthlyRevenue * 100)}% заявленной выручки` : '');
    };
    root.querySelectorAll('[data-action]').forEach(b => b.onclick = () => setAction(b.dataset.action));
    form.oninput = e => {
      if (e.target.name === 'amount') form.amount.value = fmtInput(parseMoney(form.amount.value));
      if (['amount', 'term_months', 'rate'].includes(e.target.name)) updatePay();
    };
    setAction(form.action.value);
    form.onsubmit = async e => {
      e.preventDefault();
      const act = form.action.value;
      const body = { action: act, version: a.version, client_message: form.client_message.value, comment: form.comment.value };
      if (act === 'APPROVE' || act === 'COUNTER_OFFER') {
        body.amount = parseMoney(form.amount.value);
        body.term_months = Number(form.term_months.value);
        body.rate = Number(String(form.rate.value).replace(',', '.'));
      }
      if (act === 'REQUEST_DOCS') body.documents = [...form.querySelectorAll('[name=doc]:checked')].map(c => ({ doc_type: c.value }));
      const btn = form.querySelector('#dsubmit');
      btn.disabled = true;
      try {
        await api('POST', `/applications/${a.id}/decision`, body);
        toast('Решение принято');
        await refresh(true);
      } catch (err) {
        btn.disabled = false;
        if (err instanceof ApiError && err.status === 422) showFieldErrors(form, err.fields);
        toast(err.message, 'error');
        if (err.code === 'VERSION_CONFLICT') await refresh(true);
      }
    };
  }

  await refresh(true);
  ctx.startPolling(() => refresh(false), 3000);
}

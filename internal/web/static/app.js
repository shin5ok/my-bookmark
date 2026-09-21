(() => {
  'use strict';
  const csrf = document.querySelector('meta[name="csrf-token"]')?.content;
  const active = new Set();
  const toast = message => {
    const el = document.querySelector('.toast');
    el.textContent = message;
    el.hidden = false;
    clearTimeout(el.timer);
    el.timer = setTimeout(() => { el.hidden = true; }, 9000);
  };
  const article = id => document.querySelector(`[data-article="${id}"]`);
  const elStatus = id => article(id)?.querySelector('.summary')?.dataset.status || '';
  function update(id, data) {
    const el = article(id);
    if (!el) return;
    const box = el.querySelector('.summary-content');
    const button = el.querySelector('.generate-button');
    const summary = el.querySelector('.summary');
    el.querySelector('.source-list')?.remove();
    el.querySelector('.summary').dataset.status = data.status;
    if (data.status === 'ready' && Array.isArray(data.points)) {
      const list = document.createElement('ul');
      data.points.slice(0, 5).forEach(point => {
        const li = document.createElement('li'); li.textContent = point; list.append(li);
      });
      box.replaceChildren(list);
      if (data.title) el.querySelector('.article-title a').textContent = data.title;
      if (Array.isArray(data.sources) && data.sources.length) {
        const sources = document.createElement('p'); sources.className = 'source-list';
        sources.append(document.createTextNode('参照元：'));
        data.sources.forEach((source, index) => {
          let url;
          try { url = new URL(source); } catch { return; }
          if (!['http:', 'https:'].includes(url.protocol)) return;
          if (index) sources.append(document.createTextNode(' · '));
          const link = document.createElement('a'); link.href = url.href;
          link.target = '_blank'; link.rel = 'noopener noreferrer'; link.textContent = url.hostname;
          sources.append(link);
        });
        if (sources.querySelector('a')) summary.insertAdjacentElement('afterend', sources);
      }
      button?.remove();
    } else {
      const text = document.createElement('p'); text.className = 'summary-message';
      text.textContent = ['queued', 'processing'].includes(data.status)
        ? (data.progress || '記事の要点をまとめています…')
        : data.status === 'interrupted' ? '処理が中断されました。再試行できます。'
        : data.status === 'failed' ? (data.error || '要約を作成できませんでした。再試行できます。') : '要約はまだありません。作成できます。';
      box.replaceChildren(text);
      if (button) { button.disabled = ['queued','processing'].includes(data.status); button.textContent = data.status === 'pending' ? '要約を作成する' : '要約を再試行する'; }
    }
  }
  async function poll(id) {
    for (let i = 0; i < 180; i++) {
      await new Promise(resolve => setTimeout(resolve, 3000));
      try {
        const response = await fetch(`/articles/${id}/summary`);
        if (!response.ok) return;
        const data = await response.json(); update(id, data);
        if (!['queued','processing'].includes(data.status)) return;
      } catch { return; }
    }
  }
  async function generate(id) {
    if (!csrf || !/^[a-f0-9]{64}$/.test(id) || active.has(id)) return;
    active.add(id);
    const button = article(id)?.querySelector('.generate-button');
    if (button) { button.disabled = true; button.textContent = '要約を作成中…'; }
    try {
      const response = await fetch(`/articles/${id}/summary`, {
        method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: new URLSearchParams({ csrf }),
      });
      if (!response.headers.get('content-type')?.includes('application/json')) {
        throw new Error(response.status === 401 || response.status === 403 ? 'ページを再読み込みし、ログインしてから再試行してください。' : '通信に失敗しました。時間をおいて再試行してください。');
      }
      const data = await response.json();
      if (!response.ok) throw new Error(data.error || '要約を作成できませんでした。');
      update(id, data);
      if (['queued','processing'].includes(data.status)) await poll(id);
    } catch (error) {
      toast(error.message);
      if (button) button.disabled = false;
    } finally {
      active.delete(id);
      if (button?.isConnected && !['queued','processing'].includes(elStatus(id))) { button.textContent = elStatus(id) === 'pending' ? '要約を作成する' : '要約を再試行する'; button.disabled = false; }
    }
  }
  document.querySelectorAll('.generate-button').forEach(button => button.addEventListener('click', () => generate(button.dataset.id)));
  document.querySelectorAll('.summary[data-status="queued"], .summary[data-status="processing"]').forEach(el => poll(el.closest('[data-article]').dataset.article));
  document.querySelectorAll('.delete-form').forEach(form => form.addEventListener('submit', event => {
    if (!window.confirm('このブックマークを削除しますか？')) event.preventDefault();
  }));
})();

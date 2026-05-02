const body = document.body;
  const apiPath = window.AppCommon.detectApiPath();
  const logoutBtn = document.getElementById('logoutBtn');
  const homeBtn = document.getElementById('homeBtn');
  const uploadBtn = document.getElementById('uploadBtn');
  const queryBtn = document.getElementById('queryBtn');
  const categorySelect = document.getElementById('categorySelect');
  const tagFilter = document.getElementById('tagFilter');
  const imageList = document.getElementById('imageList');
  const result = document.getElementById('result');
  const systemSummary = document.getElementById('systemSummary');
  const tagSummary = document.getElementById('tagSummary');
  const prevPageBtn = document.getElementById('prevPageBtn');
  const nextPageBtn = document.getElementById('nextPageBtn');
  const pageInfo = document.getElementById('pageInfo');
  const pageSizeSelect = document.getElementById('pageSizeSelect');

  let currentPage = 1;
  let pageSize = Number(pageSizeSelect.value) || 100;
  let total = 0;

  function refreshBackground() {
    window.AppCommon.refreshBackground(body, apiPath);
  }

  function formatApiError(status, payload, fallbackText) {
    if (payload && typeof payload === 'object') {
      const message = payload.message || payload.error || `请求失败（${status}）`;
      const detail = payload.detail ? `，详情：${payload.detail}` : '';
      const code = payload.code ? ` [${payload.code}]` : '';
      return `${message}${code}${detail}`;
    }
    return fallbackText ? `请求失败（${status}）：${fallbackText}` : `请求失败（${status}）`;
  }

  async function readApiResponse(res) {
    const contentType = (res.headers.get('content-type') || '').toLowerCase();
    let payload = null;
    let text = '';

    if (contentType.includes('application/json')) {
      try {
        payload = await res.json();
      } catch (_) {}
    } else {
      text = await res.text();
      if (text) {
        try {
          payload = JSON.parse(text);
        } catch (_) {}
      }
    }

    if (!res.ok) {
      throw new Error(formatApiError(res.status, payload, text));
    }
    return payload || {};
  }

  async function ensureLoggedIn() {
    try {
      const res = await fetch('/api/auth/status', { credentials: 'same-origin' });
      const data = await readApiResponse(res);
      if (!data.loggedIn) {
        location.href = '/public/login.html';
        return false;
      }
      return true;
    } catch (_) {
      location.href = '/public/login.html';
      return false;
    }
  }

  logoutBtn.addEventListener('click', async () => {
    try {
      await fetch('/api/logout', { method: 'POST', credentials: 'same-origin' });
    } catch (_) {}
    location.href = '/public/login.html';
  });
  homeBtn.addEventListener('click', () => {
    location.href = '/public/index.html';
  });
  uploadBtn.addEventListener('click', () => {
    location.href = '/public/upload.html';
  });

  function renderTagSummary(items) {
    if (!Array.isArray(items) || items.length === 0) {
      tagSummary.textContent = '暂无标签';
      return;
    }
    tagSummary.textContent = items.map(x => `${x.tag} (${x.count})`).join('  |  ');
  }

  function renderPagination() {
    const totalPages = Math.max(1, Math.ceil(total / pageSize));
    if (currentPage > totalPages) {
      currentPage = totalPages;
    }
    pageInfo.textContent = `第 ${currentPage} / ${totalPages} 页 · 共 ${total} 张`;
    prevPageBtn.disabled = currentPage <= 1;
    nextPageBtn.disabled = currentPage >= totalPages;
  }

  function formatBytes(bytes) {
    const n = Number(bytes) || 0;
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    let value = n;
    let idx = 0;
    while (value >= 1024 && idx < units.length - 1) {
      value /= 1024;
      idx += 1;
    }
    return `${value.toFixed(idx === 0 ? 0 : 2)} ${units[idx]}`;
  }

  function makeItemRow(item) {
    const row = document.createElement('div');
    row.className = 'admin-item';

    const previewWrap = document.createElement('div');
    previewWrap.className = 'admin-preview';

    const thumb = document.createElement('img');
    thumb.className = 'admin-thumb';
    thumb.loading = 'lazy';
    thumb.src = '/images/' + item.path;
    thumb.alt = item.path;

    const pathWrap = document.createElement('div');
    pathWrap.className = 'admin-meta';

    const pathEl = document.createElement('div');
    pathEl.className = 'admin-path';
    pathEl.textContent = item.path;

    const originalLink = document.createElement('a');
    originalLink.className = 'admin-original-link';
    originalLink.href = '/images/' + item.path;
    originalLink.target = '_blank';
    originalLink.rel = 'noopener';
    originalLink.textContent = '查看原图';

    pathWrap.appendChild(pathEl);
    pathWrap.appendChild(originalLink);
    previewWrap.appendChild(thumb);
    previewWrap.appendChild(pathWrap);

    const tagsEl = document.createElement('input');
    tagsEl.type = 'text';
    tagsEl.value = Array.isArray(item.tags) ? item.tags.join(', ') : '';
    tagsEl.placeholder = '输入标签，逗号分隔';

    const replaceBtn = document.createElement('button');
    replaceBtn.type = 'button';
    replaceBtn.textContent = '覆盖标签';

    const appendBtn = document.createElement('button');
    appendBtn.type = 'button';
    appendBtn.textContent = '追加标签';

    const deleteBtn = document.createElement('button');
    deleteBtn.type = 'button';
    deleteBtn.className = 'danger-btn';
    deleteBtn.textContent = '删除图片';

    async function submit(mode) {
      try {
        const raw = tagsEl.value || '';
        const tags = raw.split(/[，,;；\s]+/).map(s => s.trim()).filter(Boolean);
        const res = await fetch('/api/admin/image/tags', {
          method: 'POST',
          credentials: 'same-origin',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ path: item.path, tags, mode })
        });

        const json = await readApiResponse(res);
        tagsEl.value = (json.tags || []).join(', ');
        result.textContent = `已更新: ${json.path}`;
        await loadTagSummary();
      } catch (err) {
        result.textContent = '保存失败: ' + (err && err.message ? err.message : err);
      }
    }

    replaceBtn.addEventListener('click', () => submit('replace'));
    appendBtn.addEventListener('click', () => submit('append'));
    deleteBtn.addEventListener('click', async () => {
      const ok = confirm(`确定删除这张图片吗？\n${item.path}`);
      if (!ok) return;
      try {
        const res = await fetch('/api/admin/image/delete', {
          method: 'POST',
          credentials: 'same-origin',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ path: item.path })
        });
        const json = await readApiResponse(res);
        result.textContent = `已删除: ${json.path}`;
        await Promise.all([loadSystemSummary(), loadTagSummary()]);
        await loadImages(false);
      } catch (err) {
        result.textContent = '删除失败: ' + (err && err.message ? err.message : err);
      }
    });

    const actionWrap = document.createElement('div');
    actionWrap.className = 'admin-actions';
    actionWrap.appendChild(replaceBtn);
    actionWrap.appendChild(appendBtn);
    actionWrap.appendChild(deleteBtn);

    row.appendChild(previewWrap);
    row.appendChild(tagsEl);
    row.appendChild(actionWrap);
    return row;
  }

  async function loadTagSummary() {
    try {
      const res = await fetch('/api/admin/tags', { credentials: 'same-origin' });
      const json = await readApiResponse(res);
      renderTagSummary(json.items || []);
    } catch (err) {
      tagSummary.textContent = '加载失败: ' + (err && err.message ? err.message : err);
    }
  }

  async function loadSystemSummary() {
    try {
      const res = await fetch('/api/admin/system', { credentials: 'same-origin' });
      const json = await readApiResponse(res);
      const lines = [
        `图片数量：${json.imageCount || 0}`,
        `已用空间：${formatBytes(json.usedBytes || 0)}`
      ];
      if (json.maxBytes) {
        lines.push(`空间上限：${formatBytes(json.maxBytes)}`);
        lines.push(`剩余空间：${formatBytes(json.freeBytes || 0)}`);
      }
      systemSummary.textContent = lines.join('\n');
    } catch (err) {
      systemSummary.textContent = '加载失败: ' + (err && err.message ? err.message : err);
    }
  }

  async function loadImages(resetPage = false) {
    if (resetPage) {
      currentPage = 1;
    }

    const params = new URLSearchParams();
    if (categorySelect.value) params.set('category', categorySelect.value);
    if (tagFilter.value.trim()) params.set('tag', tagFilter.value.trim());
    params.set('page', String(currentPage));
    params.set('pageSize', String(pageSize));

    try {
      const res = await fetch('/api/admin/images?' + params.toString(), { credentials: 'same-origin' });
      const json = await readApiResponse(res);

      const items = Array.isArray(json.items) ? json.items : [];
      total = Number(json.total) || 0;
      currentPage = Number(json.page) || currentPage;
      pageSize = Number(json.pageSize) || pageSize;
      pageSizeSelect.value = String(pageSize);

      imageList.replaceChildren();
      if (items.length === 0) {
        imageList.textContent = '没有匹配图片';
      } else {
        items.forEach(item => imageList.appendChild(makeItemRow(item)));
      }
      renderPagination();
    } catch (err) {
      imageList.textContent = '加载失败: ' + (err && err.message ? err.message : err);
      total = 0;
      renderPagination();
    }
  }

  queryBtn.addEventListener('click', () => {
    loadImages(true);
  });

  tagFilter.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      loadImages(true);
    }
  });

  prevPageBtn.addEventListener('click', () => {
    if (currentPage <= 1) return;
    currentPage -= 1;
    loadImages(false);
  });

  nextPageBtn.addEventListener('click', () => {
    const totalPages = Math.max(1, Math.ceil(total / pageSize));
    if (currentPage >= totalPages) return;
    currentPage += 1;
    loadImages(false);
  });

  pageSizeSelect.addEventListener('change', () => {
    pageSize = Number(pageSizeSelect.value) || 100;
    loadImages(true);
  });

  (async () => {
    const ok = await ensureLoggedIn();
    if (!ok) return;
    refreshBackground();
    renderPagination();
    await loadSystemSummary();
    await loadTagSummary();
    await loadImages(true);
  })();

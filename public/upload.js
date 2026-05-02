const form = document.getElementById('uploadForm');
  const result = document.getElementById('result');
  const body = document.body;
  const apiPath = window.AppCommon.detectApiPath();

  const dropZone = document.getElementById('dropZone');
  const dropHint = document.getElementById('dropHint');
  const fileInput = document.getElementById('fileInput');
  const categorySelect = document.getElementById('categorySelect');
  const logoutBtn = document.getElementById('logoutBtn');
  const homeBtn = document.getElementById('homeBtn');
  const adminBtn = document.getElementById('adminBtn');
  const tagsInput = document.getElementById('tagsInput');

  let selectedFile = null;

  function refreshBackground() {
    window.AppCommon.refreshBackground(body, apiPath);
  }

  function setSelectedFile(file) {
    selectedFile = file || null;
    if (selectedFile) {
      dropHint.innerText = `已选择：${selectedFile.name}（${Math.ceil(selectedFile.size / 1024)} KB）`;
    } else {
      dropHint.innerText = '拖拽图片到这里，或点击选择文件';
    }
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
  adminBtn.addEventListener('click', () => {
    location.href = '/public/admin.html';
  });

  dropZone.addEventListener('click', () => fileInput.click());
  dropZone.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      fileInput.click();
    }
  });

  fileInput.addEventListener('change', () => {
    setSelectedFile(fileInput.files && fileInput.files[0]);
  });

  ['dragenter', 'dragover'].forEach((evt) => {
    dropZone.addEventListener(evt, (e) => {
      e.preventDefault();
      e.stopPropagation();
      dropZone.classList.add('dragover');
    });
  });

  ['dragleave', 'dragend'].forEach((evt) => {
    dropZone.addEventListener(evt, (e) => {
      e.preventDefault();
      e.stopPropagation();
      dropZone.classList.remove('dragover');
    });
  });

  dropZone.addEventListener('drop', (e) => {
    e.preventDefault();
    e.stopPropagation();
    dropZone.classList.remove('dragover');

    const files = e.dataTransfer && e.dataTransfer.files;
    if (!files || files.length === 0) return;
    setSelectedFile(files[0]);
  });

  form.addEventListener('submit', async e => {
    e.preventDefault();

    if (!selectedFile) {
      result.innerText = '请先选择或拖入一张图片';
      return;
    }

    const data = new FormData();
    data.append('file', selectedFile);
    data.append('category', categorySelect.value);
    data.append('tags', (tagsInput.value || '').trim());

    try {
      const res = await fetch('/api/upload', { method:'POST', body: data, credentials: 'same-origin' });
      const json = await readApiResponse(res);
      result.innerText = JSON.stringify(json, null, 2);
      if (json.status === 'ok') {
        refreshBackground();
        setSelectedFile(null);
      }
    } catch(err) {
      const msg = err && err.message ? err.message : String(err);
      result.innerText = '上传失败: ' + msg;
      if (msg.includes('UNAUTHORIZED') || msg.includes('请先登录')) {
        setTimeout(() => location.href = '/public/login.html', 500);
      }
    }
  });

  (async () => {
    const ok = await ensureLoggedIn();
    if (!ok) return;
    refreshBackground();
  })();

const form = document.getElementById('loginForm');
    const tokenInput = document.getElementById('tokenInput');
    const result = document.getElementById('result');

    async function checkLoggedIn() {
      try {
        const res = await fetch('/api/auth/status', { credentials: 'same-origin' });
        if (!res.ok) return;
        const data = await res.json();
        if (data.loggedIn) {
          location.href = '/public/upload.html';
        }
      } catch (_) {}
    }

    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      result.innerText = '登录中...';
      try {
        const res = await fetch('/api/login', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          credentials: 'same-origin',
          body: JSON.stringify({ token: tokenInput.value.trim() })
        });

        const text = await res.text();
        if (!res.ok) {
          result.innerText = `登录失败（${res.status}）: ${text || 'Token 无效'}`;
          return;
        }

        result.innerText = '登录成功，正在跳转上传页...';
        setTimeout(() => {
          location.href = '/public/upload.html';
        }, 300);
      } catch (err) {
        result.innerText = '登录失败: ' + err;
      }
    });

    checkLoggedIn();

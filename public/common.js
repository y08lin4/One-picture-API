(function () {
  function detectApiPath() {
    const isMobile = /Android|webOS|iPhone|iPad|iPod|BlackBerry/i.test(navigator.userAgent);
    return isMobile ? '/api/m' : '/api/web';
  }

  function refreshBackground(target = document.body, apiPath = detectApiPath()) {
    target.style.backgroundImage = `url(${apiPath}?t=${Date.now()})`;
  }

  window.AppCommon = {
    detectApiPath,
    refreshBackground,
  };
})();

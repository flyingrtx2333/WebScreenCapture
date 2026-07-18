(() => {
  'use strict';

  const el = {
    workspace: document.getElementById('workspace'),
    video: document.getElementById('remoteVideo'),
    emptyState: document.getElementById('emptyState'),
    emptyTitle: document.getElementById('emptyTitle'),
    emptyMessage: document.getElementById('emptyMessage'),
    badge: document.getElementById('connectionBadge'),
    metrics: document.getElementById('metricsBand'),
    metricsToggle: document.getElementById('metricsToggleButton'),
    metricsToggleLabel: document.getElementById('metricsToggleLabel'),
    resolution: document.getElementById('resolutionMetric'),
    fps: document.getElementById('fpsMetric'),
    bitrate: document.getElementById('bitrateMetric'),
    rtt: document.getElementById('rttMetric'),
    loss: document.getElementById('lossMetric'),
    route: document.getElementById('routeMetric'),
    codec: document.getElementById('codecMetric'),
    loginLayer: document.getElementById('loginLayer'),
    accountLoginForm: document.getElementById('accountLoginForm'),
    accountLoginButton: document.getElementById('accountLoginButton'),
    accountUsername: document.getElementById('accountUsername'),
    accountPassword: document.getElementById('accountPassword'),
    accountLoginError: document.getElementById('accountLoginError'),
    pairingPanel: document.getElementById('pairingPanel'),
    accountLogout: document.getElementById('accountLogoutButton'),
    loginForm: document.getElementById('loginForm'),
    loginButton: document.getElementById('loginButton'),
    tokenInput: document.getElementById('tokenInput'),
    loginError: document.getElementById('loginError'),
    copyToken: document.getElementById('copyTokenButton'),
    fullscreen: document.getElementById('fullscreenButton'),
    logout: document.getElementById('logoutButton'),
  };

  let ws = null;
  let pc = null;
  let iceServers = [];
  let iceExpiresAt = 0;
  let iceRefreshTimer = null;
  let iceRefreshPromise = null;
  let sessionId = '';
  let pendingCandidates = [];
  let statsTimer = null;
  let lastInbound = null;
  let reconnectAttempt = 0;
  let wsOpenedAt = 0;
  let mediaRecoveryTimer = null;
  let intentionalClose = false;
  let currentToken = sessionStorage.getItem('pairingToken') || '';
  let metricsCollapsed = readMetricsCollapsed();

  function readMetricsCollapsed() {
    try { return localStorage.getItem('viewerMetricsCollapsed') === '1'; } catch (_) { return false; }
  }

  function applyMetricsState() {
    const label = metricsCollapsed ? '展开指标' : '收起指标';
    el.metrics.classList.toggle('is-collapsed', metricsCollapsed);
    el.metricsToggle.setAttribute('aria-expanded', String(!metricsCollapsed));
    el.metricsToggle.title = label;
    el.metricsToggleLabel.textContent = label;
  }

  function updateCopyTokenButton() {
    el.copyToken.hidden = !currentToken;
    el.copyToken.textContent = '复制 Token';
  }

  function setBadge(label, state = 'idle') {
    el.badge.className = `connection-badge is-${state}`;
    el.badge.querySelector('span').textContent = label;
  }

  function setEmpty(title, message) {
    el.emptyTitle.textContent = title;
    el.emptyMessage.textContent = message;
    el.workspace.classList.remove('has-video');
    el.metrics.hidden = true;
  }

  async function request(path, options = {}) {
    const response = await fetch(path, {
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', ...(options.headers || {}) },
      ...options,
    });
    if (!response.ok) {
      let message = `请求失败 (${response.status})`;
      try { message = (await response.json()).error || message; } catch (_) {}
      const error = new Error(message);
      error.status = response.status;
      throw error;
    }
    return response.status === 204 ? null : response.json();
  }

  async function authenticateAccount(username, password) {
    await request('/api/account/session', { method: 'POST', body: JSON.stringify({ username, password }) });
  }

  async function pairViewer(token) {
    await request('/api/viewer/session', { method: 'POST', body: JSON.stringify({ token }) });
  }

  function showAccountLogin(message = '') {
    el.loginLayer.classList.remove('is-hidden');
    el.accountLoginForm.hidden = false;
    el.pairingPanel.hidden = true;
    el.accountLoginError.textContent = message;
    el.accountPassword.value = '';
    el.accountUsername.focus();
  }

  function showPairing() {
    el.loginLayer.classList.remove('is-hidden');
    el.accountLoginForm.hidden = true;
    el.pairingPanel.hidden = false;
    el.loginError.textContent = '';
    el.tokenInput.focus();
  }

  async function bootViewer() {
    intentionalClose = false;
    await refreshIceConfiguration(false);
    el.loginLayer.classList.add('is-hidden');
    connectWebSocket();
  }

  async function refreshIceConfiguration(restartAfterRefresh) {
    if (iceRefreshPromise) return iceRefreshPromise;
    iceRefreshPromise = (async () => {
      const config = await request('/api/ice');
      iceServers = config.iceServers || [];
      iceExpiresAt = Number(config.expiresAt || 0);
      if (pc) {
        const current = pc.getConfiguration();
        pc.setConfiguration({ ...current, iceServers });
      }
      scheduleIceRefresh();
      if (restartAfterRefresh && pc && sessionId) {
        console.info('TURN credentials refreshed; requesting ICE restart');
        sendSignal('ice.restart', { reason: 'turn-credentials-refreshed' });
      }
    })().finally(() => { iceRefreshPromise = null; });
    return iceRefreshPromise;
  }

  function scheduleIceRefresh() {
    if (iceRefreshTimer) window.clearTimeout(iceRefreshTimer);
    if (!iceExpiresAt || intentionalClose) return;
    const delay = Math.max(30_000, iceExpiresAt * 1000 - Date.now() - 75_000);
    iceRefreshTimer = window.setTimeout(() => {
      refreshIceConfiguration(true).catch(error => {
        console.warn('TURN credential refresh failed', error);
        iceRefreshTimer = window.setTimeout(() => {
          refreshIceConfiguration(true).catch(retryError => console.warn('TURN credential refresh retry failed', retryError));
        }, 30_000);
      });
    }, delay);
  }

  function connectWebSocket() {
    if (intentionalClose) return;
    const scheme = location.protocol === 'https:' ? 'wss:' : 'ws:';
    ws = new WebSocket(`${scheme}//${location.host}/ws`);
    setBadge('正在连接', 'idle');

    ws.addEventListener('open', () => {
      wsOpenedAt = Date.now();
      reconnectAttempt = 0;
      setBadge('等待捕获端', 'idle');
      setEmpty('等待被捕获端上线', '捕获端开始共享后，画面会自动出现。');
    });

    ws.addEventListener('message', event => {
      let message;
      try { message = JSON.parse(event.data); } catch (_) { return; }
      handleSignal(message).catch(error => showConnectionError(error.message));
    });

    ws.addEventListener('close', event => {
      console.info('Signaling closed', { code: event.code, reason: event.reason, durationMs: wsOpenedAt ? Date.now() - wsOpenedAt : 0 });
      wsOpenedAt = 0;
      closePeer();
      if (intentionalClose) return;
      if (event.code === 1008) {
        setBadge('已被接管', 'error');
        setEmpty('此窗口已被接管', '新的观看窗口已连接。刷新页面可重新接管。');
        return;
      }
      setBadge('连接中断', 'error');
      setEmpty('连接已中断', '正在尝试重新连接信令服务。');
      const delays = [1000, 2000, 5000, 10000, 30000];
      const delay = delays[Math.min(reconnectAttempt++, delays.length - 1)];
      window.setTimeout(connectWebSocket, delay);
    });
  }

  async function handleSignal(message) {
    switch (message.type) {
      case 'hello':
        sessionId = message.sessionId || '';
        break;
      case 'peer.start':
        sessionId = message.sessionId;
        closePeer();
        setBadge('捕获端在线', 'live');
        setEmpty('捕获端已上线', '正在启动原生整桌面捕获，画面会自动出现。');
        break;
      case 'sdp.offer':
        if (message.sessionId !== sessionId) return;
        await acceptOffer(message.payload);
        break;
      case 'ice.candidate':
        if (message.sessionId !== sessionId || !message.payload) return;
        if (pc && pc.remoteDescription) await addRemoteCandidate(message.payload);
        else pendingCandidates.push(normalizeIceCandidate(message.payload));
        break;
      case 'peer.stop':
        if (message.sessionId !== sessionId) return;
        closePeer();
        setBadge('等待捕获端', 'idle');
        setEmpty('画面暂时停止', '捕获端仍在线时可随时重新开始。');
        break;
      case 'status':
        if (message.sessionId !== sessionId) return;
        if (message.payload?.captureActive === false) {
          setBadge('捕获端在线', 'live');
          setEmpty('捕获端已上线', '在 Windows 捕获端点击“开始捕获整个桌面”。');
        }
        break;
    }
  }

  async function acceptOffer(offer) {
    if (!pc || pc.connectionState === 'closed') createPeerConnection();
    let peer = pc;
    try {
      await applyOffer(peer, offer);
    } catch (error) {
      console.warn('Recreating peer after offer failed', error);
      closePeer();
      createPeerConnection();
      peer = pc;
      await applyOffer(peer, offer);
    }
  }

  function createPeerConnection() {
    const peer = new RTCPeerConnection({ iceServers, bundlePolicy: 'max-bundle', rtcpMuxPolicy: 'require' });
    pc = peer;
    peer.addEventListener('icecandidate', event => {
      if (pc !== peer) return;
      if (event.candidate) sendSignal('ice.candidate', event.candidate.toJSON());
    });
    peer.addEventListener('track', event => {
      if (pc !== peer) return;
      el.video.srcObject = event.streams[0] || new MediaStream([event.track]);
      el.workspace.classList.add('has-video');
      el.metrics.hidden = false;
      setBadge('实时画面', 'live');
      startStats();
    });
    peer.addEventListener('connectionstatechange', () => {
      if (pc !== peer) return;
      if (peer.connectionState === 'connected') {
        cancelMediaRecovery();
        setBadge('实时画面', 'live');
      }
      if (peer.connectionState === 'failed' || peer.connectionState === 'disconnected') {
        setBadge('链路波动', 'error');
        scheduleMediaRecovery(peer.connectionState);
      }
    });
  }

  async function applyOffer(peer, offer) {
    await peer.setRemoteDescription(offer);
    for (const candidate of pendingCandidates.splice(0)) await addRemoteCandidate(candidate);
    const answer = await peer.createAnswer();
    await peer.setLocalDescription(answer);
    sendSignal('sdp.answer', peer.localDescription.toJSON());
  }

  function scheduleMediaRecovery(reason) {
    if (mediaRecoveryTimer) return;
    mediaRecoveryTimer = window.setTimeout(() => {
      mediaRecoveryTimer = null;
      if (!pc || !['failed', 'disconnected'].includes(pc.connectionState)) return;
      console.warn('Media path unhealthy for 4 seconds; requesting recovery', reason);
      setBadge('链路恢复中', 'error');
      refreshIceConfiguration(false)
        .catch(error => console.warn('ICE refresh during recovery failed', error))
        .finally(() => {
          sendSignal('ice.restart', { reason: `media-${reason}` });
        });
    }, 4000);
  }

  function cancelMediaRecovery() {
    if (mediaRecoveryTimer) window.clearTimeout(mediaRecoveryTimer);
    mediaRecoveryTimer = null;
  }

  function sendSignal(type, payload) {
    if (!ws || ws.readyState !== WebSocket.OPEN || !sessionId) return;
    ws.send(JSON.stringify({ type, sessionId, payload }));
  }

  function normalizeIceCandidate(payload) {
    if (!payload || typeof payload.candidate !== 'string') return payload;
    let candidate = payload.candidate.trim();
    if (candidate.toLowerCase().startsWith('a=')) candidate = candidate.slice(2);
    if (candidate && !candidate.toLowerCase().startsWith('candidate:')) candidate = `candidate:${candidate}`;
    return { ...payload, candidate };
  }

  async function addRemoteCandidate(payload) {
    const candidate = normalizeIceCandidate(payload);
    if (!candidate?.candidate || !pc) return;
    try {
      await pc.addIceCandidate(candidate);
    } catch (error) {
      console.warn('Ignored invalid remote ICE candidate', candidate.candidate, error);
    }
  }

  function closePeer() {
    cancelMediaRecovery();
    if (statsTimer) window.clearInterval(statsTimer);
    statsTimer = null;
    lastInbound = null;
    pendingCandidates = [];
    if (pc) pc.close();
    pc = null;
    if (el.video.srcObject) {
      for (const track of el.video.srcObject.getTracks()) track.stop();
      el.video.srcObject = null;
    }
  }

  function startStats() {
    if (statsTimer) window.clearInterval(statsTimer);
    statsTimer = window.setInterval(updateStats, 1000);
  }

  async function updateStats() {
    if (!pc) return;
    const report = await pc.getStats();
    let inbound = null;
    let pair = null;
    let codec = null;
    for (const stat of report.values()) {
      if (stat.type === 'inbound-rtp' && stat.kind === 'video' && !stat.isRemote) inbound = stat;
      if (stat.type === 'transport' && stat.selectedCandidatePairId) pair = report.get(stat.selectedCandidatePairId);
    }
    if (!pair) {
      for (const stat of report.values()) if (stat.type === 'candidate-pair' && stat.state === 'succeeded' && stat.nominated) pair = stat;
    }
    if (!inbound) return;
    if (inbound.codecId) codec = report.get(inbound.codecId);

    const now = performance.now();
    let bitrate = 0;
    let loss = 0;
    let fps = inbound.framesPerSecond || 0;
    if (lastInbound) {
      const seconds = Math.max((now - lastInbound.at) / 1000, .1);
      bitrate = Math.max(0, (inbound.bytesReceived - lastInbound.bytes) * 8 / seconds);
      const receivedDelta = Math.max(0, inbound.packetsReceived - lastInbound.received);
      const lostDelta = Math.max(0, inbound.packetsLost - lastInbound.lost);
      loss = receivedDelta + lostDelta > 0 ? lostDelta / (receivedDelta + lostDelta) : 0;
      if (!fps) fps = Math.max(0, (inbound.framesDecoded - lastInbound.frames) / seconds);
    }
    lastInbound = { at: now, bytes: inbound.bytesReceived, received: inbound.packetsReceived, lost: inbound.packetsLost, frames: inbound.framesDecoded };

    let route = '直连';
    if (pair) {
      const local = report.get(pair.localCandidateId);
      const remote = report.get(pair.remoteCandidateId);
      if (local?.candidateType === 'relay' || remote?.candidateType === 'relay') route = 'TURN 中继';
    }
    el.resolution.textContent = inbound.frameWidth && inbound.frameHeight ? `${inbound.frameWidth}×${inbound.frameHeight}` : '—';
    el.fps.textContent = fps ? `${Math.round(fps)} fps` : '—';
    el.bitrate.textContent = bitrate ? formatBitrate(bitrate) : '—';
    el.rtt.textContent = pair?.currentRoundTripTime != null ? `${Math.round(pair.currentRoundTripTime * 1000)} ms` : '—';
    el.loss.textContent = `${(loss * 100).toFixed(loss < .01 ? 2 : 1)}%`;
    el.route.textContent = route;
    el.codec.textContent = codec?.mimeType ? codec.mimeType.replace('video/', '') : '—';
  }

  function formatBitrate(value) {
    return value >= 1_000_000 ? `${(value / 1_000_000).toFixed(1)} Mbps` : `${Math.round(value / 1000)} Kbps`;
  }

  function showConnectionError(message) {
    setBadge('连接异常', 'error');
    setEmpty('画面连接异常', message);
  }

  el.accountLoginForm.addEventListener('submit', async event => {
    event.preventDefault();
    el.accountLoginError.textContent = '';
    el.accountLoginButton.disabled = true;
    try {
      await authenticateAccount(el.accountUsername.value.trim(), el.accountPassword.value);
      el.accountPassword.value = '';
      showPairing();
    } catch (error) {
      const messages = {
        'invalid username or password': '账号或密码错误。',
        'username and password required': '请输入账号和密码。',
        'too many attempts': '登录尝试过于频繁，请稍后再试。',
        'account service unavailable': '统一账号服务暂时不可用。',
      };
      el.accountLoginError.textContent = messages[error.message] || error.message;
    } finally {
      el.accountLoginButton.disabled = false;
    }
  });

  el.loginForm.addEventListener('submit', async event => {
    event.preventDefault();
    el.loginError.textContent = '';
    el.loginButton.disabled = true;
    try {
      const token = el.tokenInput.value.trim();
      await pairViewer(token);
      currentToken = token;
      sessionStorage.setItem('pairingToken', token);
      updateCopyTokenButton();
      el.tokenInput.value = '';
      await bootViewer();
    } catch (error) {
      if (error.status === 401) {
        showAccountLogin('登录已过期，请重新登录。');
      } else {
        el.loginError.textContent = error.message === 'pairing token required' ? '请输入配对 Token。' : error.message;
      }
    } finally {
      el.loginButton.disabled = false;
    }
  });

  el.copyToken.addEventListener('click', async () => {
    if (!currentToken) return;
    try {
      await navigator.clipboard.writeText(currentToken);
      el.copyToken.textContent = '已复制';
      window.setTimeout(updateCopyTokenButton, 1600);
    } catch (_) {
      el.copyToken.textContent = '复制失败';
      window.setTimeout(updateCopyTokenButton, 1600);
    }
  });

  el.fullscreen.addEventListener('click', () => {
    if (!document.fullscreenElement) el.workspace.requestFullscreen?.();
    else document.exitFullscreen?.();
  });

  el.metricsToggle.addEventListener('click', () => {
    metricsCollapsed = !metricsCollapsed;
    try { localStorage.setItem('viewerMetricsCollapsed', metricsCollapsed ? '1' : '0'); } catch (_) {}
    applyMetricsState();
  });

  async function logoutAccount() {
    intentionalClose = true;
    if (iceRefreshTimer) window.clearTimeout(iceRefreshTimer);
    iceRefreshTimer = null;
    closePeer();
    if (ws) ws.close(1000, 'logout');
    try { await request('/api/session', { method: 'DELETE' }); } catch (_) {}
    currentToken = '';
    sessionStorage.removeItem('pairingToken');
    updateCopyTokenButton();
    setBadge('等待连接', 'idle');
    setEmpty('等待被捕获端上线', '登录后查看实时桌面。');
    showAccountLogin();
  }

  el.accountLogout.addEventListener('click', logoutAccount);
  el.logout.addEventListener('click', logoutAccount);

  applyMetricsState();
  updateCopyTokenButton();
  request('/api/session')
    .then(session => {
      if (!session.accountAuthenticated) showAccountLogin();
      else if (session.paired && currentToken) bootViewer();
      else showPairing();
    })
    .catch(() => showAccountLogin());
})();

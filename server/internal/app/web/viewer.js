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
    resolution: document.getElementById('resolutionMetric'),
    fps: document.getElementById('fpsMetric'),
    bitrate: document.getElementById('bitrateMetric'),
    rtt: document.getElementById('rttMetric'),
    loss: document.getElementById('lossMetric'),
    route: document.getElementById('routeMetric'),
    codec: document.getElementById('codecMetric'),
    loginLayer: document.getElementById('loginLayer'),
    loginForm: document.getElementById('loginForm'),
    loginButton: document.getElementById('loginButton'),
    tokenInput: document.getElementById('tokenInput'),
    loginError: document.getElementById('loginError'),
    fullscreen: document.getElementById('fullscreenButton'),
    logout: document.getElementById('logoutButton'),
  };

  let ws = null;
  let pc = null;
  let iceServers = [];
  let sessionId = '';
  let pendingCandidates = [];
  let statsTimer = null;
  let lastInbound = null;
  let reconnectAttempt = 0;
  let intentionalClose = false;

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
      throw new Error(message);
    }
    return response.status === 204 ? null : response.json();
  }

  async function authenticate(token) {
    await request('/api/viewer/session', { method: 'POST', body: JSON.stringify({ token }) });
  }

  async function bootViewer() {
    const config = await request('/api/ice');
    iceServers = config.iceServers;
    el.loginLayer.classList.add('is-hidden');
    connectWebSocket();
  }

  function connectWebSocket() {
    if (intentionalClose) return;
    const scheme = location.protocol === 'https:' ? 'wss:' : 'ws:';
    ws = new WebSocket(`${scheme}//${location.host}/ws`);
    setBadge('正在连接', 'idle');

    ws.addEventListener('open', () => {
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
        setEmpty('捕获端已上线', '等待捕获端选择屏幕，画面会自动出现。');
        break;
      case 'sdp.offer':
        if (message.sessionId !== sessionId) return;
        await acceptOffer(message.payload);
        break;
      case 'ice.candidate':
        if (message.sessionId !== sessionId || !message.payload) return;
        if (pc && pc.remoteDescription) await pc.addIceCandidate(message.payload);
        else pendingCandidates.push(message.payload);
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
          setEmpty('捕获端已上线', '等待捕获端选择屏幕，画面会自动出现。');
        }
        break;
    }
  }

  async function acceptOffer(offer) {
    closePeer();
    pc = new RTCPeerConnection({ iceServers, bundlePolicy: 'max-bundle', rtcpMuxPolicy: 'require' });
    pc.addEventListener('icecandidate', event => {
      if (event.candidate) sendSignal('ice.candidate', event.candidate.toJSON());
    });
    pc.addEventListener('track', event => {
      el.video.srcObject = event.streams[0] || new MediaStream([event.track]);
      el.workspace.classList.add('has-video');
      el.metrics.hidden = false;
      setBadge('实时画面', 'live');
      startStats();
    });
    pc.addEventListener('connectionstatechange', () => {
      if (!pc) return;
      if (pc.connectionState === 'connected') setBadge('实时画面', 'live');
      if (pc.connectionState === 'failed') showConnectionError('媒体链路建立失败，等待重连。');
      if (pc.connectionState === 'disconnected') setBadge('链路波动', 'error');
    });
    await pc.setRemoteDescription(offer);
    for (const candidate of pendingCandidates.splice(0)) await pc.addIceCandidate(candidate);
    const answer = await pc.createAnswer();
    await pc.setLocalDescription(answer);
    sendSignal('sdp.answer', pc.localDescription.toJSON());
  }

  function sendSignal(type, payload) {
    if (!ws || ws.readyState !== WebSocket.OPEN || !sessionId) return;
    ws.send(JSON.stringify({ type, sessionId, payload }));
  }

  function closePeer() {
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

  el.loginForm.addEventListener('submit', async event => {
    event.preventDefault();
    el.loginError.textContent = '';
    el.loginButton.disabled = true;
    try {
      await authenticate(el.tokenInput.value.trim());
      el.tokenInput.value = '';
      await bootViewer();
    } catch (error) {
      el.loginError.textContent = error.message === 'pairing token required' ? '请输入配对 Token。' : error.message;
    } finally {
      el.loginButton.disabled = false;
    }
  });

  el.fullscreen.addEventListener('click', () => {
    if (!document.fullscreenElement) el.workspace.requestFullscreen?.();
    else document.exitFullscreen?.();
  });

  el.logout.addEventListener('click', async () => {
    intentionalClose = true;
    closePeer();
    if (ws) ws.close(1000, 'logout');
    try { await request('/api/session', { method: 'DELETE' }); } catch (_) {}
    el.loginLayer.classList.remove('is-hidden');
    setEmpty('等待被捕获端上线', '登录后查看实时桌面。');
    el.tokenInput.focus();
  });

  request('/api/session')
    .then(session => session.authenticated ? bootViewer() : el.tokenInput.focus())
    .catch(() => el.tokenInput.focus());
})();

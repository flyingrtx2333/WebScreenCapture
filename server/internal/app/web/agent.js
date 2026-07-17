(() => {
  'use strict';

  const profiles = [
    { name: '1080p60', width: 1920, height: 1080, fps: 60, maxBitrate: 12_000_000, minBandwidth: 7_000_000 },
    { name: '900p45', width: 1600, height: 900, fps: 45, maxBitrate: 7_000_000, minBandwidth: 4_000_000 },
    { name: '720p30', width: 1280, height: 720, fps: 30, maxBitrate: 3_500_000, minBandwidth: 0 },
  ];

  const el = {
    badge: document.getElementById('agentConnectionBadge'),
    title: document.getElementById('agentTitle'),
    description: document.getElementById('agentDescription'),
    start: document.getElementById('startButton'),
    stop: document.getElementById('stopButton'),
    progress: document.getElementById('captureProgress'),
    status: document.getElementById('captureStatus'),
    profile: document.getElementById('profileMetric'),
    resolution: document.getElementById('agentResolutionMetric'),
    bitrate: document.getElementById('agentBitrateMetric'),
    rtt: document.getElementById('agentRttMetric'),
    route: document.getElementById('agentRouteMetric'),
    codec: document.getElementById('agentCodecMetric'),
    error: document.getElementById('agentError'),
  };

  let ws = null;
  let pc = null;
  let stream = null;
  let videoTrack = null;
  let sender = null;
  let iceServers = [];
  let sessionId = '';
  let pendingCandidates = [];
  let reconnectAttempt = 0;
  let reauthTimer = null;
  let statsTimer = null;
  let peerRecoveryTimer = null;
  let lastOutbound = null;
  let currentProfile = 0;
  const adaptive = new window.AdaptiveTierController(profiles);
  let connectedAt = 0;
  let authenticated = false;
  let bootstrapping = false;

  function postHost(message) {
    try { window.chrome?.webview?.postMessage(message); } catch (_) {}
  }

  function setBadge(label, state = 'idle') {
    el.badge.className = `connection-badge is-${state}`;
    el.badge.querySelector('span').textContent = label;
  }

  function setStatus(label) {
    el.status.textContent = label;
    postHost({ type: 'status', label });
  }

  function showError(message) {
    el.error.textContent = message;
    el.error.hidden = false;
    window.setTimeout(() => { el.error.hidden = true; }, 6000);
    postHost({ type: 'error', message });
  }

  async function request(path, options = {}) {
    const response = await fetch(path, { credentials: 'same-origin', ...options });
    if (!response.ok) {
      let message = `请求失败 (${response.status})`;
      try { message = (await response.json()).error || message; } catch (_) {}
      throw new Error(message);
    }
    return response.status === 204 ? null : response.json();
  }

  async function bootstrap(token) {
    if (bootstrapping || authenticated) return;
    if (reauthTimer) window.clearTimeout(reauthTimer);
    reauthTimer = null;
    bootstrapping = true;
    try {
      await request('/api/agent/session', { method: 'POST', headers: { Authorization: `Bearer ${token}` } });
      const config = await request('/api/ice');
      iceServers = config.iceServers;
      authenticated = true;
      reconnectAttempt = 0;
      el.start.disabled = false;
      setBadge('信令已连接', 'live');
      setStatus('等待选择屏幕');
      connectWebSocket();
      postHost({ type: 'authenticated' });
    } catch (error) {
      authenticated = false;
      if (error.message === 'pairing token required') {
        setBadge('验证失败', 'error');
        setStatus('配对 Token 无效');
        showError('请输入与观看网页相同的配对 Token。');
        postHost({ type: 'auth-error', message: error.message });
      } else {
        setBadge('信令重连中', 'error');
        setStatus(videoTrack ? '画面保留，等待重连' : '信令连接中断');
        scheduleReauthentication();
      }
    } finally {
      bootstrapping = false;
      token = '';
    }
  }

  function connectWebSocket() {
    if (!authenticated) return;
    const scheme = location.protocol === 'https:' ? 'wss:' : 'ws:';
    ws = new WebSocket(`${scheme}//${location.host}/ws`);
    setBadge('正在连接信令', 'idle');

    ws.addEventListener('open', () => {
      reconnectAttempt = 0;
      setBadge(videoTrack ? '捕获已就绪' : '信令已连接', 'live');
      setStatus(videoTrack ? '等待观看者' : '等待选择屏幕');
    });

    ws.addEventListener('message', event => {
      let message;
      try { message = JSON.parse(event.data); } catch (_) { return; }
      handleSignal(message).catch(error => showError(error.message));
    });

    ws.addEventListener('close', event => {
      closePeer();
      authenticated = false;
      if (event.code === 1008) {
        setBadge('连接已被替换', 'error');
        setStatus('另一捕获端已登录');
        return;
      }
      setBadge('信令重连中', 'error');
      setStatus(videoTrack ? '画面保留，等待重连' : '信令连接中断');
      const delays = [1000, 2000, 5000, 10000, 30000];
      const delay = delays[Math.min(reconnectAttempt++, delays.length - 1)];
      scheduleReauthentication(delay);
    });
  }

  function scheduleReauthentication(delay = 1000) {
    if (reauthTimer) return;
    reauthTimer = window.setTimeout(() => {
      reauthTimer = null;
      postHost({ type: 'reauthenticate' });
    }, delay);
  }

  async function handleSignal(message) {
    switch (message.type) {
      case 'hello':
        sessionId = message.sessionId || '';
        if (sessionId && videoTrack) await createOffer();
        else if (sessionId) sendSignal('status', { captureActive: false });
        break;
      case 'peer.start':
        sessionId = message.sessionId;
        if (videoTrack) await createOffer();
        else {
          setStatus('观看者在线，等待开始捕获');
          sendSignal('status', { captureActive: false });
        }
        break;
      case 'sdp.answer':
        if (message.sessionId !== sessionId || !pc) return;
        await pc.setRemoteDescription(message.payload);
        for (const candidate of pendingCandidates.splice(0)) await pc.addIceCandidate(candidate);
        break;
      case 'ice.candidate':
        if (message.sessionId !== sessionId || !message.payload) return;
        if (pc && pc.remoteDescription) await pc.addIceCandidate(message.payload);
        else pendingCandidates.push(message.payload);
        break;
      case 'peer.stop':
        if (message.sessionId !== sessionId) return;
        closePeer();
        sessionId = '';
        setStatus(videoTrack ? '等待观看者' : '等待选择屏幕');
        break;
    }
  }

  async function startCapture() {
    try {
      stream = await navigator.mediaDevices.getDisplayMedia({
        video: {
          width: { ideal: 1920, max: 1920 },
          height: { ideal: 1080, max: 1080 },
          frameRate: { ideal: 60, max: 60 },
        },
        audio: false,
      });
      videoTrack = stream.getVideoTracks()[0];
      if (!videoTrack) throw new Error('系统没有返回可捕获的视频轨道');
      videoTrack.contentHint = 'motion';
      videoTrack.addEventListener('ended', () => stopCapture(false), { once: true });
      currentProfile = 0;
      adaptive.setIndex(0);
      await applyProfile(0);
      el.start.hidden = true;
      el.stop.hidden = false;
      el.progress.style.width = '100%';
      el.title.textContent = '屏幕捕获已启动';
      el.description.textContent = '保持此程序运行；断线恢复不需要重新选屏。';
      setStatus(sessionId ? '正在建立观看链路' : '等待观看者');
      setBadge('捕获已就绪', 'live');
      if (sessionId) await createOffer();
    } catch (error) {
      if (error.name !== 'NotAllowedError') showError(error.message);
      setStatus('未选择屏幕');
    }
  }

  function stopCapture(notify = true) {
    if (notify && sessionId) sendSignal('peer.stop', { reason: 'capture stopped' });
    closePeer();
    if (videoTrack) videoTrack.stop();
    videoTrack = null;
    stream = null;
    el.start.hidden = false;
    el.stop.hidden = true;
    el.progress.style.width = '0';
    el.title.textContent = '选择要共享的屏幕';
    el.description.textContent = '只传输你在系统窗口中明确选择的画面。';
    setStatus('等待选择屏幕');
    resetMetrics();
  }

  async function createOffer() {
    if (!videoTrack || !sessionId || !ws || ws.readyState !== WebSocket.OPEN) return;
    closePeer();
    pc = new RTCPeerConnection({ iceServers, bundlePolicy: 'max-bundle', rtcpMuxPolicy: 'require' });
    const transceiver = pc.addTransceiver(videoTrack, {
      direction: 'sendonly',
      streams: [stream],
      sendEncodings: [{ maxBitrate: profiles[currentProfile].maxBitrate, maxFramerate: profiles[currentProfile].fps, priority: 'high', networkPriority: 'high' }],
    });
    sender = transceiver.sender;
    preferScreenCodecs(transceiver);
    pendingCandidates = [];
    pc.addEventListener('icecandidate', event => {
      if (event.candidate) sendSignal('ice.candidate', event.candidate.toJSON());
    });
    pc.addEventListener('connectionstatechange', () => {
      if (!pc) return;
      if (pc.connectionState === 'connected') {
        connectedAt = performance.now();
        setBadge('实时传输中', 'live');
        setStatus('观看者已连接');
        startStats();
      } else if (pc.connectionState === 'disconnected') {
        setBadge('链路波动', 'error');
        setStatus('媒体链路正在恢复');
        schedulePeerRecovery(pc);
      } else if (pc.connectionState === 'failed') {
        setBadge('链路失败', 'error');
        setStatus('正在重新协商');
        schedulePeerRecovery(pc, 0);
      }
    });
    const offer = await pc.createOffer();
    await pc.setLocalDescription(offer);
    sendSignal('sdp.offer', pc.localDescription.toJSON());
    setStatus('等待观看者响应');
  }

  function preferScreenCodecs(transceiver) {
    const capabilities = RTCRtpSender.getCapabilities?.('video');
    if (!capabilities?.codecs || !transceiver.setCodecPreferences) return;
    const weight = codec => {
      if (codec.mimeType === 'video/H264' && /profile-level-id=42/i.test(codec.sdpFmtpLine || '')) return 0;
      if (codec.mimeType === 'video/H264') return 1;
      if (codec.mimeType === 'video/VP8') return 2;
      if (codec.mimeType === 'video/rtx') return 4;
      return 3;
    };
    transceiver.setCodecPreferences([...capabilities.codecs].sort((a, b) => weight(a) - weight(b)));
  }

  function sendSignal(type, payload) {
    if (!ws || ws.readyState !== WebSocket.OPEN || !sessionId) return;
    ws.send(JSON.stringify({ type, sessionId, payload }));
  }

  function closePeer() {
    if (statsTimer) window.clearInterval(statsTimer);
    if (peerRecoveryTimer) window.clearTimeout(peerRecoveryTimer);
    statsTimer = null;
    peerRecoveryTimer = null;
    lastOutbound = null;
    pendingCandidates = [];
    sender = null;
    if (pc) pc.close();
    pc = null;
  }

  function schedulePeerRecovery(activePeer, delay = 5000) {
    if (peerRecoveryTimer) window.clearTimeout(peerRecoveryTimer);
    peerRecoveryTimer = window.setTimeout(() => {
      peerRecoveryTimer = null;
      if (pc !== activePeer || !sessionId || !videoTrack) return;
      if (activePeer.connectionState === 'connected') return;
      createOffer().catch(error => showError(error.message));
    }, delay);
  }

  function startStats() {
    if (statsTimer) window.clearInterval(statsTimer);
    statsTimer = window.setInterval(updateStats, 1000);
  }

  async function updateStats() {
    if (!pc) return;
    const report = await pc.getStats();
    let outbound = null;
    let remoteInbound = null;
    let pair = null;
    let codec = null;
    for (const stat of report.values()) {
      if (stat.type === 'outbound-rtp' && stat.kind === 'video' && !stat.isRemote) outbound = stat;
      if (stat.type === 'remote-inbound-rtp' && stat.kind === 'video') remoteInbound = stat;
      if (stat.type === 'transport' && stat.selectedCandidatePairId) pair = report.get(stat.selectedCandidatePairId);
    }
    if (!pair) for (const stat of report.values()) if (stat.type === 'candidate-pair' && stat.nominated && stat.state === 'succeeded') pair = stat;
    if (!outbound) return;
    if (outbound.codecId) codec = report.get(outbound.codecId);

    const now = performance.now();
    let bitrate = 0;
    let loss = remoteInbound?.fractionLost || 0;
    if (lastOutbound) {
      const seconds = Math.max((now - lastOutbound.at) / 1000, .1);
      bitrate = Math.max(0, (outbound.bytesSent - lastOutbound.bytes) * 8 / seconds);
      if (remoteInbound && remoteInbound.packetsLost != null && lastOutbound.remoteLost != null) {
        const lostDelta = Math.max(0, remoteInbound.packetsLost - lastOutbound.remoteLost);
        const sentDelta = Math.max(0, outbound.packetsSent - lastOutbound.packetsSent);
        if (sentDelta > 0) loss = Math.min(1, lostDelta / sentDelta);
      }
    }
    lastOutbound = { at: now, bytes: outbound.bytesSent, packetsSent: outbound.packetsSent, remoteLost: remoteInbound?.packetsLost };

    const settings = videoTrack?.getSettings() || {};
    const available = pair?.availableOutgoingBitrate || 0;
    let route = '直连';
    if (pair) {
      const local = report.get(pair.localCandidateId);
      const remote = report.get(pair.remoteCandidateId);
      if (local?.candidateType === 'relay' || remote?.candidateType === 'relay') route = 'TURN 中继';
    }

    el.profile.textContent = profiles[currentProfile].name;
    el.resolution.textContent = settings.width && settings.height ? `${settings.width}×${settings.height} / ${settings.frameRate ? Math.round(settings.frameRate) : profiles[currentProfile].fps}fps` : profiles[currentProfile].name;
    el.bitrate.textContent = bitrate ? formatBitrate(bitrate) : '启动中';
    el.rtt.textContent = remoteInbound?.roundTripTime != null ? `${Math.round(remoteInbound.roundTripTime * 1000)} ms` : pair?.currentRoundTripTime != null ? `${Math.round(pair.currentRoundTripTime * 1000)} ms` : '—';
    el.route.textContent = route;
    el.codec.textContent = codec?.mimeType ? codec.mimeType.replace('video/', '') : '—';

    if (sessionId && ws?.readyState === WebSocket.OPEN) {
      sendSignal('status', { profile: profiles[currentProfile].name, bitrate, loss, route, codec: el.codec.textContent });
    }
    if (now - connectedAt > 10_000) await evaluateProfile(available, loss);
  }

  async function evaluateProfile(available, loss) {
    const nextProfile = adaptive.sample(available, loss);
    if (nextProfile != null) await applyProfile(nextProfile);
  }

  async function applyProfile(index) {
    currentProfile = Math.max(0, Math.min(index, profiles.length - 1));
    if (adaptive.index !== currentProfile) adaptive.setIndex(currentProfile);
    const profile = profiles[currentProfile];
    if (videoTrack) {
      await videoTrack.applyConstraints({
        width: { ideal: profile.width, max: profile.width },
        height: { ideal: profile.height, max: profile.height },
        frameRate: { ideal: profile.fps, max: profile.fps },
      });
    }
    if (sender) {
      const parameters = sender.getParameters();
      parameters.encodings ||= [{}];
      parameters.encodings[0].maxBitrate = profile.maxBitrate;
      parameters.encodings[0].maxFramerate = profile.fps;
      parameters.degradationPreference = 'maintain-framerate';
      await sender.setParameters(parameters);
    }
    el.profile.textContent = profile.name;
  }

  function resetMetrics() {
    for (const target of [el.profile, el.resolution, el.bitrate, el.rtt, el.route, el.codec]) target.textContent = '—';
  }

  function formatBitrate(value) {
    return value >= 1_000_000 ? `${(value / 1_000_000).toFixed(1)} Mbps` : `${Math.round(value / 1000)} Kbps`;
  }

  window.chrome?.webview?.addEventListener('message', event => {
    const message = event.data;
    if (message?.type === 'bootstrap' && typeof message.token === 'string') bootstrap(message.token);
  });

  el.start.addEventListener('click', startCapture);
  el.stop.addEventListener('click', () => stopCapture(true));
  window.addEventListener('beforeunload', () => stopCapture(false));
  postHost({ type: 'ready' });
})();

// Google Sync Lite - Wails v2 Modern Frontend Controller

let currentSyncMode = 'local_master';
let selectedDriveFolderId = 'root';
let selectedDriveFolderName = 'Мой Диск (Корень)';

// Window controls
function appMinimize() {
  if (window.go && window.go.main && window.go.main.App) {
    window.go.main.App.MinimizeWindow();
  }
}

function appHideToTray() {
  if (window.go && window.go.main && window.go.main.App) {
    window.go.main.App.HideToTray();
  }
}

// Navigation
function switchTab(name) {
  document.querySelectorAll('.nav-item').forEach(el => el.classList.remove('active'));
  document.querySelectorAll('.page').forEach(el => el.classList.remove('active'));

  const navEl = document.getElementById('nav-' + name);
  const pageEl = document.getElementById('page-' + name);
  if (navEl) navEl.classList.add('active');
  if (pageEl) pageEl.classList.add('active');
}

// Logging Engine
function formatLogLine(msg) {
  let tagClass = 'log-tag-info';
  if (msg.includes('[+]') || msg.includes('[✓]')) {
    tagClass = 'log-tag-success';
  } else if (msg.includes('[!]') || msg.includes('[✗]') || msg.includes('Ошибка')) {
    tagClass = 'log-tag-error';
  } else if (msg.includes('[*]')) {
    tagClass = 'log-tag-info';
  } else if (msg.includes('[🛡') || msg.includes('Защита')) {
    tagClass = 'log-tag-shield';
  }

  const time = new Date().toLocaleTimeString('ru-RU');
  return `<div class="log-line"><span style="color: #475569;">[${time}]</span> <span class="${tagClass}">${escapeHtml(msg)}</span></div>`;
}

function escapeHtml(str) {
  return str.replace(/&/g, '&amp;')
            .replace(/</g, '&lt;')
            .replace(/>/g, '&gt;');
}

function appendLog(msg) {
  const term = document.getElementById('terminalLog');
  const mini = document.getElementById('dashMiniLog');

  if (term) {
    if (term.textContent.trim() === 'Ожидание событий...') {
      term.innerHTML = '';
    }
    term.innerHTML += formatLogLine(msg);
    term.scrollTop = term.scrollHeight;
  }

  if (mini) {
    if (mini.textContent.trim() === 'Ожидание команд. Нажмите «Синхронизировать» для проверки файлов.') {
      mini.innerHTML = '';
    }
    mini.innerHTML += formatLogLine(msg);
    mini.scrollTop = mini.scrollHeight;
  }
}

function clearLogs() {
  const term = document.getElementById('terminalLog');
  if (term) term.innerHTML = 'Журнал очищен.\n';
}

function copyLogs() {
  const term = document.getElementById('terminalLog');
  if (term) {
    navigator.clipboard.writeText(term.innerText)
      .then(() => appendLog('[*] Журнал скопирован в буфер обмена.'))
      .catch(() => {});
  }
}

// Storage Quota Engine
let lastQuotaFetchTime = 0;

async function loadStorageQuota() {
  if (!window.go || !window.go.main || !window.go.main.App) return;

  const userText = document.getElementById('quotaUserEmail');
  const usageText = document.getElementById('quotaUsageText');
  const percentText = document.getElementById('quotaPercentText');
  const bar = document.getElementById('quotaBar');

  try {
    const info = await window.go.main.App.GetAccountStorageInfo();
    if (!info) return;

    lastQuotaFetchTime = Date.now();

    if (userText) {
      userText.textContent = info.user_name ? `${info.user_name} (${info.user_email})` : (info.user_email || 'Google Drive');
    }

    if (usageText) {
      if (info.is_unlimited) {
        usageText.textContent = `${info.usage_formatted} использовано (Безлимитный аккаунт)`;
      } else {
        usageText.textContent = `${info.usage_formatted} из ${info.limit_formatted} использовано`;
      }
    }

    if (percentText) {
      percentText.textContent = info.is_unlimited ? '' : `${info.usage_percentage.toFixed(1)}%`;
    }

    if (bar) {
      const pct = Math.min(100, Math.max(0, info.usage_percentage));
      bar.style.width = pct + '%';
      bar.classList.remove('warning', 'danger');
      if (pct > 90) {
        bar.classList.add('danger');
      } else if (pct > 75) {
        bar.classList.add('warning');
      }
    }
  } catch (err) {
    if (usageText) usageText.textContent = 'Требуется авторизация в Google Drive';
    if (userText) userText.textContent = 'Не авторизован';
  }
}

// Live Progress Engine
function formatBytesJs(b) {
  if (!b || b <= 0) return '0 Б';
  const units = ['Б', 'КБ', 'МБ', 'ГБ', 'ТБ'];
  const i = Math.floor(Math.log(b) / Math.log(1024));
  return (b / Math.pow(1024, i)).toFixed(1) + ' ' + (units[i] || 'Б');
}

function handleSyncProgress(p) {
  const card = document.getElementById('transferProgressCard');
  if (!card) return;

  if (!p || p.phase === 'done' || p.phase === 'idle') {
    card.style.display = 'none';
    return;
  }

  card.style.display = 'block';

  const fill = document.getElementById('transferBarFill');
  if (fill) fill.style.width = Math.min(100, Math.max(0, p.percent || 0)) + '%';

  const title = document.getElementById('transferTitle');
  if (title) {
    title.textContent = p.phase === 'scanning' ? 'Дельта-сканирование файлов...' : 'Параллельная передача файлов';
  }

  const speed = document.getElementById('transferSpeed');
  if (speed) speed.textContent = p.speed_formatted || '0 Б/с';

  const eta = document.getElementById('transferETA');
  if (eta) eta.textContent = 'Осталось: ' + (p.eta_formatted || '--:--');

  const activeFile = document.getElementById('transferActiveFile');
  if (activeFile) {
    activeFile.textContent = p.current_file ? 'Файл: ' + p.current_file : 'Синхронизация каталогов...';
  }

  const counts = document.getElementById('transferCounts');
  if (counts) {
    counts.textContent = `${p.processed_files} / ${p.total_files} файлов (${(p.percent || 0).toFixed(1)}%)`;
  }

  const workers = document.getElementById('transferWorkersBadge');
  if (workers) {
    workers.textContent = `⚡ Потоков: ${p.active_workers || 4}`;
  }

  const bytesInfo = document.getElementById('transferBytesInfo');
  if (bytesInfo) {
    if (p.total_bytes > 0) {
      bytesInfo.textContent = `${formatBytesJs(p.processed_bytes)} / ${formatBytesJs(p.total_bytes)}`;
    } else {
      bytesInfo.textContent = '';
    }
  }
}

// Status & Data Sync
async function loadStatus() {
  if (!window.go || !window.go.main || !window.go.main.App) return;

  try {
    const data = await window.go.main.App.GetStatus();
    if (!data) return;

    // Auth badge
    const authDot = document.getElementById('authDot');
    const authText = document.getElementById('authStatusText');
    if (data.authenticated) {
      authDot.classList.add('connected');
      authText.textContent = 'Google Drive подключен';
      if (Date.now() - lastQuotaFetchTime > 60000) {
        loadStorageQuota();
      }
    } else {
      authDot.classList.remove('connected');
      authText.textContent = 'Требуется вход Google';
    }

    // Config form values (do not overwrite if the user is currently typing in the field)
    if (data.config) {
      const cfg = data.config;
      const activeEl = document.activeElement;

      const setValIfNotFocused = (id, val) => {
        const el = document.getElementById(id);
        if (el && el !== activeEl) {
          el.value = val;
        }
      };

      setValIfNotFocused('dashLocalFolder', cfg.local_folder || '');
      setValIfNotFocused('dashRemoteFolder', cfg.remote_folder_id || 'root');
      setValIfNotFocused('cfgLocalFolder', cfg.local_folder || '');
      setValIfNotFocused('cfgRemoteFolder', cfg.remote_folder_id || 'root');

      const elAutostart = document.getElementById('cfgAutostart');
      if (elAutostart && elAutostart !== activeEl) {
        elAutostart.checked = !!cfg.autostart;
      }

      const elWatch = document.getElementById('cfgRealtimeWatch');
      if (elWatch && elWatch !== activeEl) {
        elWatch.checked = cfg.realtime_watch !== false;
      }

      setValIfNotFocused('cfgInterval', Math.max(1, Math.floor((cfg.sync_interval_seconds || 60) / 60)));

      const elConc = document.getElementById('cfgConcurrentUploads');
      if (elConc && elConc !== activeEl) {
        elConc.value = String(cfg.concurrent_uploads || 4);
      }

      const elDel = document.getElementById('cfgAllowRemoteDeletion');
      if (elDel && elDel !== activeEl) {
        elDel.checked = !!cfg.allow_remote_deletion;
      }

      const elNotif = document.getElementById('cfgNotificationsEnabled');
      if (elNotif && elNotif !== activeEl) {
        elNotif.checked = cfg.notifications_enabled !== false;
      }

      const elRules = document.getElementById('cfgCustomIgnoreRules');
      if (elRules && elRules !== activeEl) {
        elRules.value = (cfg.custom_ignore_rules || []).join('\n');
      }

      const elBw = document.getElementById('cfgBandwidthLimit');
      if (elBw && elBw !== activeEl) {
        elBw.value = String(cfg.bandwidth_limit_kbps || 0);
      }

      setValIfNotFocused('cfgMaxDeleteThreshold', cfg.max_delete_threshold || 20);

      if (cfg.sync_mode) {
        updateModeUI(cfg.sync_mode);
      }
    }

    // Live Sync Status Banner & Buttons
    const sBanner = document.getElementById('statusBanner');
    const sMain = document.getElementById('statusMainText');
    const sSub = document.getElementById('statusSubText');
    const sTime = document.getElementById('statusTimeText');
    const btnSync = document.getElementById('btnSync');
    const btnSyncIcon = document.getElementById('btnSyncIcon');
    const btnSyncText = document.getElementById('btnSyncText');
    const btnStop = document.getElementById('btnStop');
    const btnPause = document.getElementById('btnPause');
    const btnPauseIcon = document.getElementById('btnPauseIcon');
    const btnPauseText = document.getElementById('btnPauseText');
    const menuResumeItem = document.getElementById('menuResumeItem');

    if (data.is_paused) {
      sBanner.classList.remove('syncing');
      sBanner.classList.add('paused');
      sMain.textContent = 'Синхронизация приостановлена';
      sSub.textContent = data.paused_until ? `Пауза активна до ${data.paused_until} (нажмите «Возобновить»)` : 'Пауза активна вручную (до возобновления)';
      if (btnPause) {
        btnPause.classList.add('is-resuming');
        if (btnPauseIcon) btnPauseIcon.textContent = '▶';
        if (btnPauseText) btnPauseText.textContent = data.paused_until ? `Возобновить (${data.paused_until})` : 'Возобновить';
      }
      if (menuResumeItem) menuResumeItem.style.display = 'flex';
      btnSync.disabled = true;
      btnStop.disabled = true;
    } else {
      sBanner.classList.remove('paused');
      if (btnPause) {
        btnPause.classList.remove('is-resuming');
        if (btnPauseIcon) btnPauseIcon.textContent = '⏸';
        if (btnPauseText) btnPauseText.textContent = 'Пауза';
      }
      if (menuResumeItem) menuResumeItem.style.display = 'none';

      if (data.is_syncing) {
        sBanner.classList.add('syncing');
        sMain.textContent = 'Идет синхронизация файлов...';
        sSub.textContent = 'Проверка контрольных сумм MD5 и передача данных';
        btnSync.disabled = true;
        btnSyncIcon.innerHTML = '<div class="spinner"></div>';
        btnSyncText.textContent = 'Синхронизация...';
        btnStop.disabled = false;
      } else {
        sBanner.classList.remove('syncing');
        sMain.textContent = data.last_sync_msg || 'Готов к работе';
        sSub.textContent = 'Все файлы под надежной защитой Disconnect Guard';
        btnSync.disabled = false;
        btnSyncIcon.innerHTML = '🔄';
        btnSyncText.textContent = 'Синхронизировать';
        btnStop.disabled = true;
        if (data.last_sync_time) {
          sTime.textContent = 'Посл. синхронизация: ' + data.last_sync_time;
        }
        const progressCard = document.getElementById('transferProgressCard');
        if (progressCard) progressCard.style.display = 'none';
      }
    }
  } catch (err) {
    console.error('Failed to load status:', err);
  }
}

// Mode Selector
function setSyncMode(mode) {
  currentSyncMode = mode;
  updateModeUI(mode);
  appendLog(`[*] Выбран режим синхронизации: ${mode === 'local_master' ? 'Локальный диск (Master / Mirror)' : 'Двусторонний (Two-Way)'}`);
  saveSettings();
}

function updateModeUI(mode) {
  currentSyncMode = mode;
  const isMaster = mode === 'local_master';

  // Dashboard
  const dashMaster = document.getElementById('dashModeMaster');
  const dashTwoWay = document.getElementById('dashModeTwoWay');
  const dashRadioMaster = document.getElementById('dashRadioMaster');
  const dashRadioTwoWay = document.getElementById('dashRadioTwoWay');
  if (dashMaster && dashTwoWay && dashRadioMaster && dashRadioTwoWay) {
    dashMaster.classList.toggle('selected', isMaster);
    dashTwoWay.classList.toggle('selected', !isMaster);
    dashRadioMaster.checked = isMaster;
    dashRadioTwoWay.checked = !isMaster;
  }

  // Settings
  const cfgMaster = document.getElementById('cfgModeMaster');
  const cfgTwoWay = document.getElementById('cfgModeTwoWay');
  const cfgRadioMaster = document.getElementById('cfgRadioMaster');
  const cfgRadioTwoWay = document.getElementById('cfgRadioTwoWay');
  if (cfgMaster && cfgTwoWay && cfgRadioMaster && cfgRadioTwoWay) {
    cfgMaster.classList.toggle('selected', isMaster);
    cfgTwoWay.classList.toggle('selected', !isMaster);
    cfgRadioMaster.checked = isMaster;
    cfgRadioTwoWay.checked = !isMaster;
  }
}

// Actions
async function triggerSync() {
  appendLog('[*] Запуск процедуры синхронизации...');
  try {
    await window.go.main.App.TriggerSync();
    loadStatus();
  } catch (err) {
    appendLog(`[!] Ошибка запуска: ${err}`);
  }
}

let isPauseDropdownOpen = false;

function togglePauseDropdown(event) {
  if (event) event.stopPropagation();
  const btnPause = document.getElementById('btnPause');
  if (btnPause && btnPause.classList.contains('is-resuming')) {
    triggerResume();
    return;
  }
  const menu = document.getElementById('pauseDropdownMenu');
  if (menu) {
    isPauseDropdownOpen = !isPauseDropdownOpen;
    menu.classList.toggle('show', isPauseDropdownOpen);
  }
}

function closePauseDropdown() {
  const menu = document.getElementById('pauseDropdownMenu');
  if (menu) {
    isPauseDropdownOpen = false;
    menu.classList.remove('show');
  }
}

async function triggerPause(minutes) {
  closePauseDropdown();
  appendLog(`[*] Запрос приостановки синхронизации (${minutes > 0 ? minutes + ' мин.' : 'до включения вручную'})...`);
  try {
    if (window.go && window.go.main && window.go.main.App) {
      await window.go.main.App.PauseSync(minutes);
    }
    loadStatus();
  } catch (err) {
    appendLog(`[!] Ошибка паузы: ${err}`);
  }
}

async function triggerResume() {
  closePauseDropdown();
  appendLog('[*] Запрос возобновления синхронизации...');
  try {
    if (window.go && window.go.main && window.go.main.App) {
      await window.go.main.App.ResumeSync();
    }
    loadStatus();
  } catch (err) {
    appendLog(`[!] Ошибка возобновления: ${err}`);
  }
}

async function openSyncIgnoreFile() {
  try {
    if (window.go && window.go.main && window.go.main.App) {
      await window.go.main.App.OpenSyncIgnoreFile();
      appendLog('[+] Файл .syncignore открыт для редактирования в Блокноте.');
    }
  } catch (err) {
    appendLog(`[!] Не удалось открыть .syncignore: ${err}`);
  }
}

async function triggerStop() {
  appendLog('[*] Отправлен запрос остановки синхронизации...');
  const btnStop = document.getElementById('btnStop');
  if (btnStop) {
    btnStop.disabled = true;
    btnStop.innerText = 'Остановка...';
  }
  try {
    await window.go.main.App.TriggerStop();
  } catch (err) {
    appendLog(`[!] Ошибка вызова остановки: ${err}`);
  }
  setTimeout(loadStatus, 400);
}

async function triggerVerify() {
  appendLog('[*] Запуск проверки контрольных сумм MD5...');
  try {
    await window.go.main.App.TriggerVerify();
  } catch (err) {
    appendLog(`[!] Ошибка: ${err}`);
  }
}

async function triggerRestoreTrash() {
  if (!confirm('Восстановить все удаленные файлы и папки из Корзины Google Диска?')) {
    return;
  }
  appendLog('[*] Запуск восстановления объектов из Корзины Google Drive...');
  try {
    await window.go.main.App.TriggerRestore();
    loadStatus();
  } catch (err) {
    appendLog(`[!] Ошибка: ${err}`);
  }
}

async function openLocalExplorer() {
  try {
    await window.go.main.App.OpenLocalFolder();
  } catch (err) {
    appendLog(`[!] Не удалось открыть Проводник: ${err}`);
  }
}

async function triggerAuth() {
  appendLog('[*] Открываем браузер для авторизации Google...');
  try {
    await window.go.main.App.TriggerAuth();
  } catch (err) {
    appendLog(`[!] Ошибка авторизации: ${err}`);
  }
}

async function chooseLocalFolder() {
  appendLog('[*] Выбор локальной папки на диске...');
  try {
    const selected = await window.go.main.App.ChooseLocalFolder();
    if (selected && selected.trim() !== '') {
      document.getElementById('cfgLocalFolder').value = selected;
      document.getElementById('dashLocalFolder').value = selected;
      appendLog(`[+] Выбрана локальная папка: ${selected}`);
      saveSettings();
    }
  } catch (err) {
    appendLog(`[!] Ошибка выбора папки: ${err}`);
  }
}

// Drive Modal Picker
async function openDrivePicker() {
  const modal = document.getElementById('driveModal');
  modal.classList.add('active');
  const tree = document.getElementById('driveFolderTree');
  tree.innerHTML = '<div style="padding: 12px; color: var(--text-muted);">Загрузка папок с Google Диска...</div>';

  try {
    const folders = await window.go.main.App.GetDriveFolders();
    tree.innerHTML = '';

    folders.forEach(f => {
      const item = document.createElement('div');
      item.className = 'folder-item' + (f.id === selectedDriveFolderId ? ' selected' : '');
      item.innerHTML = `<span>📁</span> <span>${escapeHtml(f.name)}</span>`;
      item.onclick = () => {
        document.querySelectorAll('.folder-item').forEach(el => el.classList.remove('selected'));
        item.classList.add('selected');
        selectedDriveFolderId = f.id;
        selectedDriveFolderName = f.name;
      };
      tree.appendChild(item);
    });
  } catch (err) {
    tree.innerHTML = `<div style="padding: 12px; color: var(--accent-red); font-size: 12px;">${escapeHtml(String(err))}</div>`;
  }
}

function closeDrivePicker() {
  document.getElementById('driveModal').classList.remove('active');
}

function confirmDrivePicker() {
  document.getElementById('cfgRemoteFolder').value = selectedDriveFolderId;
  document.getElementById('dashRemoteFolder').value = selectedDriveFolderId;
  appendLog(`[+] Выбрана папка Google Drive: ${selectedDriveFolderName} (${selectedDriveFolderId})`);
  closeDrivePicker();
  saveSettings();
}

async function createDriveFolder() {
  const nameInput = document.getElementById('newFolderName');
  const name = nameInput.value.trim();
  if (!name) return;

  appendLog(`[*] Создание новой папки '${name}' на Google Drive...`);
  try {
    const folder = await window.go.main.App.CreateDriveFolder(name, selectedDriveFolderId);
    nameInput.value = '';
    appendLog(`[+] Папка '${folder.name}' успешно создана.`);
    openDrivePicker();
  } catch (err) {
    appendLog(`[!] Ошибка создания папки: ${err}`);
  }
}

// Automation
async function toggleAutostart(enabled) {
  try {
    await window.go.main.App.ToggleAutostart(enabled);
    appendLog(`[*] Автозапуск Windows: ${enabled ? 'ВКЛ' : 'ВЫКЛ'}`);
  } catch (err) {
    appendLog(`[!] Ошибка настройки автозапуска: ${err}`);
  }
}

async function saveSettings() {
  const intervalMin = parseInt(document.getElementById('cfgInterval').value) || 1;
  const maxDel = parseInt(document.getElementById('cfgMaxDeleteThreshold').value) || 20;
  const concUploads = parseInt(document.getElementById('cfgConcurrentUploads').value) || 4;
  const bwLimit = parseInt(document.getElementById('cfgBandwidthLimit') ? document.getElementById('cfgBandwidthLimit').value : 0) || 0;

  const rawRules = document.getElementById('cfgCustomIgnoreRules') ? document.getElementById('cfgCustomIgnoreRules').value : '';
  const customIgnoreRules = rawRules.split('\n').map(s => s.trim()).filter(Boolean);

  const cfg = {
    local_folder: document.getElementById('cfgLocalFolder').value.trim(),
    remote_folder_id: document.getElementById('cfgRemoteFolder').value.trim() || 'root',
    sync_interval_seconds: intervalMin * 60,
    autostart: document.getElementById('cfgAutostart').checked,
    realtime_watch: document.getElementById('cfgRealtimeWatch').checked,
    concurrent_uploads: concUploads,
    custom_ignore_rules: customIgnoreRules,
    notifications_enabled: document.getElementById('cfgNotificationsEnabled') ? document.getElementById('cfgNotificationsEnabled').checked : true,
    bandwidth_limit_kbps: bwLimit,
    sync_mode: currentSyncMode,
    safety_shield: true,
    allow_remote_deletion: document.getElementById('cfgAllowRemoteDeletion').checked,
    max_delete_threshold: maxDel
  };

  try {
    await window.go.main.App.SaveSettings(cfg);
    loadStatus();
  } catch (err) {
    appendLog(`[!] Ошибка сохранения: ${err}`);
  }
}

// Recent Files Widget Engine
async function loadRecentFiles() {
  if (!window.go || !window.go.main || !window.go.main.App) return;

  const container = document.getElementById('recentFilesList');
  if (!container) return;

  try {
    const items = await window.go.main.App.GetRecentFiles();
    if (!items || items.length === 0) {
      container.innerHTML = '<div class="recent-files-empty">Синхронизированных файлов пока нет.</div>';
      return;
    }

    let html = '';
    for (const it of items) {
      let icon = '↑';
      let badgeClass = 'upload';
      if (it.action === 'download') {
        icon = '↓';
        badgeClass = 'download';
      } else if (it.action === 'delete') {
        icon = '✕';
        badgeClass = 'delete';
      }

      const sizeStr = it.action === 'delete' ? '' : (it.size_formatted || '');

      html += `
        <div class="recent-file-item" title="${escapeHtml(it.rel_path)}">
          <div class="recent-file-left">
            <span class="recent-file-action-badge ${badgeClass}">${icon}</span>
            <span class="recent-file-name">${escapeHtml(it.rel_path)}</span>
          </div>
          <div class="recent-file-right">
            ${sizeStr ? `<span class="recent-file-size">${escapeHtml(sizeStr)}</span>` : ''}
            <span class="recent-file-time">${escapeHtml(it.time_ago || it.timestamp)}</span>
          </div>
        </div>
      `;
    }
    container.innerHTML = html;
  } catch (err) {
    console.error('Failed to load recent files:', err);
  }
}

// Close pause dropdown when clicking outside
document.addEventListener('click', (e) => {
  const wrapper = document.querySelector('.pause-dropdown-wrapper');
  if (wrapper && !wrapper.contains(e.target)) {
    closePauseDropdown();
  }
});

// Event Listeners for Wails Runtime
window.addEventListener('DOMContentLoaded', () => {
  // Setup Wails runtime listeners
  if (window.runtime) {
    window.runtime.EventsOn('log', (msg) => {
      appendLog(msg);
      loadStatus();
      loadRecentFiles();
    });

    window.runtime.EventsOn('status-updated', () => {
      loadStatus();
      loadRecentFiles();
    });

    window.runtime.EventsOn('sync-progress', (p) => {
      handleSyncProgress(p);
    });
  }

  loadStatus();
  loadStorageQuota();
  loadRecentFiles();
  setInterval(loadStatus, 2500);
  setInterval(loadRecentFiles, 5000);
  setInterval(loadStorageQuota, 60000);
});

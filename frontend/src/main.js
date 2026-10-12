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

  if (name === 'search') {
    const input = document.getElementById('fileSearchInput');
    if (input) {
      setTimeout(() => input.focus(), 60);
      if (!input.value) searchFiles('');
    }
  } else if (name === 'storage') {
    loadStorageBreakdown();
  } else if (name === 'settings') {
    loadSelectiveSyncFolders();
  }
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

      const elConflict = document.getElementById('cfgConflictStrategy');
      if (elConflict && elConflict !== activeEl) {
        elConflict.value = cfg.conflict_strategy || 'keep_both';
      }

      const elQuiet = document.getElementById('cfgQuietHoursEnabled');
      if (elQuiet && elQuiet !== activeEl) {
        elQuiet.checked = !!cfg.quiet_hours_enabled;
        toggleQuietHoursUI(elQuiet.checked);
      }

      setValIfNotFocused('cfgQuietHoursStart', cfg.quiet_hours_start || '23:00');
      setValIfNotFocused('cfgQuietHoursEnd', cfg.quiet_hours_end || '07:00');

      currentExcludedFolders = cfg.excluded_folders || [];
      if (cachedSubfolders.length > 0) {
        renderSelectiveSyncList(cachedSubfolders, currentExcludedFolders);
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

function toggleQuietHoursUI(enabled) {
  const row = document.getElementById('quietHoursTimeRow');
  if (row) {
    row.style.display = enabled ? 'flex' : 'none';
  }
}

function showToast(message) {
  const existing = document.querySelector('.toast-notification');
  if (existing) existing.remove();

  const toast = document.createElement('div');
  toast.className = 'toast-notification';
  toast.innerHTML = `<span>✓</span><span>${escapeHtml(message)}</span>`;
  document.body.appendChild(toast);

  setTimeout(() => {
    toast.style.transition = 'opacity 0.3s ease, transform 0.3s ease';
    toast.style.opacity = '0';
    toast.style.transform = 'translateY(10px)';
    setTimeout(() => toast.remove(), 300);
  }, 2500);
}

async function openLocalFile(relPath) {
  if (window.go && window.go.main && window.go.main.App && window.go.main.App.OpenLocalFile) {
    try {
      await window.go.main.App.OpenLocalFile(relPath);
    } catch (err) {
      console.error('Error opening local file:', err);
    }
  }
}

async function openInDrive(relPath) {
  if (window.go && window.go.main && window.go.main.App && window.go.main.App.OpenInGoogleDrive) {
    try {
      await window.go.main.App.OpenInGoogleDrive(relPath);
    } catch (err) {
      console.error('Error opening file in Google Drive:', err);
    }
  }
}

function copyDriveUrl(url) {
  if (!url) return;
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(url).then(() => {
      showToast('Ссылка на Google Диск скопирована!');
    }).catch(() => fallbackCopy(url));
  } else {
    fallbackCopy(url);
  }
}

function fallbackCopy(text) {
  const ta = document.createElement('textarea');
  ta.value = text;
  ta.style.position = 'fixed';
  ta.style.opacity = '0';
  document.body.appendChild(ta);
  ta.select();
  document.execCommand('copy');
  document.body.removeChild(ta);
  showToast('Ссылка на Google Диск скопирована!');
}

async function saveSettings() {
  const intervalMin = parseInt(document.getElementById('cfgInterval').value) || 1;
  const maxDel = parseInt(document.getElementById('cfgMaxDeleteThreshold').value) || 20;
  const concUploads = parseInt(document.getElementById('cfgConcurrentUploads').value) || 4;
  const bwLimit = parseInt(document.getElementById('cfgBandwidthLimit') ? document.getElementById('cfgBandwidthLimit').value : 0) || 0;
  const conflictStrategy = document.getElementById('cfgConflictStrategy') ? document.getElementById('cfgConflictStrategy').value : 'keep_both';
  const quietHoursEnabled = document.getElementById('cfgQuietHoursEnabled') ? document.getElementById('cfgQuietHoursEnabled').checked : false;
  const quietHoursStart = document.getElementById('cfgQuietHoursStart') ? document.getElementById('cfgQuietHoursStart').value : '23:00';
  const quietHoursEnd = document.getElementById('cfgQuietHoursEnd') ? document.getElementById('cfgQuietHoursEnd').value : '07:00';

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
    conflict_strategy: conflictStrategy,
    quiet_hours_enabled: quietHoursEnabled,
    quiet_hours_start: quietHoursStart,
    quiet_hours_end: quietHoursEnd,
    excluded_folders: currentExcludedFolders,
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
      } else if (it.action && it.action.includes('conflict')) {
        icon = '⚡';
        badgeClass = 'conflict';
      }

      const sizeStr = it.action === 'delete' ? '' : (it.size_formatted || '');
      const safeRel = escapeHtml(it.rel_path);
      const safeJsRel = it.rel_path.replace(/\\/g, '\\\\').replace(/'/g, "\\'");
      const safeDriveLink = it.drive_link ? it.drive_link.replace(/'/g, "\\'") : '';

      let actionsHtml = `
        <button class="recent-file-btn" onclick="openLocalFile('${safeJsRel}')" title="Показать в проводнике Windows">📂</button>
      `;

      if (it.drive_link && it.action !== 'delete') {
        actionsHtml += `
          <button class="recent-file-btn" onclick="openInDrive('${safeJsRel}')" title="Открыть в Google Диске (в браузере)">↗</button>
          <button class="recent-file-btn" onclick="shareFileLink('${safeJsRel}')" title="Создать ссылку для доступа и скопировать">🔗</button>
          <button class="recent-file-btn" onclick="openFileRevisions('${safeJsRel}')" title="История версий">🕒</button>
        `;
      }

      html += `
        <div class="recent-file-item" title="${safeRel}">
          <div class="recent-file-left">
            <span class="recent-file-action-badge ${badgeClass}">${icon}</span>
            <span class="recent-file-name">${safeRel}</span>
          </div>
          <div class="recent-file-right">
            ${sizeStr ? `<span class="recent-file-size">${escapeHtml(sizeStr)}</span>` : ''}
            <span class="recent-file-time">${escapeHtml(it.time_ago || it.timestamp)}</span>
            <div class="recent-file-actions">
              ${actionsHtml}
            </div>
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

// --- Phase 5: Search, Storage Breakdown & Selective Sync Engines ---

let searchDebounceTimer = null;

function onSearchInput(val) {
  const clearBtn = document.getElementById('searchClearBtn');
  if (clearBtn) {
    clearBtn.style.display = val ? 'inline-block' : 'none';
  }
  clearTimeout(searchDebounceTimer);
  searchDebounceTimer = setTimeout(() => {
    searchFiles(val);
  }, 180);
}

function clearSearch() {
  const input = document.getElementById('fileSearchInput');
  if (input) {
    input.value = '';
    onSearchInput('');
    input.focus();
  }
}

function getFileIcon(fileName) {
  const ext = fileName.split('.').pop().toLowerCase();
  switch (ext) {
    case 'pdf': return '📕';
    case 'doc': case 'docx': return '📘';
    case 'xls': case 'xlsx': case 'csv': return '📊';
    case 'ppt': case 'pptx': return '📙';
    case 'txt': case 'md': return '📄';
    case 'jpg': case 'jpeg': case 'png': case 'gif': case 'webp': case 'svg': case 'psd': return '🖼️';
    case 'mp4': case 'mkv': case 'avi': case 'mov': return '🎬';
    case 'mp3': case 'wav': case 'flac': return '🎵';
    case 'zip': case 'rar': case '7z': case 'tar': case 'gz': return '📦';
    case 'go': case 'js': case 'ts': case 'py': case 'html': case 'css': case 'json': return '💻';
    default: return '📄';
  }
}

async function searchFiles(query) {
  if (!window.go || !window.go.main || !window.go.main.App || !window.go.main.App.SearchFiles) return;
  const listEl = document.getElementById('searchResultsList');
  const countEl = document.getElementById('searchResultCount');
  if (!listEl) return;

  try {
    const results = await window.go.main.App.SearchFiles(query, 50);
    if (!results || results.length === 0) {
      listEl.innerHTML = '<div class="search-empty-state">Файлы по данному запросу не найдены.</div>';
      if (countEl) countEl.textContent = 'Найдено: 0 файлов';
      return;
    }

    if (countEl) {
      countEl.textContent = `Найдено: ${results.length} файлов${results.length >= 50 ? ' (показаны первые 50)' : ''}`;
    }

    let html = '';
    for (const it of results) {
      const icon = getFileIcon(it.file_name);
      const safeRel = escapeHtml(it.rel_path);
      const safeJsRel = it.rel_path.replace(/\\/g, '\\\\').replace(/'/g, "\\'");
      const safeDriveLink = it.drive_link ? it.drive_link.replace(/'/g, "\\'") : '';

      let actionsHtml = `
        <button class="recent-file-btn" onclick="openLocalFile('${safeJsRel}')" title="Показать в проводнике Windows">📂</button>
      `;

      if (it.drive_link && !it.is_dir) {
        actionsHtml += `
          <button class="recent-file-btn" onclick="openInDrive('${safeJsRel}')" title="Открыть в Google Диске (в браузере)">↗</button>
          <button class="recent-file-btn" onclick="shareFileLink('${safeJsRel}')" title="Создать ссылку для доступа и скопировать">🔗</button>
          <button class="recent-file-btn" onclick="openFileRevisions('${safeJsRel}')" title="История версий">🕒</button>
        `;
      }

      html += `
        <div class="search-result-item" title="${safeRel}">
          <div class="search-result-left">
            <span class="search-file-icon">${icon}</span>
            <div class="search-file-meta">
              <span class="search-file-name">${escapeHtml(it.file_name)}</span>
              <span class="search-file-folder">${escapeHtml(it.folder || '/')}</span>
            </div>
          </div>
          <div class="search-result-right">
            <span class="search-file-size">${escapeHtml(it.size_formatted || '0 Б')}</span>
            <span class="search-file-time">${escapeHtml(it.synced_at || it.mtime || '')}</span>
            <div class="recent-file-actions">
              ${actionsHtml}
            </div>
          </div>
        </div>
      `;
    }
    listEl.innerHTML = html;
  } catch (err) {
    console.error('Search error:', err);
  }
}

async function loadStorageBreakdown() {
  if (!window.go || !window.go.main || !window.go.main.App || !window.go.main.App.GetStorageBreakdown) return;
  const barEl = document.getElementById('storageSegmentedBar');
  const gridEl = document.getElementById('storageCategoriesGrid');
  const sizeEl = document.getElementById('storageTotalSize');
  const countEl = document.getElementById('storageTotalCount');

  try {
    const data = await window.go.main.App.GetStorageBreakdown();
    if (!data) return;

    if (sizeEl) sizeEl.textContent = data.size_formatted || '0 Б';
    if (countEl) countEl.textContent = `${(data.total_files || 0).toLocaleString('ru-RU')} файлов синхронизировано локально`;

    if (barEl && data.categories) {
      let barHtml = '';
      for (const cat of data.categories) {
        if (cat.percentage > 0.5) {
          barHtml += `<div class="storage-segment" style="width: ${cat.percentage}%; background-color: ${cat.color};" title="${escapeHtml(cat.category)}: ${escapeHtml(cat.size_formatted)} (${cat.percentage.toFixed(1)}%)"></div>`;
        }
      }
      barEl.innerHTML = barHtml || '<div style="width: 100%; background: rgba(255,255,255,0.05);"></div>';
    }

    if (gridEl && data.categories) {
      let gridHtml = '';
      for (const cat of data.categories) {
        gridHtml += `
          <div class="storage-category-card">
            <div class="storage-category-header">
              <span class="storage-cat-dot" style="background-color: ${cat.color};"></span>
              <span class="storage-category-name">${escapeHtml(cat.category)}</span>
              <span class="storage-category-pct">${(cat.percentage || 0).toFixed(1)}%</span>
            </div>
            <div class="storage-category-size">${escapeHtml(cat.size_formatted || '0 Б')}</div>
            <div class="storage-category-count">${(cat.count || 0).toLocaleString('ru-RU')} файлов</div>
          </div>
        `;
      }
      gridEl.innerHTML = gridHtml;
    }
  } catch (err) {
    console.error('Failed to load storage breakdown:', err);
  }
}

let currentExcludedFolders = [];
let cachedSubfolders = [];

async function loadSelectiveSyncFolders() {
  if (!window.go || !window.go.main || !window.go.main.App || !window.go.main.App.GetLocalSubfolders) return;
  const listEl = document.getElementById('selectiveSyncList');
  if (!listEl) return;

  try {
    const folders = await window.go.main.App.GetLocalSubfolders();
    cachedSubfolders = folders || [];
    renderSelectiveSyncList(cachedSubfolders, currentExcludedFolders);
  } catch (err) {
    console.error('Failed to get subfolders:', err);
  }
}

function renderSelectiveSyncList(folders, excluded) {
  const listEl = document.getElementById('selectiveSyncList');
  if (!listEl) return;

  if (!folders || folders.length === 0) {
    listEl.innerHTML = '<div style="font-size: 12px; color: var(--text-muted); padding: 8px;">Корневых подпапок в локальной директории не обнаружено.</div>';
    return;
  }

  const exclSet = new Set(excluded || []);
  let html = '';
  for (const f of folders) {
    const isChecked = !exclSet.has(f.name);
    const safeName = escapeHtml(f.name);
    html += `
      <label class="selective-sync-item">
        <input type="checkbox" ${isChecked ? 'checked' : ''} onchange="toggleFolderExclusion('${safeName}', this.checked)">
        <span class="selective-sync-label">
          <span>📁</span>
          <span>${safeName}</span>
        </span>
      </label>
    `;
  }
  listEl.innerHTML = html;
}

function toggleFolderExclusion(folderName, isChecked) {
  if (isChecked) {
    currentExcludedFolders = currentExcludedFolders.filter(f => f !== folderName);
  } else {
    if (!currentExcludedFolders.includes(folderName)) {
      currentExcludedFolders.push(folderName);
    }
  }
}

// --- Phase 6: Share Links, File Revisions & Cloud Trash Cleaner ---

// Share File Link Handler
async function shareFileLink(relPath) {
  if (!window.go || !window.go.main || !window.go.main.App || !window.go.main.App.CreateShareLink) return;
  try {
    showToast('Создание ссылки общего доступа...');
    const res = await window.go.main.App.CreateShareLink(relPath, true);
    if (res && res.url) {
      if (navigator.clipboard && navigator.clipboard.writeText) {
        await navigator.clipboard.writeText(res.url);
      }
      showToast('🔗 Ссылка скопирована в буфер обмена!');
    }
  } catch (err) {
    appendLog(`[!] Ошибка создания ссылки: ${err}`);
    showToast(`Ошибка: ${err}`);
  }
}

// Revisions Modal Handlers
let currentRevisionRelPath = '';

async function openFileRevisions(relPath) {
  currentRevisionRelPath = relPath;
  const modal = document.getElementById('revisionsModal');
  const pathLabel = document.getElementById('revisionsFileRelPath');
  const listEl = document.getElementById('revisionsList');
  if (!modal || !listEl) return;

  if (pathLabel) pathLabel.textContent = relPath;
  listEl.innerHTML = '<div style="padding: 24px; text-align: center; color: var(--text-muted);">Загрузка истории версий из Google Диска...</div>';
  modal.classList.add('active');

  try {
    const revisions = await window.go.main.App.GetFileRevisions(relPath);
    if (!revisions || revisions.length === 0) {
      listEl.innerHTML = '<div style="padding: 24px; text-align: center; color: var(--text-muted);">У данного файла пока нет истории версий.</div>';
      return;
    }

    let html = '';
    revisions.forEach((rev, idx) => {
      const isCurrent = idx === 0;
      const safeJsRel = relPath.replace(/\\/g, '\\\\').replace(/'/g, "\\'");
      const safeJsRevId = rev.id.replace(/'/g, "\\'");

      html += `
        <div class="revision-item">
          <div class="revision-left">
            <div class="revision-header">
              <span class="revision-date">${escapeHtml(rev.modified_time || '')}</span>
              ${isCurrent ? '<span class="revision-badge-current">Текущая версия</span>' : ''}
              ${rev.modified_time_ago ? `<span class="revision-ago">(${escapeHtml(rev.modified_time_ago)})</span>` : ''}
            </div>
            <div class="revision-meta">
              <span>Размер: ${escapeHtml(rev.size_formatted || '0 Б')}</span>
              <span>ID: ${escapeHtml(rev.id)}</span>
            </div>
          </div>
          <div class="revision-right">
            ${isCurrent ? '' : `
              <button class="btn btn-sm btn-primary" onclick="restoreFileRevision('${safeJsRel}', '${safeJsRevId}')" title="Восстановить эту версию файла">
                ↺ Восстановить
              </button>
            `}
          </div>
        </div>
      `;
    });
    listEl.innerHTML = html;
  } catch (err) {
    listEl.innerHTML = `<div style="padding: 16px; color: var(--accent-red); font-size: 12px; text-align: center;">${escapeHtml(String(err))}</div>`;
  }
}

function closeRevisionsModal() {
  const modal = document.getElementById('revisionsModal');
  if (modal) modal.classList.remove('active');
}

async function restoreFileRevision(relPath, revisionId) {
  if (!confirm(`Вы действительно хотите восстановить выбранную версию файла '${relPath}'? Текущий локальный файл будет заменен.`)) {
    return;
  }
  try {
    showToast('Восстановление версии...');
    await window.go.main.App.RestoreFileRevision(relPath, revisionId);
    showToast('✓ Версия успешно восстановлена!');
    closeRevisionsModal();
    loadRecentFiles();
  } catch (err) {
    appendLog(`[!] Ошибка восстановления версии: ${err}`);
    showToast(`Ошибка: ${err}`);
  }
}

// Empty Cloud Trash Handler
async function emptyDriveTrash() {
  if (!confirm('Вы действительно хотите навсегда очистить Корзину Google Диска? Это освободит место в хранилище.')) {
    return;
  }
  try {
    showToast('Очистка Корзины...');
    await window.go.main.App.EmptyDriveTrash();
    showToast('🗑 Корзина Google Диска успешно очищена!');
    loadStorageQuota();
  } catch (err) {
    appendLog(`[!] Ошибка очистки корзины: ${err}`);
    showToast(`Ошибка: ${err}`);
  }
}



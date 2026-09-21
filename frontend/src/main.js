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
    } else {
      authDot.classList.remove('connected');
      authText.textContent = 'Требуется вход Google';
    }

    // Config form values
    if (data.config) {
      const cfg = data.config;
      document.getElementById('dashLocalFolder').value = cfg.local_folder || '';
      document.getElementById('dashRemoteFolder').value = cfg.remote_folder_id || 'root';
      document.getElementById('cfgLocalFolder').value = cfg.local_folder || '';
      document.getElementById('cfgRemoteFolder').value = cfg.remote_folder_id || 'root';
      document.getElementById('cfgAutostart').checked = !!cfg.autostart;
      document.getElementById('cfgInterval').value = Math.max(1, Math.floor((cfg.sync_interval_seconds || 60) / 60));
      document.getElementById('cfgAllowRemoteDeletion').checked = !!cfg.allow_remote_deletion;
      document.getElementById('cfgMaxDeleteThreshold').value = cfg.max_delete_threshold || 20;

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

  const cfg = {
    local_folder: document.getElementById('cfgLocalFolder').value.trim(),
    remote_folder_id: document.getElementById('cfgRemoteFolder').value.trim() || 'root',
    sync_interval_seconds: intervalMin * 60,
    autostart: document.getElementById('cfgAutostart').checked,
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

// Event Listeners for Wails Runtime
window.addEventListener('DOMContentLoaded', () => {
  // Setup Wails runtime listeners
  if (window.runtime) {
    window.runtime.EventsOn('log', (msg) => {
      appendLog(msg);
      loadStatus();
    });

    window.runtime.EventsOn('status-updated', () => {
      loadStatus();
    });
  }

  loadStatus();
  setInterval(loadStatus, 2500);
});

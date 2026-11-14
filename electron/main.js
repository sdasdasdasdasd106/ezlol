'use strict'
const { app, BrowserWindow, shell, Menu, ipcMain, Notification, Tray, nativeImage, globalShortcut, screen } = require('electron')
const { spawn } = require('node:child_process')
const path = require('node:path')
const fs = require('node:fs')
const http = require('node:http')
let autoUpdater = null
try {
  autoUpdater = require('electron-updater').autoUpdater
} catch {
  autoUpdater = null
}
const { MacUpdater } = require('./updater-mac')
let macUpdater = null

// Keep Chromium's profile out of the Go backend's data directory.
app.setPath('userData', process.env.EZLOL_USER_DATA || path.join(app.getPath('appData'), 'ezlol-app'))

const PORT = Number(process.env.EZLOL_PORT || 7331)
const URL = `http://127.0.0.1:${PORT}`
const DEV_UI = process.env.EZLOL_DEV || '' // e.g. http://localhost:5173 to proxy the UI to Vite

let backend = null
let win = null

// One instance only: launching ezlol again (dock, Start menu, login item) shows
// the existing window instead of starting a second backend.
if (!app.requestSingleInstanceLock()) {
  app.quit()
} else {
  app.on('second-instance', () => showWindow())
}

// ---- Desktop settings: login item, tray, voice, hotkeys ----
// Ctrl+Alt, not Ctrl+Shift: on Chinese Windows Ctrl+Shift switches the input
// method / keyboard layout, so in game it could leave an IME on and swallow
// skill keys. Ctrl+<digit> are League emotes, Alt+<digit> self-cast items.
const HOTKEY_DEFAULTS = { scan: 'CommandOrControl+Alt+S', pick1: 'CommandOrControl+Alt+1', pick2: 'CommandOrControl+Alt+2', pick3: 'CommandOrControl+Alt+3' }
// Defaults of earlier versions: saved settings still holding them move to the new ones.
const OLD_HOTKEY_DEFAULTS = { pick1: 'CommandOrControl+Shift+1', pick2: 'CommandOrControl+Shift+2', pick3: 'CommandOrControl+Shift+3' }
// voice: speak the recommendation (optional; League runs in exclusive
// fullscreen, so nothing can be shown over it: the player switches out to the
// window, or listens).
// side / sidePos: the small panel at the side of the screen during a game.
const SIDE_POSITIONS = ['left', 'right']
const APP_SETTINGS_DEFAULTS = { launchAtLogin: false, startInTray: false, voice: false, side: true, sidePos: 'left', hotkeys: HOTKEY_DEFAULTS }
const appSettingsPath = () => path.join(app.getPath('userData'), 'app-settings.json')

/**
 * Checks a hotkey and returns it in canonical accelerator form, or null.
 * Keys: A-Z, 0-9, F1-F24, num0-num9, Mouse4 / Mouse5 (side buttons). Letters
 * and digits need Ctrl or Alt (a bare key, or Shift+key, would be taken from
 * normal typing everywhere); F-keys, numpad keys and side buttons may be used
 * alone. Windows reserves F12 for debuggers.
 */
function normalizeHotkey(raw) {
  if (typeof raw !== 'string' || raw.length > 60) return null
  const mac = process.platform === 'darwin'
  const MODS = {
    ctrl: mac ? 'Control' : 'CommandOrControl',
    control: mac ? 'Control' : 'CommandOrControl',
    commandorcontrol: 'CommandOrControl',
    cmdorctrl: 'CommandOrControl',
    command: 'CommandOrControl',
    cmd: 'CommandOrControl',
    alt: 'Alt',
    option: 'Alt',
    shift: 'Shift',
  }
  const parts = raw.split('+').map((p) => p.trim()).filter(Boolean)
  const mods = new Set()
  let key = null
  for (const p of parts) {
    const m = MODS[p.toLowerCase()]
    if (m) mods.add(m)
    else if (key) return null
    else key = p
  }
  if (!key) return null
  let k = null
  if (/^[a-z]$/i.test(key)) k = key.toUpperCase()
  else if (/^[0-9]$/.test(key)) k = key
  else if (/^f([1-9]|1[0-9]|2[0-4])$/i.test(key)) k = key.toUpperCase()
  else if (/^num[0-9]$/i.test(key)) k = key.toLowerCase()
  else if (/^mouse[45]$/i.test(key)) k = 'Mouse' + key.slice(-1)
  if (!k) return null
  const standalone = /^F\d+$/.test(k) || /^num\d$/.test(k) || /^Mouse\d$/.test(k)
  if (!standalone && !mods.has('CommandOrControl') && !mods.has('Control') && !mods.has('Alt')) return null
  if (k === 'F12' && process.platform === 'win32') return null
  return [...['CommandOrControl', 'Control', 'Alt', 'Shift'].filter((m) => mods.has(m)), k].join('+')
}

function loadAppSettings() {
  try {
    const saved = JSON.parse(fs.readFileSync(appSettingsPath(), 'utf8'))
    const hk = { ...HOTKEY_DEFAULTS }
    for (const k of Object.keys(HOTKEY_DEFAULTS)) {
      const v = saved.hotkeys && saved.hotkeys[k]
      if (!v || v === OLD_HOTKEY_DEFAULTS[k]) continue
      const n = normalizeHotkey(v)
      if (n) hk[k] = n
    }
    const rest = { ...saved }
    for (const k of ['overlay', 'overlayPos', 'overlayDisplay']) delete rest[k] // the removed overlay window
    return { ...APP_SETTINGS_DEFAULTS, ...rest, hotkeys: hk }
  } catch {
    return { ...APP_SETTINGS_DEFAULTS, hotkeys: { ...HOTKEY_DEFAULTS } }
  }
}
function saveAppSettings(s) {
  try {
    fs.mkdirSync(path.dirname(appSettingsPath()), { recursive: true })
    fs.writeFileSync(appSettingsPath(), JSON.stringify(s, null, 2))
  } catch (err) {
    console.error('could not save app settings', err)
  }
}
let appSettings = loadAppSettings()
let tray = null

/** Register / unregister the login item. Only meaningful for the installed app: in dev it would register the electron binary. */
function applyLoginItem() {
  if (!app.isPackaged) return
  try {
    app.setLoginItemSettings({ openAtLogin: appSettings.launchAtLogin, args: appSettings.startInTray ? ['--hidden'] : [] })
  } catch (err) {
    console.error('login item', err)
  }
}
function loginItemState() {
  if (!app.isPackaged) return { launchAtLogin: appSettings.launchAtLogin, loginItemSupported: false }
  try {
    return { launchAtLogin: !!app.getLoginItemSettings().openAtLogin, loginItemSupported: true }
  } catch {
    return { launchAtLogin: appSettings.launchAtLogin, loginItemSupported: false }
  }
}
function appSettingsView() {
  return { ...appSettings, ...loginItemState(), hotkeyErrors, platform: process.platform, packaged: app.isPackaged }
}

function showWindow() {
  if (!win) createWindow(true)
  else {
    if (win.isMinimized()) win.restore()
    win.show()
    win.focus()
  }
  if (process.platform === 'darwin' && app.dock) app.dock.show().catch(() => {})
}

function trayLabel() {
  return process.platform === 'darwin' ? '启动到菜单栏' : '启动到托盘'
}
function updateTray() {
  if (!appSettings.startInTray) {
    if (tray) {
      tray.destroy()
      tray = null
    }
    return
  }
  if (!tray) {
    // nativeImage picks tray@2x.png on HiDPI screens by itself.
    tray = new Tray(nativeImage.createFromPath(path.join(__dirname, 'tray', 'tray.png')))
    tray.setToolTip('ezlol')
    if (process.platform !== 'darwin') tray.on('click', showWindow)
  }
  tray.setContextMenu(
    Menu.buildFromTemplate([
      { label: '打开 ezlol', click: showWindow },
      { type: 'separator' },
      { label: '开机启动', type: 'checkbox', checked: appSettings.launchAtLogin, enabled: app.isPackaged, click: (item) => setAppSettings({ launchAtLogin: item.checked }) },
      { label: trayLabel(), type: 'checkbox', checked: appSettings.startInTray, click: (item) => setAppSettings({ startInTray: item.checked }) },
      { type: 'separator' },
      {
        label: '退出 ezlol',
        click: () => {
          app.isQuitting = true
          app.quit()
        },
      },
    ]),
  )
}
function setAppSettings(patch) {
  const clean = {}
  for (const k of ['launchAtLogin', 'startInTray', 'voice', 'side']) if (patch && typeof patch[k] === 'boolean') clean[k] = patch[k]
  if (patch && SIDE_POSITIONS.includes(patch.sidePos)) clean.sidePos = patch.sidePos
  if (patch && patch.hotkeys && typeof patch.hotkeys === 'object') {
    const hk = { ...appSettings.hotkeys }
    for (const k of Object.keys(HOTKEY_DEFAULTS)) {
      const v = patch.hotkeys[k]
      if (v === undefined || v === hk[k]) continue
      const n = normalizeHotkey(v)
      if (!n) throw new Error(`「${v}」不能用作快捷键：字母和数字要配 Ctrl 或 Alt，F1–F11、小键盘数字和鼠标侧键可以单独用`)
      hk[k] = n
    }
    const seen = {}
    for (const [k, v] of Object.entries(hk)) {
      if (seen[v]) throw new Error(`${v.replace('CommandOrControl', 'Ctrl')} 已经用在别的功能上了`)
      seen[v] = k
    }
    clean.hotkeys = hk
  }
  appSettings = { ...appSettings, ...clean }
  saveAppSettings(appSettings)
  applyLoginItem()
  updateTray()
  registerHotkeys()
  updateSide()
  const view = appSettingsView()
  if (win) win.webContents.send('app-settings', view)
  if (sideWin) sideWin.webContents.send('app-settings', view)
  return view
}
ipcMain.handle('app-settings:get', () => appSettingsView())

let hotkeyErrors = []

// ---- Side panel: a small always-on-top window at the side of the screen ----
// Shows the augment recommendation and the next item during a game, so the
// player does not have to switch out. It never takes focus and lets clicks
// through (a focused window would pull a fullscreen game out of fullscreen),
// and it is left out of screen captures so its text is never read as cards.
// Over a borderless or windowed game it is always visible; over exclusive
// fullscreen only when Windows' fullscreen optimizations are on.
const SIDE_W = 320
let sideWin = null
let sideWant = false
let sideHeight = 200
let sidePreviewUntil = 0

function createSide() {
  sideWin = new BrowserWindow({
    width: SIDE_W,
    height: sideHeight,
    show: false,
    frame: false,
    transparent: false,
    backgroundColor: '#16191f',
    resizable: false,
    movable: false,
    minimizable: false,
    maximizable: false,
    fullscreenable: false,
    skipTaskbar: true,
    focusable: false,
    alwaysOnTop: true,
    hasShadow: false,
    title: 'ezlol',
    webPreferences: { preload: path.join(__dirname, 'preload.js'), contextIsolation: true, nodeIntegration: false, sandbox: true, backgroundThrottling: false },
  })
  sideWin.setAlwaysOnTop(true, 'screen-saver')
  sideWin.setIgnoreMouseEvents(true)
  sideWin.setContentProtection(true) // opaque window: reliably left out of captures
  sideWin.loadURL(`${URL}/?side=1`)
  sideWin.webContents.setWindowOpenHandler(() => ({ action: 'deny' }))
  sideWin.on('closed', () => (sideWin = null))
}

function updateSide() {
  if (!sideWin) return
  const show = Date.now() < sidePreviewUntil || (appSettings.side && sideWant)
  if (!show) {
    if (sideWin.isVisible()) sideWin.hide()
    return
  }
  // Full display bounds: a fullscreen game covers the taskbar.
  const d = screen.getPrimaryDisplay().bounds
  const h = Math.max(60, Math.min(Math.round(d.height * 0.8), sideHeight))
  const m = 12
  const x = appSettings.sidePos === 'right' ? d.x + d.width - SIDE_W - m : d.x + m
  sideWin.setBounds({ x, y: Math.round(d.y + (d.height - h) / 2), width: SIDE_W, height: h })
  if (!sideWin.isVisible()) sideWin.showInactive()
  // Re-assert (about once a second): a game going fullscreen can push it behind.
  sideWin.setAlwaysOnTop(true, 'screen-saver')
  sideWin.moveTop()
}

ipcMain.on('side:want', (e, want, height) => {
  if (!sideWin || e.sender !== sideWin.webContents) return
  sideWant = !!want
  if (height > 0) sideHeight = Math.round(height)
  updateSide()
})
ipcMain.handle('side:preview', () => {
  if (!sideWin) createSide()
  sidePreviewUntil = Date.now() + 8000
  sideWin.webContents.send('side:preview')
  setTimeout(updateSide, 300)
  setTimeout(updateSide, 8100)
  return true
})

ipcMain.handle('voice:test', () => {
  if (win) win.webContents.send('voice:say', '语音播报正常。推荐中间的，大法师。', true)
  return true
})

// ---- Global hotkeys (work while the game has focus, even fullscreen) ----
// Two ways in, both handled by the backend, which runs the action once per
// press and sends the result to the window (SSE "hotkey"):
//   - the backend reads the keyboard state itself (Windows; what OBS does):
//     this still works when a fullscreen game switches registered hotkeys off;
//   - Electron's globalShortcut (RegisterHotKey), which forwards to the backend.
function postJSON(pathname, body, timeoutMs = 3000) {
  return new Promise((resolve) => {
    const data = JSON.stringify(body)
    const req = http.request(`${URL}${pathname}`, { method: 'POST', headers: { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(data) } }, (res) => {
      let out = ''
      res.setEncoding('utf8')
      res.on('data', (d) => (out += d))
      res.on('end', () => {
        try {
          resolve({ status: res.statusCode, body: JSON.parse(out) })
        } catch {
          resolve({ status: res.statusCode, body: {} })
        }
      })
    })
    req.on('error', (err) => resolve({ status: 0, body: { error: String(err.message || err) } }))
    req.setTimeout(timeoutMs, () => req.destroy())
    req.end(data)
  })
}

const fireHotkey = (action) => postJSON('/api/hotkeys/fire', { action })
const isMouse = (acc) => /^(.+\+)?Mouse[45]$/.test(acc)

let hotkeysSuspended = false
let resumeTimer = null
function registerHotkeys() {
  globalShortcut.unregisterAll()
  hotkeyErrors = []
  const hk = appSettings.hotkeys
  void postJSON('/api/hotkeys', { bindings: hk, suspended: hotkeysSuspended })
  if (hotkeysSuspended) return
  for (const action of Object.keys(HOTKEY_DEFAULTS)) {
    const acc = hk[action]
    if (!acc || isMouse(acc)) continue // mouse buttons: only the backend can read them
    try {
      if (!globalShortcut.register(acc, () => fireHotkey(action))) hotkeyErrors.push(acc)
    } catch {
      hotkeyErrors.push(acc)
    }
  }
}
// While a new hotkey is being recorded in Settings, the global ones are let
// go: otherwise pressing a combination that is already registered would be
// taken by the hotkey (and run it) and never reach the recorder.
function suspendHotkeys(on) {
  clearTimeout(resumeTimer)
  if (hotkeysSuspended === on) return
  hotkeysSuspended = on
  if (on) {
    globalShortcut.unregisterAll()
    void postJSON('/api/hotkeys', { suspended: true })
    resumeTimer = setTimeout(() => suspendHotkeys(false), 30000)
  } else {
    registerHotkeys()
  }
}
ipcMain.handle('hotkeys:suspend', (_e, on) => {
  suspendHotkeys(!!on)
  return appSettingsView()
})
ipcMain.handle('hotkeys:defaults', () => setAppSettings({ hotkeys: HOTKEY_DEFAULTS }))
ipcMain.handle('app-settings:set', (_e, patch) => setAppSettings(patch))

const BIN = process.platform === 'win32' ? 'ezlol.exe' : 'ezlol'
function backendPath() {
  if (app.isPackaged) return path.join(process.resourcesPath, BIN)
  return path.join(__dirname, '..', 'bin', BIN)
}

// Reuse an already-running ezlol (e.g. started from a terminal) instead of failing on the port.
function ping() {
  return new Promise((resolve) => {
    const req = http.get(`${URL}/api/status`, (res) => {
      res.resume()
      resolve(res.statusCode === 200)
    })
    req.on('error', () => resolve(false))
    req.setTimeout(500, () => {
      req.destroy()
      resolve(false)
    })
  })
}

async function startBackend() {
  if (await ping()) return
  const bin = backendPath()
  if (!fs.existsSync(bin)) throw new Error(`backend not found at ${bin}; run make build`)
  const args = ['-no-open', '-addr', `127.0.0.1:${PORT}`]
  if (DEV_UI) args.push('-dev', DEV_UI)
  const env = { ...process.env }
  if (process.platform === 'darwin') {
    const ocr = app.isPackaged ? path.join(process.resourcesPath, 'ezlol-ocr') : path.join(__dirname, '..', 'bin', 'ezlol-ocr')
    if (fs.existsSync(ocr)) env.EZLOL_OCR = ocr
  }
  backend = spawn(bin, args, { stdio: ['ignore', 'pipe', 'pipe'], env, windowsHide: true })
  backend.stdout.on('data', (d) => process.stdout.write(d))
  backend.stderr.on('data', (d) => process.stderr.write(d))
  backend.on('exit', (code) => {
    backend = null
    if (!app.isQuitting) console.error(`ezlol backend exited with ${code}`)
  })
  for (let i = 0; i < 100; i++) {
    if (await ping()) return
    await new Promise((r) => setTimeout(r, 100))
  }
  throw new Error('backend did not come up')
}

function hideAllWindows() {
  for (const w of BrowserWindow.getAllWindows()) {
    try {
      w.hide()
    } catch {
      /* ignore */
    }
  }
}

function createWindow(show = true) {
  win = new BrowserWindow({
    show,
    width: 1280,
    height: 860,
    minWidth: 900,
    minHeight: 600,
    title: 'ezlol',
    backgroundColor: '#010a13',
    titleBarStyle: 'hiddenInset',
    trafficLightPosition: { x: 14, y: 16 },
    webPreferences: {
      preload: path.join(__dirname, 'preload.js'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
      // Keep polling the game and speaking while hidden behind a fullscreen game.
      backgroundThrottling: false,
    },
  })
  // Invisible to screen capture (shows as black there): the augment reader
  // captures the screen, and this window lists augment names that would
  // otherwise be read as an augment choice whenever it is in front of the game.
  win.setContentProtection(true)
  win.loadURL(URL)
  // Open external links (Riot dev portal etc.) in the system browser, never inside the app.
  win.webContents.setWindowOpenHandler(({ url }) => {
    if (/^https?:\/\//.test(url)) shell.openExternal(url)
    return { action: 'deny' }
  })
  win.webContents.on('will-navigate', (e, url) => {
    if (!url.startsWith(URL)) {
      e.preventDefault()
      shell.openExternal(url)
    }
  })
  win.on('blur', () => suspendHotkeys(false))
  // Tray mode: the close button hides the window and ezlol keeps accepting queues.
  win.on('close', (e) => {
    if (appSettings.startInTray && !app.isQuitting) {
      e.preventDefault()
      win.hide()
    }
  })
  win.on('closed', () => {
    win = null
    // Closing the window quits ezlol (the side panel is a window too)
    // (tray mode keeps running hidden instead, see 'close' above).
    if (process.platform !== 'darwin' && !app.isQuitting) {
      app.isQuitting = true
      app.quit()
    }
  })
}

ipcMain.handle('notify', (_e, title, body) => {
  try {
    if (Notification.isSupported()) new Notification({ title: String(title), body: String(body), silent: true }).show()
    if (process.platform === 'darwin' && app.dock) app.dock.bounce('critical')
  } catch (err) {
    console.error('notify failed', err)
  }
  return true
})

// In-place updates: Windows (NSIS) installs on quit; macOS needs a signed app for
// electron-updater, so unsigned mac builds fall back to the download banner in the UI.
async function updatesEnabled() {
  return new Promise((resolve) => {
    const req = http.get(`${URL}/api/settings`, (res) => {
      let body = ''
      res.setEncoding('utf8')
      res.on('data', (d) => (body += d))
      res.on('end', () => {
        try {
          resolve(JSON.parse(body).updateCheck !== false)
        } catch {
          resolve(true)
        }
      })
    })
    req.on('error', () => resolve(true))
    req.setTimeout(2000, () => {
      req.destroy()
      resolve(true)
    })
  })
}

/** True when this bundle carries a Developer ID signature (ad-hoc signed builds have a seal too, so check the authority). */
function developerIdSigned() {
  if (process.platform !== 'darwin' || !app.isPackaged) return false
  try {
    const appPath = path.resolve(process.resourcesPath, '..', '..')
    const res = require('node:child_process').spawnSync('codesign', ['-dv', appPath], { encoding: 'utf8' })
    return /Authority=Developer ID Application/.test(`${res.stdout || ''}${res.stderr || ''}`)
  } catch {
    return false
  }
}

function setupUpdates() {
  if (!app.isPackaged) return
  const send = (payload) => win && win.webContents.send('update-state', payload)
  // Signed + notarized mac builds (CI with CSC_LINK) can use electron-updater like
  // Windows; unsigned (ad-hoc signed) ones use our own download/verify/swap updater.
  if (process.platform === 'darwin' && !developerIdSigned()) {
    macUpdater = new MacUpdater(send)
    const check = async () => {
      if (await updatesEnabled()) macUpdater.check()
    }
    setTimeout(check, 15000)
    setInterval(check, 6 * 60 * 60 * 1000)
    return
  }
  if (!autoUpdater) return
  autoUpdater.autoDownload = true
  autoUpdater.autoInstallOnAppQuit = true
  autoUpdater.on('checking-for-update', () => send({ status: 'checking' }))
  autoUpdater.on('update-available', (i) => send({ status: 'downloading', version: i.version }))
  autoUpdater.on('update-not-available', () => send({ status: 'none' }))
  autoUpdater.on('update-downloaded', (i) => send({ status: 'downloaded', version: i.version }))
  autoUpdater.on('error', (e) => send({ status: 'error', error: String(e && e.message ? e.message : e) }))
  const check = async () => {
    if (await updatesEnabled()) autoUpdater.checkForUpdates().catch(() => {})
  }
  setTimeout(check, 15000)
  setInterval(check, 6 * 60 * 60 * 1000)
}
ipcMain.handle('install-update', async () => {
  if (macUpdater) {
    try {
      await macUpdater.install(async () => {
        app.isQuitting = true
        // Make sure the old backend is gone before the new instance starts, or the
        // new app would attach to a backend that is about to die.
        if (backend) {
          const b = backend
          await new Promise((resolve) => {
            const t = setTimeout(() => {
              try {
                b.kill('SIGKILL')
              } catch {
                /* already gone */
              }
              resolve()
            }, 3000)
            b.once('exit', () => {
              clearTimeout(t)
              resolve()
            })
            b.kill('SIGTERM')
          })
        }
        hideAllWindows()
      })
    } catch (err) {
      if (win) win.webContents.send('update-state', { status: 'error', error: String(err && err.message ? err.message : err) })
      return false
    }
    return true
  }
  if (autoUpdater) autoUpdater.quitAndInstall()
  return true
})
ipcMain.handle('check-update', async () => {
  if (macUpdater) {
    await macUpdater.check()
    return macUpdater.state
  }
  if (autoUpdater && app.isPackaged) {
    try {
      await autoUpdater.checkForUpdates()
    } catch (err) {
      return { status: 'error', error: String(err && err.message ? err.message : err) }
    }
  }
  return { status: 'checking' }
})

app.whenReady().then(async () => {
  Menu.setApplicationMenu(
    Menu.buildFromTemplate([
      { role: 'appMenu' },
      { role: 'editMenu' },
      { role: 'viewMenu' },
      { role: 'windowMenu' },
    ]),
  )
  try {
    await startBackend()
  } catch (err) {
    const { dialog } = require('electron')
    dialog.showErrorBox('ezlol', String(err.message || err))
    app.quit()
    return
  }
  // "Start in the tray": come up hidden behind the tray icon, whoever launched us.
  createWindow(!appSettings.startInTray)
  applyLoginItem()
  updateTray()
  registerHotkeys()
  createSide()
  screen.on('display-metrics-changed', updateSide)
  setupUpdates()
  app.on('activate', () => showWindow())
})

app.on('will-quit', () => globalShortcut.unregisterAll())

app.on('before-quit', () => {
  app.isQuitting = true
  if (backend) backend.kill('SIGTERM')
})

app.on('window-all-closed', () => {
  // Keep the watcher alive in the dock so queue pops are still accepted with the window closed.
  if (process.platform !== 'darwin') app.quit()
})

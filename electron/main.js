'use strict'
const { app, BrowserWindow, shell, Menu, ipcMain, Notification, Tray, nativeImage } = require('electron')
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

// ---- Desktop settings: launch at login, start in the tray / menu bar ----
const APP_SETTINGS_DEFAULTS = { launchAtLogin: false, startInTray: false }
const appSettingsPath = () => path.join(app.getPath('userData'), 'app-settings.json')
function loadAppSettings() {
  try {
    return { ...APP_SETTINGS_DEFAULTS, ...JSON.parse(fs.readFileSync(appSettingsPath(), 'utf8')) }
  } catch {
    return { ...APP_SETTINGS_DEFAULTS }
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
  return { ...appSettings, ...loginItemState(), platform: process.platform, packaged: app.isPackaged }
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
  for (const k of Object.keys(APP_SETTINGS_DEFAULTS)) if (patch && typeof patch[k] === 'boolean') clean[k] = patch[k]
  appSettings = { ...appSettings, ...clean }
  saveAppSettings(appSettings)
  applyLoginItem()
  updateTray()
  const view = appSettingsView()
  if (win) win.webContents.send('app-settings', view)
  return view
}
ipcMain.handle('app-settings:get', () => appSettingsView())
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
    },
  })
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
  // Tray mode: the close button hides the window and ezlol keeps accepting queues.
  win.on('close', (e) => {
    if (appSettings.startInTray && !app.isQuitting) {
      e.preventDefault()
      win.hide()
    }
  })
  win.on('closed', () => (win = null))
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
  setupUpdates()
  app.on('activate', () => showWindow())
})

app.on('before-quit', () => {
  app.isQuitting = true
  if (backend) backend.kill('SIGTERM')
})

app.on('window-all-closed', () => {
  // Keep the watcher alive in the dock so queue pops are still accepted with the window closed.
  if (process.platform !== 'darwin') app.quit()
})

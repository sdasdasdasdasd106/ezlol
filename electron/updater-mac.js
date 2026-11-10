'use strict'
// macOS self-updater for unsigned builds. electron-updater refuses to update an
// unsigned app on macOS, so we do it ourselves: download the release zip that
// matches this machine's architecture, verify it against SHA256SUMS.txt, strip
// the quarantine attribute, and on "install" swap the .app bundle and relaunch.
const { app } = require('electron')
const https = require('node:https')
const fs = require('node:fs')
const path = require('node:path')
const crypto = require('node:crypto')
const { execFile, spawn } = require('node:child_process')

// Releases come from the repository electron-builder publishes to (set in
// package.json; CI points it at the repository that builds the app).
const PUBLISH = (require('./package.json').build?.publish ?? [])[0] ?? {}
const REPO = `${PUBLISH.owner ?? 'aarlint'}/${PUBLISH.repo ?? 'ezlol'}`
const UA = `ezlol/${app.getVersion()} (${process.platform}; ${process.arch})`

// Downloads may only come from GitHub itself (release assets redirect to
// objects.githubusercontent.com). Anything else is refused.
function allowedURL(url) {
  try {
    const u = new URL(url)
    return u.protocol === 'https:' && /(^|\.)(github\.com|githubusercontent\.com)$/.test(u.hostname)
  } catch {
    return false
  }
}

function getJSON(url) {
  return new Promise((resolve, reject) => {
    if (!allowedURL(url)) return reject(new Error(`refusing non-GitHub URL ${url}`))
    const req = https.get(url, { headers: { 'User-Agent': UA, Accept: 'application/vnd.github+json' } }, (res) => {
      if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
        res.resume()
        return getJSON(res.headers.location).then(resolve, reject)
      }
      if (res.statusCode !== 200) {
        res.resume()
        return reject(new Error(`GitHub ${res.statusCode} for ${url}`))
      }
      let body = ''
      res.setEncoding('utf8')
      res.on('data', (d) => (body += d))
      res.on('end', () => {
        try {
          resolve(JSON.parse(body))
        } catch (e) {
          reject(e)
        }
      })
    })
    req.on('error', reject)
    req.setTimeout(20000, () => req.destroy(new Error('timeout')))
  })
}

function getText(url) {
  return new Promise((resolve, reject) => {
    if (!allowedURL(url)) return reject(new Error(`refusing non-GitHub URL ${url}`))
    const req = https.get(url, { headers: { 'User-Agent': UA } }, (res) => {
      if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
        res.resume()
        return getText(res.headers.location).then(resolve, reject)
      }
      if (res.statusCode !== 200) {
        res.resume()
        return reject(new Error(`HTTP ${res.statusCode} for ${url}`))
      }
      let body = ''
      res.setEncoding('utf8')
      res.on('data', (d) => (body += d))
      res.on('end', () => resolve(body))
    })
    req.on('error', reject)
    req.setTimeout(20000, () => req.destroy(new Error('timeout')))
  })
}

// Download to file, following redirects, returning the sha256 hex digest.
function download(url, dest, onProgress) {
  return new Promise((resolve, reject) => {
    if (!allowedURL(url)) return reject(new Error(`refusing non-GitHub URL ${url}`))
    const req = https.get(url, { headers: { 'User-Agent': UA } }, (res) => {
      if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
        res.resume()
        return download(res.headers.location, dest, onProgress).then(resolve, reject)
      }
      if (res.statusCode !== 200) {
        res.resume()
        return reject(new Error(`HTTP ${res.statusCode} downloading ${url}`))
      }
      const total = Number(res.headers['content-length'] || 0)
      let got = 0
      const hash = crypto.createHash('sha256')
      const out = fs.createWriteStream(dest)
      res.on('data', (chunk) => {
        hash.update(chunk)
        got += chunk.length
        if (onProgress && total) onProgress(got / total)
      })
      res.on('error', reject)
      out.on('error', reject)
      out.on('finish', () => resolve(hash.digest('hex')))
      res.pipe(out)
    })
    req.on('error', reject)
    req.setTimeout(60000, () => req.destroy(new Error('timeout')))
  })
}

function run(cmd, args, opts) {
  return new Promise((resolve, reject) => {
    execFile(cmd, args, { ...opts, maxBuffer: 8 << 20 }, (err, stdout, stderr) => {
      if (err) return reject(new Error(`${cmd} ${args.join(' ')}: ${stderr || err.message}`))
      resolve(stdout)
    })
  })
}

function versionNewer(a, b) {
  const pa = String(a).replace(/^v/, '').split('.').map((n) => parseInt(n, 10) || 0)
  const pb = String(b).replace(/^v/, '').split('.').map((n) => parseInt(n, 10) || 0)
  for (let i = 0; i < 3; i++) {
    if ((pa[i] || 0) !== (pb[i] || 0)) return (pa[i] || 0) > (pb[i] || 0)
  }
  return false
}

// Path of the running .app bundle, or null when not running from a bundle.
function currentBundle() {
  const exe = app.getPath('exe') // .../ezlol.app/Contents/MacOS/ezlol
  const i = exe.indexOf('.app/Contents/MacOS/')
  if (i < 0) return null
  return exe.slice(0, i + 4)
}

class MacUpdater {
  constructor(send) {
    this.send = send // (state) => void, forwards to the renderer
    this.state = { status: 'idle' }
    this.staged = null // { version, appPath }
    this.busy = false
  }

  set(state) {
    this.state = state
    try {
      this.send(state)
    } catch {
      /* window may be gone */
    }
  }

  async check() {
    if (this.busy || !app.isPackaged || process.platform !== 'darwin') return
    if (this.staged) {
      this.set({ status: 'downloaded', version: this.staged.version })
      return
    }
    this.busy = true
    try {
      this.set({ status: 'checking' })
      const rel = await getJSON(`https://api.github.com/repos/${REPO}/releases/latest`)
      const latest = String(rel.tag_name || '').replace(/^v/, '')
      if (!versionNewer(latest, app.getVersion())) {
        this.set({ status: 'none' })
        return
      }
      const arch = process.arch === 'arm64' ? 'arm64' : 'x64'
      const want = `ezlol-${latest}-mac-${arch}.zip`
      const asset = (rel.assets || []).find((a) => a.name === want)
      const sums = (rel.assets || []).find((a) => a.name === 'SHA256SUMS.txt')
      if (!asset) throw new Error(`no ${want} in release ${latest}`)
      if (!sums) throw new Error(`release ${latest} has no SHA256SUMS.txt; refusing to install unverified update`)
      const txt = await getText(sums.browser_download_url)
      let expected = null
      for (const raw of txt.split('\n')) {
        const m = raw.trim().match(/^([0-9a-fA-F]{64})\s+\*?(.+)$/)
        if (m && m[2].trim() === want) expected = m[1].toLowerCase()
      }
      if (!expected) throw new Error(`no checksum for ${want} in SHA256SUMS.txt; refusing to install`)
      const dir = path.join(app.getPath('temp'), 'ezlol-update')
      fs.rmSync(dir, { recursive: true, force: true })
      fs.mkdirSync(dir, { recursive: true })
      const zip = path.join(dir, want)
      this.set({ status: 'downloading', version: latest, progress: 0 })
      let lastP = -1
      const digest = await download(asset.browser_download_url, zip, (p) => {
        if (p - lastP >= 0.02 || p >= 1) {
          lastP = p
          this.set({ status: 'downloading', version: latest, progress: p })
        }
      })
      if (digest !== expected) {
        fs.rmSync(dir, { recursive: true, force: true })
        throw new Error('checksum mismatch, download discarded')
      }
      // ditto preserves bundle metadata; unzip would drop resource forks and symlinks.
      await run('/usr/bin/ditto', ['-x', '-k', zip, dir])
      const appPath = path.join(dir, 'ezlol.app')
      if (!fs.existsSync(path.join(appPath, 'Contents', 'MacOS'))) throw new Error('zip did not contain ezlol.app')
      // Files we downloaded ourselves carry the quarantine flag; drop it so Gatekeeper
      // does not block the swapped-in bundle.
      await run('/usr/bin/xattr', ['-dr', 'com.apple.quarantine', appPath]).catch(() => {})
      this.staged = { version: latest, appPath }
      this.set({ status: 'downloaded', version: latest })
    } catch (err) {
      this.set({ status: 'error', error: String(err && err.message ? err.message : err) })
    } finally {
      this.busy = false
    }
  }

  // Swap the running bundle for the staged one and relaunch.
  async install(beforeQuit) {
    if (!this.staged) throw new Error('no update staged')
    const target = currentBundle()
    if (!target) throw new Error('not running from an .app bundle')
    if (target.includes('/AppTranslocation/')) {
      throw new Error('ezlol is running from a quarantined location; move ezlol.app into Applications and open it from there, then update')
    }
    const parent = path.dirname(target)
    try {
      fs.accessSync(parent, fs.constants.W_OK)
    } catch {
      throw new Error(`cannot write to ${parent}; move ezlol.app to a folder you own (e.g. ~/Applications) or reinstall from the release page`)
    }
    const backup = target + '.old'
    fs.rmSync(backup, { recursive: true, force: true })
    fs.renameSync(target, backup)
    try {
      // Same volume in practice (temp -> /Applications is cross-volume on some setups), so copy with ditto then remove.
      await run('/usr/bin/ditto', [this.staged.appPath, target])
    } catch (err) {
      fs.rmSync(target, { recursive: true, force: true })
      fs.renameSync(backup, target)
      throw err
    }
    fs.rmSync(backup, { recursive: true, force: true })
    if (beforeQuit) await beforeQuit()
    // Relaunch after this process has exited. The bundle path is passed as an
    // argument ($0), never interpolated into the shell string.
    const child = spawn('/bin/sh', ['-c', 'sleep 1.5; exec /usr/bin/open -n "$0"', target], { detached: true, stdio: 'ignore' })
    child.unref()
    app.exit(0)
  }
}

module.exports = { MacUpdater }

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { api } from '../api'
import type { SettingsView, UpdateInfo } from '../types'
import type { AppSettings, Hotkeys, OverlayPos } from '../api'
import * as sound from '../sound'

const emit = defineEmits<{ close: [] }>()

const s = ref<SettingsView | null>(null)
const upd = ref<UpdateInfo | null>(null)
const msg = ref('')
const msgKind = ref<'ok' | 'err'>('ok')
const busy = ref(false)
const loadError = ref('')
const soundOn = ref(sound.enabled())
const dialog = ref<HTMLElement | null>(null)
// Desktop-only settings live in the Electron shell; absent in a plain browser.
const desktop = !!window.ezlol?.getAppSettings
const appS = ref<AppSettings | null>(null)
async function saveApp(patch: Partial<Pick<AppSettings, 'launchAtLogin' | 'startInTray' | 'overlay' | 'overlayPos' | 'hotkeys'>>) {
  if (!window.ezlol?.setAppSettings) return
  busy.value = true
  try {
    appS.value = await window.ezlol.setAppSettings(patch)
    flash('已保存', 'ok')
  } catch (e) {
    flash(errText(e), 'err')
  } finally {
    busy.value = false
  }
}
if (desktop) window.ezlol?.onAppSettings?.((v) => (appS.value = v))
const POSITIONS: { id: OverlayPos; label: string }[] = [
  { id: 'bottom-left', label: '左下角' },
  { id: 'left-middle', label: '左侧中间' },
  { id: 'top-left', label: '左上角' },
  { id: 'top-right', label: '右上角' },
  { id: 'bottom-right', label: '右下角' },
]
const HOTKEY_ROWS: { key: keyof Hotkeys; label: string }[] = [
  { key: 'pick1', label: '标记选了第 1 个' },
  { key: 'pick2', label: '标记选了第 2 个' },
  { key: 'pick3', label: '标记选了第 3 个' },
  { key: 'toggle', label: '显示 / 隐藏小窗' },
]
const prettyKey = (k: string) => k.replace('CommandOrControl', 'Ctrl')
const hkDraft = ref<Partial<Hotkeys>>({})
function saveHotkey(k: keyof Hotkeys) {
  const raw = (hkDraft.value[k] ?? '').trim().replace(/\s+/g, '').replace(/^Ctrl\+/i, 'CommandOrControl+').replace(/\+Ctrl\+/i, '+CommandOrControl+')
  if (!appS.value || !raw) return
  void saveApp({ hotkeys: { ...appS.value.hotkeys, [k]: raw } })
  hkDraft.value[k] = undefined
}
async function previewOverlay() {
  await window.ezlol?.previewOverlay?.()
  flash('小窗预览 10 秒', 'ok')
}
let msgTimer: ReturnType<typeof setTimeout> | undefined
let pressOnBackdrop = false

/** Badge, description and tint for the Version row, derived together so they never disagree. */
const updateState = computed(() => {
  const u = upd.value
  if (!u) return { badge: '检查中…', cls: '', desc: '正在检查新版本…', descCls: '' }
  if (u.hasUpdate) return { badge: `有新版本 ${u.latest}`, cls: 'riot', desc: `当前 ${u.current}，可以下载 ${u.latest}。`, descCls: 'ok' }
  if (u.error) return { badge: '检查失败', cls: '', desc: `检查失败：${u.error}`, descCls: 'warn' }
  return { badge: '已是最新', cls: 'lcu', desc: `当前 ${u.current}，已是最新版本。`, descCls: '' }
})

function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

function flash(text: string, kind: 'ok' | 'err') {
  msg.value = text
  msgKind.value = kind
  clearTimeout(msgTimer)
  msgTimer = setTimeout(() => (msg.value = ''), 2500)
}

async function load() {
  loadError.value = ''
  let view: SettingsView
  try {
    view = await api.settings()
  } catch (e) {
    loadError.value = errText(e)
    return
  }
  s.value = view
  if (desktop) window.ezlol?.getAppSettings?.().then((v) => (appS.value = v)).catch(() => (appS.value = null))
  try {
    upd.value = await api.update()
  } catch (e) {
    // Surface the failure on the Version row rather than leaving it on "checking…".
    upd.value = { current: view.version, latest: '', hasUpdate: false, url: '', notes: '', checkedAt: '', error: errText(e) }
  }
}

/** Persist a partial settings patch; resolves true on success. */
async function save(patch: Record<string, unknown>): Promise<boolean> {
  busy.value = true
  try {
    s.value = await api.saveSettings(patch)
    flash('已保存', 'ok')
    return true
  } catch (e) {
    flash(errText(e), 'err')
    return false
  } finally {
    busy.value = false
  }
}

async function check() {
  busy.value = true
  try {
    window.ezlol?.checkUpdate?.()
    upd.value = await api.checkUpdate()
  } catch (e) {
    flash(errText(e), 'err')
  } finally {
    busy.value = false
  }
}

function download() {
  if (upd.value?.url) window.open(upd.value.url, '_blank', 'noopener,noreferrer')
}

async function openLogs() {
  try {
    await api.openLogs()
  } catch (e) {
    flash(errText(e), 'err')
  }
}

function toggleSound() {
  soundOn.value = !soundOn.value
  sound.setEnabled(soundOn.value)
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    e.preventDefault()
    emit('close')
  }
}

// Close only when the press started on the backdrop too, so a text drag that
// ends outside the dialog (selecting in the key input) does not dismiss it.
function onBackdropDown(e: PointerEvent) {
  pressOnBackdrop = e.target === e.currentTarget
}
function onBackdropClick(e: MouseEvent) {
  if (pressOnBackdrop && e.target === e.currentTarget) emit('close')
}

onMounted(() => {
  document.addEventListener('keydown', onKey)
  dialog.value?.focus()
  void load()
})
onBeforeUnmount(() => {
  document.removeEventListener('keydown', onKey)
  clearTimeout(msgTimer)
})
</script>

<template>
  <div class="backdrop" @pointerdown="onBackdropDown" @click="onBackdropClick">
    <section ref="dialog" class="panel modal" role="dialog" aria-modal="true" aria-labelledby="set-title" tabindex="-1">
      <h2>
        <span id="set-title">设置</span>
        <span class="ver">ezlol {{ s?.version ?? '…' }}</span>
        <span class="status" :class="msgKind" aria-live="polite" :title="msg || undefined">{{ msg }}</span>
        <button type="button" class="x" aria-label="关闭设置" @click="emit('close')">×</button>
      </h2>

      <div class="body">
        <template v-if="s">
          <!-- In-game -->
          <h3>强化推荐</h3>
          <dl class="rows">
            <div class="setting">
              <dt>
                <span class="lbl"><label id="set-lbl-ocr" for="set-ocr">识别屏幕上的强化选项</label></span>
                <span v-if="s.ocrAvailable" class="desc muted">选强化时自动读出屏幕上的三个选项，告诉你选哪个。游戏请用「无边框」或「窗口」模式，全屏模式可能截不到画面。</span>
                <span v-else class="desc warn">这台电脑上用不了屏幕识别。</span>
                <span v-if="s.ocrAvailable && s.ocrLang && !s.ocrLang.startsWith('zh')" class="desc warn">
                  当前识别语言是 {{ s.ocrLang }}，认不出中文强化名。请在 Windows「设置 → 时间和语言 → 语言」里添加「中文(简体)」，并勾选「光学字符识别」，然后重启 ezlol。
                </span>
              </dt>
              <dd>
                <button id="set-ocr" type="button" role="switch" class="toggle" :class="{ on: s.ocr && s.ocrAvailable }" :aria-checked="s.ocr && s.ocrAvailable" aria-labelledby="set-lbl-ocr" :disabled="!s.ocrAvailable" :title="s.ocrAvailable ? undefined : '这台电脑上用不了屏幕识别'" @click="save({ ocr: !s.ocr })">
                  <span class="track" aria-hidden="true" /><span class="state">{{ s.ocr && s.ocrAvailable ? '开' : '关' }}</span>
                </button>
              </dd>
            </div>
          </dl>

          <!-- Test log -->
          <h3>测试日志</h3>
          <dl class="rows">
            <div class="setting">
              <dt>
                <span class="lbl"><label id="set-lbl-tlog" for="set-tlog">记录测试日志</label></span>
                <span class="desc muted">把每次识别读到的文字、匹配结果、推荐理由和记录的已选强化写进本机日志，测试完把日志发出来就能排查问题。</span>
              </dt>
              <dd>
                <button id="set-tlog" type="button" role="switch" class="toggle" :class="{ on: s.testLog }" :aria-checked="s.testLog" aria-labelledby="set-lbl-tlog" @click="save({ testLog: !s.testLog })">
                  <span class="track" aria-hidden="true" /><span class="state">{{ s.testLog ? '开' : '关' }}</span>
                </button>
              </dd>
            </div>
            <div class="setting">
              <dt>
                <span class="lbl"><label id="set-lbl-shots" for="set-shots">同时保存截图</label></span>
                <span class="desc muted">识别到强化时保存当时截下的画面（只保留最近 60 张）。识别不准时打开它最有用，平时可以关掉。</span>
              </dt>
              <dd>
                <button id="set-shots" type="button" role="switch" class="toggle" :class="{ on: s.logShots }" :aria-checked="s.logShots" aria-labelledby="set-lbl-shots" :disabled="!s.testLog" @click="save({ logShots: !s.logShots })">
                  <span class="track" aria-hidden="true" /><span class="state">{{ s.logShots ? '开' : '关' }}</span>
                </button>
              </dd>
            </div>
            <div class="setting">
              <dt>
                <span class="lbl">日志位置</span>
                <span class="desc muted" :title="s.logDir">{{ s.logDir }}</span>
              </dt>
              <dd>
                <button type="button" @click="openLogs">打开日志文件夹</button>
              </dd>
            </div>
          </dl>

          <!-- App -->
          <h3>程序</h3>
          <dl class="rows">
            <div class="setting">
              <dt>
                <span class="lbl">版本 <span class="badge" :class="updateState.cls">{{ updateState.badge }}</span></span>
                <span class="desc muted" :class="updateState.descCls">{{ updateState.desc }}</span>
              </dt>
              <dd>
                <button type="button" :disabled="busy" @click="check">立即检查</button>
                <button v-if="upd?.hasUpdate" type="button" class="primary" @click="download">下载</button>
              </dd>
            </div>

            <div class="setting">
              <dt>
                <span class="lbl"><label id="set-lbl-updates" for="set-updates">自动检查更新</label></span>
                <span class="desc muted">每次启动时检查有没有新版本。</span>
              </dt>
              <dd>
                <button id="set-updates" type="button" role="switch" class="toggle" :class="{ on: s.updateCheck }" :aria-checked="s.updateCheck" aria-labelledby="set-lbl-updates" @click="save({ updateCheck: !s.updateCheck })">
                  <span class="track" aria-hidden="true" /><span class="state">{{ s.updateCheck ? '开' : '关' }}</span>
                </button>
              </dd>
            </div>

            <div class="setting">
              <dt>
                <span class="lbl"><label id="set-lbl-sound" for="set-sound">提示音</label></span>
                <span class="desc muted">识别到强化选择时响一声。</span>
              </dt>
              <dd>
                <button id="set-sound" type="button" role="switch" class="toggle" :class="{ on: soundOn }" :aria-checked="soundOn" aria-labelledby="set-lbl-sound" @click="toggleSound">
                  <span class="track" aria-hidden="true" /><span class="state">{{ soundOn ? '开' : '关' }}</span>
                </button>
              </dd>
            </div>
          </dl>

          <!-- Desktop app (Electron only) -->
          <template v-if="desktop && appS">
            <h3>桌面</h3>
            <dl class="rows">
              <div class="setting">
                <dt>
                  <span class="lbl"><label id="set-lbl-login" for="set-login">开机启动</label></span>
                  <span v-if="appS.loginItemSupported" class="desc muted">登录电脑时自动启动 ezlol。</span>
                  <span v-else class="desc warn">只有安装版可用。</span>
                </dt>
                <dd>
                  <button id="set-login" type="button" role="switch" class="toggle" :class="{ on: appS.launchAtLogin }" :aria-checked="appS.launchAtLogin" aria-labelledby="set-lbl-login" :disabled="!appS.loginItemSupported || busy" @click="saveApp({ launchAtLogin: !appS.launchAtLogin })">
                    <span class="track" aria-hidden="true" /><span class="state">{{ appS.launchAtLogin ? '开' : '关' }}</span>
                  </button>
                </dd>
              </div>

              <div class="setting">
                <dt>
                  <span class="lbl"><label id="set-lbl-tray" for="set-tray">{{ appS.platform === 'darwin' ? '启动到菜单栏' : '启动到托盘' }}</label></span>
                  <span class="desc muted">启动时隐藏到{{ appS.platform === 'darwin' ? '菜单栏' : '托盘' }}图标；关闭窗口后 ezlol 仍在后台运行。</span>
                </dt>
                <dd>
                  <button id="set-tray" type="button" role="switch" class="toggle" :class="{ on: appS.startInTray }" :aria-checked="appS.startInTray" aria-labelledby="set-lbl-tray" :disabled="busy" @click="saveApp({ startInTray: !appS.startInTray })">
                    <span class="track" aria-hidden="true" /><span class="state">{{ appS.startInTray ? '开' : '关' }}</span>
                  </button>
                </dd>
              </div>
            </dl>

            <h3>游戏内小窗与快捷键</h3>
            <p class="muted lead">
              小窗总在最上层，点击会穿过它、不会抢游戏焦点，截屏时也看不到它（不影响识别）。
              全屏模式下能不能盖在游戏上，取决于 Windows 的「全屏优化」：请在英雄联盟游戏程序
              <code>Game\League of Legends.exe</code> 上右键 → 属性 → 兼容性，确认<b>没有</b>勾选「禁用全屏优化」。
              如果全屏下还是看不到小窗，就只能改用「无边框」模式（看起来和全屏一样）。
            </p>
            <dl class="rows">
              <div class="setting">
                <dt>
                  <span class="lbl"><label id="set-lbl-ov" for="set-ov">显示小窗</label></span>
                  <span class="desc muted">出现强化选择时自动弹出；选完显示记录的强化和下一件装备，十几秒后自动隐藏。</span>
                </dt>
                <dd>
                  <button id="set-ov" type="button" role="switch" class="toggle" :class="{ on: appS.overlay }" :aria-checked="appS.overlay" aria-labelledby="set-lbl-ov" :disabled="busy" @click="saveApp({ overlay: !appS.overlay })">
                    <span class="track" aria-hidden="true" /><span class="state">{{ appS.overlay ? '开' : '关' }}</span>
                  </button>
                </dd>
              </div>
              <div class="setting">
                <dt>
                  <span class="lbl"><label for="set-ovpos">小窗位置</label></span>
                  <span class="desc muted">放在不挡强化卡片的位置（强化卡片在屏幕中间）。</span>
                </dt>
                <dd>
                  <select id="set-ovpos" :value="appS.overlayPos" @change="saveApp({ overlayPos: ($event.target as HTMLSelectElement).value as OverlayPos })">
                    <option v-for="p in POSITIONS" :key="p.id" :value="p.id">{{ p.label }}</option>
                  </select>
                  <button type="button" @click="previewOverlay">预览小窗</button>
                </dd>
              </div>
              <div v-for="r in HOTKEY_ROWS" :key="r.key" class="setting">
                <dt>
                  <span class="lbl">{{ r.label }}</span>
                  <span v-if="appS.hotkeyErrors?.includes(appS.hotkeys[r.key])" class="desc warn">这个组合键被别的程序占用了，换一个。</span>
                  <span v-else class="desc muted">当前：{{ prettyKey(appS.hotkeys[r.key]) }}</span>
                </dt>
                <dd>
                  <input v-model="hkDraft[r.key]" class="short" :placeholder="prettyKey(appS.hotkeys[r.key])" spellcheck="false" autocomplete="off" @keydown.enter.prevent="saveHotkey(r.key)" />
                  <button type="button" :disabled="!hkDraft[r.key]" @click="saveHotkey(r.key)">改</button>
                </dd>
              </div>
            </dl>
          </template>
        </template>

        <div v-else-if="loadError" class="empty">
          <span class="desc warn">读取设置失败：{{ loadError }}</span>
          <button type="button" @click="load">重试</button>
        </div>
        <p v-else class="muted lead">正在读取设置…</p>
      </div>
    </section>
  </div>
</template>

<style scoped>
/*
 * Row layout only. Colours, fonts, borders and control looks all come from the
 * global .panel / .toggle / .badge / button / input rules in style.css.
 * One spacing scale (--sp-*) and one control height (--ctl-h) drive everything.
 */
.backdrop { position: fixed; inset: 0; z-index: 100; display: grid; place-items: center; background: rgba(0, 0, 0, .6); }

.modal {
  --sp-1: 4px;
  --sp-2: 8px;
  --sp-3: 12px;
  --sp-4: 16px;
  --ctl-h: 36px;
  --pad: 18px; /* matches .panel padding so the body can bleed to the frame edge */
  display: flex;
  flex-direction: column;
  width: min(720px, 94vw);
  max-height: 85vh;
  outline: none;
}

/* Header: title · version · transient status · close, on one line */
.modal h2 { display: flex; align-items: center; gap: var(--sp-3); flex: none; }
.modal h2 .ver,
.modal h2 .status { font-family: var(--serif); font-size: 12px; font-weight: 400; letter-spacing: 0; text-transform: none; color: var(--muted); }
.modal h2 .status { margin-left: auto; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.modal h2 .status.ok { color: var(--hextech-2); }
.modal h2 .status.err { color: var(--red); }
.modal h2 .x { flex: none; width: var(--ctl-h); height: var(--ctl-h); padding: 0; display: inline-grid; place-items: center; font-size: 18px; line-height: 1; }

/* Body scrolls inside the frame, bleeding to the side and bottom edges so the scrollbar runs the full panel height */
.body { flex: 1 1 auto; min-height: 0; overflow-y: auto; margin: 0 calc(var(--pad) * -1) calc(var(--pad) * -1); padding: 0 var(--pad) var(--pad); }
.body h3 { margin: var(--sp-4) 0 var(--sp-1); }
.body h3:first-child { margin-top: var(--sp-1); }
.lead { margin: 0 0 var(--sp-1); line-height: 1.45; }
.empty { display: flex; align-items: center; justify-content: space-between; gap: var(--sp-4); padding: var(--sp-2) 0; }

/* Definition-list rows: [label + description | control] with hairline dividers */
.rows { margin: 0; }
.setting {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--sp-4);
  padding: var(--sp-2) 0;
  border-top: 1px solid var(--line);
}
.setting:first-child { border-top: 0; }
/* The key row is the one with a real text input: give its controls a fluid share instead of a fixed width */
.setting.wide { grid-template-columns: minmax(0, 1fr) minmax(0, 55%); }
.setting dt { min-width: 0; display: grid; gap: 2px; }
.setting dd { margin: 0; min-width: 0; display: flex; align-items: center; justify-content: flex-end; gap: var(--sp-2); }
.lbl { display: inline-flex; align-items: center; gap: var(--sp-2); font-size: 13px; font-weight: 600; color: var(--text); white-space: nowrap; }
.lbl label { cursor: pointer; }
.lbl .hint { font-family: ui-monospace, Menlo, monospace; text-transform: none; letter-spacing: 0; }
.desc { display: block; font-size: 12px; line-height: 1.4; overflow-wrap: anywhere; }
.desc.ok { color: var(--hextech-2); }
.desc.warn { color: var(--amber); }

/* Every control shares one height so each row reads as a single line */
.setting input { height: var(--ctl-h); padding: 0 var(--sp-3); }
.setting .short { flex: none; width: 96px; }
.setting .key { flex: 1 1 120px; min-width: 0; width: auto; }
.body button { flex: none; height: var(--ctl-h); padding: 0 var(--sp-3); display: inline-flex; align-items: center; white-space: nowrap; }
.body button:focus-visible,
.modal h2 .x:focus-visible { outline: 1px solid var(--gold); outline-offset: 1px; }

/* Switch: a real button (tab-focusable, Space/Enter, native :disabled) wearing the global .toggle track.
   The generic button chrome is stripped so only the .toggle look remains. */
.setting .toggle { padding: 0 var(--sp-2); min-height: 0; background: none; box-shadow: none; border-color: transparent; color: var(--muted); }
.setting .toggle.on { color: var(--gold-bright); }
.setting .toggle:hover:not(:disabled) { border-color: var(--gold-deep); background: var(--gold-soft); box-shadow: none; }
.setting .toggle:disabled { cursor: not-allowed; }
.setting .toggle .state { min-width: 2.6em; text-align: left; }

/* Narrow windows: stack the control under its label rather than squeeze either */
@media (max-width: 640px) {
  .setting,
  .setting.wide { grid-template-columns: 1fr; }
  .setting dd { justify-content: flex-start; }
}
</style>

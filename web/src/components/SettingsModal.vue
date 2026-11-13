<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { api } from '../api'
import type { Pitfall, SettingsView, UpdateInfo } from '../types'
import { DEFAULT_HOTKEYS, hotkeyFromEvent, prettyHotkey } from '../api'
import type { AppSettings, Hotkeys } from '../api'
import * as sound from '../sound'
import { theme, THEMES } from '../theme'

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
async function saveApp(patch: Partial<Pick<AppSettings, 'launchAtLogin' | 'startInTray' | 'voice' | 'hotkeys'>>) {
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
const HOTKEY_ROWS: { key: keyof Hotkeys; label: string; desc: string }[] = [
  { key: 'scan', label: '立即识别强化', desc: '强化选择界面打开后按一下：截屏识别，推荐显示在这个窗口里。只有按它时才截屏。' },
  { key: 'pick1', label: '选了第 1 个', desc: '选完按：记下你选的是左边第 1 个，出装按它推荐（不按就记为推荐的那个）。' },
  { key: 'pick2', label: '选了第 2 个', desc: '同上，中间那个。' },
  { key: 'pick3', label: '选了第 3 个', desc: '同上，右边那个。' },
]
const prettyKey = prettyHotkey
// Recording a hotkey: click the button, press the combination. The global
// hotkeys are let go meanwhile so an already-registered one reaches us.
const recording = ref<keyof Hotkeys | null>(null)
const recHint = ref('')
async function startRecord(k: keyof Hotkeys) {
  recording.value = k
  recHint.value = '按下新的组合键或鼠标侧键…（Esc 取消）'
  await window.ezlol?.suspendHotkeys?.(true)
}
async function stopRecord() {
  if (!recording.value) return
  recording.value = null
  recHint.value = ''
  const v = await window.ezlol?.suspendHotkeys?.(false)
  if (v) appS.value = v
}
function onRecordKey(e: KeyboardEvent, k: keyof Hotkeys) {
  if (recording.value !== k) return
  e.preventDefault()
  e.stopPropagation()
  if (e.key === 'Escape' && !e.ctrlKey && !e.altKey && !e.shiftKey && !e.metaKey) {
    void stopRecord()
    return
  }
  const r = hotkeyFromEvent(e)
  if (r.partial) return
  if (r.error) {
    recHint.value = r.error
    return
  }
  useRecorded(k, r.accel!)
}
// Mouse side buttons (back = Mouse4, forward = Mouse5), alone or with modifiers.
function onRecordMouse(e: MouseEvent, k: keyof Hotkeys) {
  if (recording.value !== k || (e.button !== 3 && e.button !== 4)) return
  e.preventDefault()
  e.stopPropagation()
  const mods: string[] = []
  if (e.ctrlKey) mods.push('CommandOrControl')
  if (e.altKey) mods.push('Alt')
  if (e.shiftKey) mods.push('Shift')
  useRecorded(k, [...mods, e.button === 3 ? 'Mouse4' : 'Mouse5'].join('+'))
}
function useRecorded(k: keyof Hotkeys, accel: string) {
  const other = HOTKEY_ROWS.find((row) => row.key !== k && appS.value?.hotkeys[row.key] === accel)
  if (other) {
    recHint.value = `${prettyKey(accel)} 已经用在「${other.label}」上了，换一个`
    return
  }
  recording.value = null
  recHint.value = ''
  void (async () => {
    await window.ezlol?.suspendHotkeys?.(false)
    if (appS.value) await saveApp({ hotkeys: { ...appS.value.hotkeys, [k]: accel } })
  })()
}
async function resetHotkeys() {
  if (!window.ezlol?.resetHotkeys) return
  try {
    appS.value = await window.ezlol.resetHotkeys()
    flash('快捷键已恢复默认', 'ok')
  } catch (e) {
    flash(errText(e), 'err')
  }
}
const defaultKeys = Object.values(DEFAULT_HOTKEYS).map(prettyHotkey).join('、')
async function testVoice() {
  await window.ezlol?.testVoice?.()
  flash('正在试听', 'ok')
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
  const t = e instanceof Error ? e.message : String(e)
  return t.replace(/^Error invoking remote method '[^']+': (Error: )?/, '')
}

function flash(text: string, kind: 'ok' | 'err') {
  msg.value = text
  msgKind.value = kind
  clearTimeout(msgTimer)
  msgTimer = setTimeout(() => (msg.value = ''), 2500)
}

// Augments marked as unusable for a champion (坑).
const pits = ref<Pitfall[]>([])
async function loadPits() {
  try {
    pits.value = await api.pitfalls()
  } catch {
    pits.value = []
  }
}
async function unmarkPit(p: Pitfall) {
  try {
    await api.setPitfall(p.champion, p.augment, false)
    await loadPits()
    flash('已取消标记', 'ok')
  } catch (e) {
    flash(errText(e), 'err')
  }
}

async function load() {
  loadError.value = ''
  void loadPits()
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

const dsKey = ref('')
async function saveDsKey() {
  if (!dsKey.value) return
  if (await save({ deepseekKey: dsKey.value })) dsKey.value = ''
}
const AI_TOGGLES: { key: 'aiReview' | 'aiResearch' | 'aiText'; label: string; desc: string }[] = [
  { key: 'aiReview', label: 'A. 选强化时 AI 复核', desc: '识别到强化后，把你的英雄、装备、双方阵容和胜率数据发给 AI，几秒后给出它的看法（同意或建议改选）。每次不到 0.1 美分。' },
  { key: 'aiResearch', label: 'B. 联网整理英雄攻略', desc: '进游戏后 AI 联网搜索这个英雄的乱斗玩法，结合胜率数据写一份攻略，A 复核时也会参考。每个英雄每个版本一次，不到 1 美分。' },
  { key: 'aiText', label: 'C. 识别不准时让 AI 根据文字推断', desc: '屏幕读到了文字但对不上强化名（错字、漏字）时，把识别出的文字发给 AI 推断是哪几个强化。只发文字，不发截图。每次不到 0.1 美分。' },
]
const ghToken = ref('')
const uploading = ref(false)
async function saveToken() {
  if (!ghToken.value) return
  if (await save({ githubToken: ghToken.value })) ghToken.value = ''
}
async function uploadNow() {
  uploading.value = true
  try {
    const r = await api.uploadLogs()
    flash(`已上传 ${r.uploaded} 个文件`, 'ok')
  } catch (e) {
    flash(errText(e), 'err')
  } finally {
    uploading.value = false
    try {
      s.value = await api.settings()
    } catch {
      /* ignore */
    }
  }
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
  void stopRecord()
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
                <span v-if="s.ocrAvailable" class="desc muted">强化选择界面打开后按「立即识别强化」快捷键（默认 Ctrl+Alt+S），读出屏幕上的选项并告诉你选哪个。只在按键时截屏，平时不截。</span>
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
            <div class="setting pits">
              <dt>
                <span class="lbl">避坑：英雄用不了的强化</span>
                <span class="desc muted">
                  有些强化某些英雄触发不了（比如莎弥拉拿「弱点」）。识别时这些强化会标红「坑」并排到最后：你在强化卡片上点过「标记为坑」的，加上内置的；
                  另外，数据显示「这个强化平均是加分、这个英雄用却减分」的会标「可能是坑」，AI 攻略里「尽量别拿」的标「AI：别拿」。
                </span>
                <ul v-if="pits.length" class="pitlist">
                  <li v-for="p in pits" :key="p.champion + '-' + p.augment">
                    <img v-if="p.icon" :src="p.icon" alt="" />
                    <b>{{ p.championName || p.champion }}</b> · {{ p.augmentName || p.augment }}
                    <span v-if="p.note" class="muted">（{{ p.note }}）</span>
                    <button type="button" class="sm" @click="unmarkPit(p)">取消</button>
                  </li>
                </ul>
                <span v-else class="desc muted">还没有标记。</span>
              </dt>
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

          <!-- AI second opinion (DeepSeek) -->
          <h3>AI 辅助（DeepSeek）</h3>
          <p class="muted lead">
            用 DeepSeek 给强化推荐做第二意见，胜率数据仍然是主推荐。需要你自己的 API key：在
            <a href="https://platform.deepseek.com/api_keys" target="_blank" rel="noreferrer">DeepSeek 开放平台</a>
            充值后新建一个 key，粘贴到下面。key 只存在本机，只发给 api.deepseek.com。
          </p>
          <dl class="rows">
            <div class="setting wide">
              <dt>
                <span class="lbl">
                  <label for="set-dskey">DeepSeek API key</label>
                  <span class="badge" :class="{ riot: s.deepseekKeySet }">{{ s.deepseekKeySet ? `已设置 ${s.deepseekKeyHint}` : '未设置' }}</span>
                </span>
              </dt>
              <dd>
                <input id="set-dskey" v-model="dsKey" type="password" class="key" placeholder="sk-…" autocomplete="off" spellcheck="false" @keydown.enter.prevent="saveDsKey" />
                <button type="button" class="primary" :disabled="busy || !dsKey" @click="saveDsKey">保存</button>
                <button type="button" class="danger" :disabled="busy || !s.deepseekKeySet" @click="save({ deepseekKey: '' })">清除</button>
              </dd>
            </div>
            <div v-for="t in AI_TOGGLES" :key="t.key" class="setting">
              <dt>
                <span class="lbl"><label :id="'set-lbl-' + t.key" :for="'set-' + t.key">{{ t.label }}</label></span>
                <span class="desc muted">{{ t.desc }}</span>
              </dt>
              <dd>
                <button :id="'set-' + t.key" type="button" role="switch" class="toggle" :class="{ on: s[t.key] }" :aria-checked="s[t.key]" :aria-labelledby="'set-lbl-' + t.key" :disabled="!s.deepseekKeySet" @click="save({ [t.key]: !s[t.key] })">
                  <span class="track" aria-hidden="true" /><span class="state">{{ s[t.key] ? '开' : '关' }}</span>
                </button>
              </dd>
            </div>
            <div class="setting">
              <dt>
                <span class="lbl">本月花费</span>
                <span class="desc muted">按 DeepSeek 官方价格估算（工作日 9–12 点、14–18 点（UTC+8）价格翻倍），以平台账单为准。</span>
              </dt>
              <dd class="usage">
                <b>${{ s.aiUsage.usd.toFixed(4) }}</b>
                <span class="muted">{{ s.aiUsage.calls }} 次对话 · {{ s.aiUsage.searches }} 次搜索 · {{ (s.aiUsage.tokens / 1000).toFixed(1) }}k tokens</span>
              </dd>
            </div>
          </dl>

          <!-- Testing aid: upload logs to GitHub (remove after testing) -->
          <h3>上传日志到 GitHub（测试用）</h3>
          <p class="muted lead">
            把日志和最近 15 张截图上传到仓库 <code>{{ s.uploadRepo }}</code> 的 <code>test-logs</code> 分支，方便直接查看测试结果（不会触发打包）。
            需要一个只对这个仓库有写权限的令牌：打开
            <a href="https://github.com/settings/personal-access-tokens/new" target="_blank" rel="noreferrer">GitHub 新建令牌</a>，
            「Repository access」选 Only select repositories → ezlol，「Permissions → Contents」选 Read and write，生成后粘贴到下面。
            令牌只存在本机，只发给 GitHub。测试结束后可以在 GitHub 上删掉这个令牌。
          </p>
          <dl class="rows">
            <div class="setting wide">
              <dt>
                <span class="lbl">
                  <label for="set-ghtok">GitHub 令牌</label>
                  <span class="badge" :class="{ riot: s.githubTokenSet }">{{ s.githubTokenSet ? `已设置 ${s.githubTokenHint}` : '未设置' }}</span>
                </span>
              </dt>
              <dd>
                <input id="set-ghtok" v-model="ghToken" type="password" class="key" placeholder="github_pat_…" autocomplete="off" spellcheck="false" @keydown.enter.prevent="saveToken" />
                <button type="button" class="primary" :disabled="busy || !ghToken" @click="saveToken">保存</button>
                <button type="button" class="danger" :disabled="busy || !s.githubTokenSet" @click="save({ githubToken: '' })">清除</button>
              </dd>
            </div>
            <div class="setting">
              <dt>
                <span class="lbl"><label id="set-lbl-autoup" for="set-autoup">每局结束自动上传</label></span>
                <span class="desc muted">游戏结束几秒后自动上传，不用手动点。</span>
              </dt>
              <dd>
                <button id="set-autoup" type="button" role="switch" class="toggle" :class="{ on: s.autoUpload }" :aria-checked="s.autoUpload" aria-labelledby="set-lbl-autoup" :disabled="!s.githubTokenSet" @click="save({ autoUpload: !s.autoUpload })">
                  <span class="track" aria-hidden="true" /><span class="state">{{ s.autoUpload ? '开' : '关' }}</span>
                </button>
              </dd>
            </div>
            <div class="setting">
              <dt>
                <span class="lbl">上次上传</span>
                <span v-if="s.lastUpload?.error" class="desc warn">{{ s.lastUpload.at }}：{{ s.lastUpload.error }}</span>
                <span v-else-if="s.lastUpload" class="desc muted">{{ s.lastUpload.at }}：上传 {{ s.lastUpload.uploaded }} 个，未变化 {{ s.lastUpload.skipped }} 个</span>
                <span v-else class="desc muted">还没上传过</span>
              </dt>
              <dd>
                <button type="button" :disabled="uploading || !s.githubTokenSet" @click="uploadNow">{{ uploading ? '上传中…' : '立即上传' }}</button>
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
            <div class="setting">
              <dt>
                <span class="lbl">外观</span>
                <span class="desc muted">简约：深灰底、圆角，干净好读；海克斯：英雄联盟客户端的金色风格。</span>
              </dt>
              <dd>
                <div class="seg" role="radiogroup" aria-label="外观">
                  <button v-for="t in THEMES" :key="t.id" type="button" role="radio" :aria-checked="theme === t.id" :class="{ on: theme === t.id }" @click="theme = t.id">{{ t.label }}</button>
                </div>
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

            <h3>游戏内快捷键</h3>
            <p class="muted lead">
              游戏是独占全屏，ezlol 不往游戏上叠窗口：按快捷键识别后，切出来（Alt+Tab）在这个窗口看推荐；也可以打开语音，不用切出来。
              ezlol 直接读键盘和鼠标的按键状态（和 OBS 的快捷键一样），全屏游戏里也能用；如果英雄联盟是以管理员身份运行的，ezlol 也要以管理员身份运行。
            </p>
            <dl class="rows">
              <div class="setting">
                <dt>
                  <span class="lbl"><label id="set-lbl-voice" for="set-voice">语音播报（可选）</label></span>
                  <span class="desc muted">识别后念出推荐（「推荐中间的，大法师」），记下强化后念下一件装备。用 Windows 自带的中文语音，不联网。</span>
                </dt>
                <dd>
                  <button type="button" @click="testVoice">试听</button>
                  <button id="set-voice" type="button" role="switch" class="toggle" :class="{ on: appS.voice }" :aria-checked="appS.voice" aria-labelledby="set-lbl-voice" :disabled="busy" @click="saveApp({ voice: !appS.voice })">
                    <span class="track" aria-hidden="true" /><span class="state">{{ appS.voice ? '开' : '关' }}</span>
                  </button>
                </dd>
              </div>
              <div v-for="r in HOTKEY_ROWS" :key="r.key" class="setting">
                <dt>
                  <span class="lbl">{{ r.label }}</span>
                  <span v-if="recording === r.key" class="desc ok">{{ recHint }}</span>
                  <span v-else-if="appS.hotkeyErrors?.includes(appS.hotkeys[r.key])" class="desc warn">{{ prettyKey(appS.hotkeys[r.key]) }} 也被别的程序（QQ、微信、显卡软件等）占用了：ezlol 照样能读到，但那个程序也会响应，最好换一个。</span>
                  <span v-else-if="r.desc" class="desc muted">{{ r.desc }}</span>
                </dt>
                <dd>
                  <button
                    type="button"
                    class="hk"
                    :class="{ rec: recording === r.key, bad: appS.hotkeyErrors?.includes(appS.hotkeys[r.key]) }"
                    :title="recording === r.key ? '按下新的组合键，Esc 取消' : '点一下，然后按下新的组合键'"
                    @click="recording === r.key ? stopRecord() : startRecord(r.key)"
                    @keydown="onRecordKey($event, r.key)"
                    @mousedown="onRecordMouse($event, r.key)"
                    @mouseup.prevent
                    @auxclick.prevent
                    @blur="recording === r.key && stopRecord()"
                  >
                    {{ recording === r.key ? '请按键…' : prettyKey(appS.hotkeys[r.key]) }}
                  </button>
                </dd>
              </div>
              <div class="setting">
                <dt>
                  <span class="lbl">恢复默认快捷键</span>
                  <span class="desc muted">
                    默认 {{ defaultKeys }}。不用 Ctrl+Shift：中文 Windows 里它会切换输入法，游戏里可能卡住按键。
                    也可以设成 F7、F8 这样的单键（笔记本可能要同时按 Fn），或者鼠标侧键（录入时按一下侧键）。
                  </span>
                </dt>
                <dd>
                  <button type="button" :disabled="busy" @click="resetHotkeys">恢复默认</button>
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
.setting.pits { grid-template-columns: 1fr; }
.pitlist { list-style: none; margin: 6px 0 0; padding: 0; display: grid; gap: 4px; font-size: 12px; }
.pitlist li { display: flex; align-items: center; gap: 6px; }
.pitlist img { width: 20px; height: 20px; border-radius: 4px; }
.body .pitlist button.sm { height: 22px; padding: 0 8px; font-size: 11px; margin-left: auto; }
.seg { display: inline-flex; padding: 3px; gap: 3px; border: 1px solid var(--border); border-radius: var(--radius-sm); background: var(--surface-input); }
.body .seg button { height: calc(var(--ctl-h) - 8px); border: 0; background: transparent; box-shadow: none; color: var(--muted); }
.body .seg button.on { background: var(--panel-2); color: var(--text); box-shadow: 0 0 0 1px var(--border); }
.body button.hk { min-width: 132px; justify-content: center; font-family: ui-monospace, Menlo, monospace; }
.body button.hk.rec { border-color: var(--hextech-2); color: var(--hextech-2); }
.body button.hk.bad { border-color: var(--amber); color: var(--amber); }
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
.usage { display: flex; flex-direction: column; align-items: flex-end; gap: 2px; font-size: 12px; }
.usage b { font-size: 15px; }
</style>

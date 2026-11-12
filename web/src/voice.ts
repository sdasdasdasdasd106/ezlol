// Spoken recommendations for exclusive-fullscreen play, where no window can
// be drawn over the game. Uses the system's text-to-speech voices (on a
// Chinese Windows: "Microsoft Huihui"); nothing leaves the computer.

let enabled = true
let zh: SpeechSynthesisVoice | null = null
const supported = typeof window !== 'undefined' && 'speechSynthesis' in window

function pickVoice() {
  const vs = speechSynthesis.getVoices()
  zh = vs.find((v) => /^zh[-_](CN|Hans)/i.test(v.lang)) ?? vs.find((v) => v.lang.toLowerCase().startsWith('zh')) ?? null
}
if (supported) {
  pickVoice()
  speechSynthesis.addEventListener('voiceschanged', pickVoice)
}

export function setVoice(on: boolean) {
  enabled = on
  if (!on && supported) speechSynthesis.cancel()
}

/**
 * Speaks text. interrupt = cut off whatever is being said (a new augment
 * choice matters more than the rest); otherwise it is queued. force = speak
 * even with the voice turned off (the test button).
 */
export function say(text: string, opts: { interrupt?: boolean; force?: boolean } = {}) {
  if (!supported || !text || (!enabled && !opts.force)) return
  if (opts.interrupt) speechSynthesis.cancel()
  const u = new SpeechSynthesisUtterance(text)
  u.lang = zh?.lang ?? 'zh-CN'
  if (zh) u.voice = zh
  u.rate = 1.15
  speechSynthesis.speak(u)
}

/** "左边 / 中间 / 右边" for a card's place on screen (0-based, n cards). */
export function place(i: number, n: number): string {
  if (n === 2) return i === 0 ? '左边' : '右边'
  if (n === 3) return ['左边', '中间', '右边'][i] ?? `第${i + 1}个`
  return `第${i + 1}个`
}

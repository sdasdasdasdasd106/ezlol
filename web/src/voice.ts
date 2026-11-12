// Optional spoken recommendations, so a player in exclusive fullscreen does
// not have to switch out. Uses the system's text-to-speech voices (on a
// Chinese Windows: "Microsoft Huihui"); nothing leaves the computer.
import { watch, type Ref } from 'vue'
import type { AugmentOffer, Game } from './types'

let enabled = false // off unless turned on in Settings
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

/**
 * Speaks what the in-game panel shows, for players who stay in the game:
 * a new recommendation, an AI review that disagrees, and a recorded pick with
 * the next item for the augments taken. Nothing is said for the state found
 * on the first look at a game (ezlol started mid-game).
 */
export function useGameVoice(game: Ref<Game | null>, nextItem: (champ: number, augs: number[], owned: number[]) => Promise<string>) {
  const cardsOf = (o: AugmentOffer) => {
    const by = new Map((o.offered ?? []).map((c) => [c.id, c]))
    return (o.screen?.length ? o.screen : (o.offered ?? []).map((c) => c.id)).map((id) => by.get(id)).filter(Boolean) as { id: number; name: string }[]
  }
  const active = () => (game.value?.offer?.active && game.value.offer.offered?.length ? game.value.offer : null)

  watch(
    () => {
      const o = active()
      return o ? `${o.scannedAt ?? ''}|${o.screen?.join(',') ?? ''}` : ''
    },
    (k) => {
      const o = active()
      if (!k || !o?.offered) return
      const cards = cardsOf(o)
      const at = (id: number) => place(cards.findIndex((c) => c.id === id), cards.length)
      const [best, second] = o.offered
      let t = `${o.via === 'ai' ? 'AI 推断，' : ''}推荐${at(best.id)}的，${best.name}。`
      if (second && o.note?.includes('差距很小')) t += `和${at(second.id)}的${second.name}差不多。`
      say(t, { interrupt: true })
    },
  )
  let spokenAI = ''
  watch(
    () => {
      const o = active()
      return o?.ai?.status === 'done' && !o.ai.agree ? `${o.scannedAt ?? ''}|${o.ai.pick}` : ''
    },
    (k) => {
      const o = active()
      if (!k || k === spokenAI || !o?.ai?.pick) return
      spokenAI = k
      const cards = cardsOf(o)
      const i = cards.findIndex((c) => c.name === o.ai!.pick)
      say(`AI 建议改选${i >= 0 ? `${place(i, cards.length)}的，` : ''}${o.ai.pick}。`)
    },
  )
  let picksKey: string | null = null
  watch(
    () => (game.value?.inGame ? (game.value.picks ?? []).map((p) => `${p.stage}:${p.augmentId}`).join(',') : null),
    async (k) => {
      if (k === null) {
        picksKey = null
        return
      }
      const first = picksKey === null
      const changed = k !== picksKey
      picksKey = k
      const g = game.value
      const ps = g?.picks ?? []
      if (first || !changed || !ps.length || !g?.me) return
      const last = ps[ps.length - 1]
      let next = ''
      try {
        next = await nextItem(g.me.champion.id, ps.map((p) => p.augmentId), g.me.items ?? [])
      } catch {
        next = ''
      }
      say(`记下了${last.name}。${next ? `下一件，${next}。` : ''}`)
    },
  )
}

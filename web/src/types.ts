export interface Status {
  connected: boolean
  phase: string
  summoner: string
  gameVersion: string
  patch: string
  queueId: number
  queueName: string
  mapId: number
  gameMode: string
  pickedChampion: number
  pickedPosition: string
  error?: string
  updatedAt: string
}

export interface Champion {
  id: number
  key: string
  name: string
  title: string
  tags: string[]
  image: string
  attack: number
  magic: number
}

export interface Count {
  games: number
  wins: number
}

export interface ItemRef {
  id: number
  name: string
  image: string
}
export interface ItemSet extends Count {
  items: ItemRef[]
  avgPlace?: number
}
export interface Build {
  champion: Champion
  patch: string
  mode: string
  source: string // aramkit | opgg | none
  total: Count
  starting: ItemSet[] | null
  core: ItemSet[] | null
  coreTags?: string[] | null
  boots: ItemSet[] | null
  late: ItemSet[] | null
  tier?: string
  rank?: number
  pickRate?: number
  augments?: Augment[]
  augScope?: string
  augSource?: string
  notes: string[] | null
}
export interface Augment {
  id: number
  name: string
  desc: string
  rarity: string
  icon: string
  tier: number
  winRate: number
  pickRate: number
  games: number
  scope: string
}

export interface OfferCard extends Augment {
  score: number
  stageWinRate: number
  stageGames: number
  lift: number
  pairWinRate?: number
  pairGames?: number
  pairWith?: string
  pairGlobal?: boolean
  fit?: number
  fitOk: boolean
  core?: AugCore
  /** 坑: the champion cannot make use of it. mark = yours or built in, data = the statistics suggest it, ai = the AI strategy card */
  pitfall?: 'mark' | 'data' | 'ai'
  reasons: string[]
}

/** GET /api/champions/{key}/pitfalls: augments to avoid on one champion. */
export interface ChampPitfall {
  augment: number
  kind: 'mark' | 'data'
  note: string
}

/** An augment a champion cannot make use of (GET /api/pitfalls). */
export interface Pitfall {
  champion: number
  augment: number
  note?: string
  builtin?: boolean
  championName: string
  augmentName: string
  icon?: string
}

export interface AugmentOffer {
  active: boolean
  pending: boolean
  waiting?: boolean
  level: number
  stage: number
  offered: OfferCard[] | null
  screen?: number[] // augment ids left to right, as on screen
  chosen?: number // slot (1..3) marked with a hotkey
  scannedAt?: string // when it was read (each scan hotkey press)
  best?: string
  why?: string
  note?: string
  source?: string
  via?: 'ocr' | 'ai'
  ai?: AIReview
}

/** The AI's (DeepSeek) second opinion on an augment choice. */
export interface AIReview {
  status: 'pending' | 'done' | 'error'
  pick?: string
  reason?: string
  agree: boolean
  confidence?: string
  error?: string
  seconds?: number
}

export interface StrategyAug {
  name: string
  why: string
}

/** The AI's pre-game strategy card for a champion (web research). */
export interface Research {
  status: 'none' | 'pending' | 'done' | 'error'
  strategy?: { summary: string; prefer: StrategyAug[] | null; avoid: StrategyAug[] | null; build: string[] | null; sources: string[] | null; at: string }
  error?: string
}

/** An augment taken this game; assumed = the recommended one was guessed. */
export interface AugPick {
  stage: number
  level: number
  augmentId: number
  name: string
  icon: string
  rarity: string
  assumed: boolean
  offered: Augment[]
}

export interface AugItem {
  item: ItemRef
  winRate: number
  games: number
  lift: number
  lowSample?: boolean
}

export interface StyleStat {
  style: string
  name: string
  winRate: number
  games: number
  share: number
}

export interface Direction {
  popular: StyleStat
  best: StyleStat
  yours?: StyleStat
  conflict: boolean
  note: string
}

export interface AugCore {
  style: string
  items: ItemRef[]
  boots?: ItemRef
  winRate: number
  games: number
  pickRate: number
  owned: number
  augment: number
}

/** GET /api/champions/{key}/augment-build */
export interface AugBuildResponse {
  plan: {
    augments: number[]
    games: number
    slot: number
    slotBase: number
    basis: 'path' | 'partial' | 'all' | ''
    next: AugItem[] | null
    boots: AugItem[] | null
    cores: AugCore[] | null
    direction: Direction | null
    source: string
  }
  augments: Augment[]
  championWinRate: number
  version: string
}

export interface Mastery {
  championId: number
  championLevel: number
  championPoints: number
  highestGrade: string
}
export interface PlayRecord {
  games: number
  wins: number
  kills: number
  deaths: number
  assists: number
}
export interface ChampionInfo {
  roles: string[]
  tacticalInfo: { style: number; difficulty: number; damageType: string; attackType: string }
  playstyleInfo: { damage: number; durability: number; crowdControl: number; mobility: number; utility: number }
}
export interface SettingsView {
  ocr: boolean
  ocrAvailable: boolean
  ocrLang: string
  updateCheck: boolean
  testLog: boolean
  logShots: boolean
  logDir: string
  // testing aid: log upload to GitHub
  githubTokenSet: boolean
  githubTokenHint: string
  autoUpload: boolean
  uploadRepo: string
  lastUpload?: { at: string; why: string; uploaded: number; skipped: number; url: string; error?: string } | null
  deepseekKeySet: boolean
  deepseekKeyHint: string
  aiReview: boolean
  aiResearch: boolean
  aiText: boolean
  aiUsage: { month: string; calls: number; searches: number; tokens: number; usd: number }
  version: string
}
export interface UpdateInfo {
  current: string
  latest: string
  hasUpdate: boolean
  url: string
  notes: string
  checkedAt: string
  error?: string
}

/** GET /api/game: your champion in the running game and the augment offer on screen. */
export interface Game {
  inGame: boolean
  gameTime: number
  mapId: number
  mode: string // aram for ARAM / Mayhem
  me?: { name: string; champion: Champion; team: string; isDead: boolean; level: number; items: number[] | null }
  patch: string
  picks: AugPick[] | null
  offer?: AugmentOffer
  ocr: string
  ocrLang: string
  research?: Research
  needAdmin?: boolean // the game runs as administrator and ezlol does not: hotkeys cannot reach it
}

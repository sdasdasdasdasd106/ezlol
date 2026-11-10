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
export interface RuneRef {
  id: number
  name: string
  icon: string
  desc?: string
}
export interface StyleRef {
  id: number
  name: string
  icon: string
}
export interface RunePage extends Count {
  primary: StyleRef
  secondary: StyleRef
  perks: RuneRef[]
  shards: RuneRef[]
  source: string
}
export interface SpellRef {
  id: number
  name: string
  image: string
}
export interface SpellPair extends Count {
  spells: SpellRef[]
}
export interface Skills extends Count {
  order: string
}
export interface RoleCount extends Count {
  role: string
}

export interface Build {
  champion: Champion
  patch: string
  role: string
  mode: string
  roles: RoleCount[] | null
  source: string
  total: Count
  starting: ItemSet[] | null
  core: ItemSet[] | null
  boots: ItemSet[] | null
  late: ItemSet[] | null
  items: ItemSet[] | null
  runes: RunePage[] | null
  spells: SpellPair[] | null
  skillOrder: Skills[] | null
  skillStart: Skills[] | null
  skillPath?: string[]
  tier?: number
  rank?: number
  pickRate?: number
  augments?: Augment[]
  augScope?: string
  prismatic?: ItemSet[] | null
  synergies?: Synergy[] | null
  avgPlace?: number
  top1?: number
  notes: string[] | null
}
export interface Synergy {
  champion: Champion
  games: number
  winRate: number
  avgPlace: number
  top1: number
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
  avgPlace?: number
  top1?: number
}

export interface Progress {
  running: boolean
  phase: string
  queue: number
  players: number
  matchesDone: number
  target: number
  requests: number
  errors: number
  lastError?: string
  startedAt?: string
  finishedAt?: string
}

export interface BuildsStatus {
  hasKey: boolean
  platform: string
  progress: Progress
  patches: { patch: string; matches: number; champions: number }[] | null
}

export interface AugmentOffer {
  active: boolean
  pending: boolean
  level: number
  offered: Augment[] | null
  best?: string
  why?: string
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
  riotApiKeySet: boolean
  riotApiKeyHint: string
  platform: string
  matchesPerRun: number
  autoCompile: boolean
  ocr: boolean
  ocrAvailable: boolean
  ocrLang: string
  updateCheck: boolean
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
  mode: 'aram' | 'arena' | 'sr' | ''
  me?: { name: string; champion: Champion; team: string; isDead: boolean; level: number }
  offer?: AugmentOffer
  ocr: string
  ocrLang: string
}

import { GridStack, type GridStackWidget } from 'gridstack'
import { reactive, type InjectionKey } from 'vue'

export interface WidgetOpts {
  w?: number
  h?: number
}

export interface Dashboard {
  add(id: string, el: HTMLElement, opts: WidgetOpts): void
  remove(el: HTMLElement): void
  /** ids of widgets currently mounted (reactive) */
  mounted: Set<string>
}

export const DASH_KEY: InjectionKey<Dashboard> = Symbol('dashboard')


/** Screens: idle (lobby/post-game), select (champ select), and in-game per mode. */
export type ModeKey = 'idle' | 'select' | 'game-rift' | 'game-aram' | 'game-arena'

type Pos = { x: number; y: number; w: number; h: number }
const P = (x: number, y: number, w: number, h: number): Pos => ({ x, y, w, h })

/**
 * Built-in arrangements (12-column grid, rows of 40px). The champion card is a
 * fixed bar above the grid, not a box. Boxes that never coexist may share a slot.
 */
const BUILD_ROW: Record<string, Pos> = {
  items: P(0, 0, 3, 12), boots: P(3, 0, 3, 12), runes: P(6, 0, 3, 12), spells: P(9, 0, 3, 12),
  'aug-prismatic': P(0, 12, 3, 9), 'aug-gold': P(3, 12, 3, 9), 'aug-silver': P(6, 12, 3, 9),
  prismatic: P(9, 12, 3, 10), synergies: P(9, 22, 3, 10),
}
export const DEFAULT_LAYOUTS: Record<ModeKey, Record<string, Pos>> = {
  idle: { ...BUILD_ROW, compile: P(0, 21, 3, 6) },
  select: { ...BUILD_ROW },
  'game-rift': { items: P(0, 0, 3, 12), boots: P(3, 0, 3, 12), runes: P(6, 0, 3, 12), spells: P(9, 0, 3, 12) },
  'game-aram': {
    'augment-assist': P(0, 0, 6, 10),
    'aug-prismatic': P(0, 10, 3, 10), 'aug-gold': P(3, 10, 3, 10), 'aug-silver': P(0, 20, 3, 10),
    items: P(6, 0, 3, 12), runes: P(9, 0, 3, 12), spells: P(6, 12, 3, 10), boots: P(9, 12, 3, 12),
  },
  'game-arena': {
    'augment-assist': P(0, 0, 6, 10),
    'aug-prismatic': P(0, 10, 3, 10), 'aug-gold': P(3, 10, 3, 10), 'aug-silver': P(0, 20, 3, 10),
    prismatic: P(6, 0, 3, 10), synergies: P(9, 0, 3, 10), items: P(6, 10, 3, 12), boots: P(9, 10, 3, 12),
    spells: P(3, 20, 3, 10), runes: P(6, 22, 3, 10),
  },
}

/** Every box a screen can show, so edit mode can offer a ghost slot for the ones not on screen yet. */
export interface WidgetSpec {
  id: string
  title: string
  w: number
  h: number
  when?: string
}
const BUILD_WIDGETS: WidgetSpec[] = [
  { id: 'items', title: '出装', w: 3, h: 12 },
  { id: 'boots', title: '鞋子与后期装', w: 3, h: 12 },
  { id: 'runes', title: '符文', w: 3, h: 10 },
  { id: 'spells', title: '召唤师技能与加点', w: 3, h: 10 },
]
const AUG_WIDGETS: WidgetSpec[] = [
  { id: 'aug-prismatic', title: '棱彩强化', w: 3, h: 9 },
  { id: 'aug-gold', title: '金色强化', w: 3, h: 9 },
  { id: 'aug-silver', title: '银色强化', w: 3, h: 9 },
]
const ARENA_BUILD: WidgetSpec[] = [
  { id: 'prismatic', title: '棱彩装备', w: 3, h: 10 },
  { id: 'synergies', title: '最佳队友', w: 3, h: 10 },
]
const ASSIST: WidgetSpec = { id: 'augment-assist', title: '强化推荐', w: 6, h: 10 }
export const CATALOG: Record<ModeKey, WidgetSpec[]> = {
  idle: [
    ...BUILD_WIDGETS,
    ...AUG_WIDGETS.map((w) => ({ ...w, when: '大乱斗 / 斗魂竞技场出装' })),
    ...ARENA_BUILD.map((w) => ({ ...w, when: '斗魂竞技场出装' })),
    { id: 'compile', title: '出装数据统计', w: 3, h: 6 },
  ],
  select: [
    ...BUILD_WIDGETS,
    ...AUG_WIDGETS.map((w) => ({ ...w, when: '大乱斗 / 斗魂竞技场' })),
    ...ARENA_BUILD.map((w) => ({ ...w, when: '斗魂竞技场' })),
  ],
  'game-rift': [...BUILD_WIDGETS],
  'game-aram': [ASSIST, ...BUILD_WIDGETS, ...AUG_WIDGETS.map((w) => ({ ...w, when: '海克斯乱斗' }))],
  'game-arena': [ASSIST, ...BUILD_WIDGETS, ...ARENA_BUILD, ...AUG_WIDGETS],
}

const COLUMNS = 12
const CELL = 40
const MARGIN = 6
const STORE = 'ezlol.layout.v5' // v5: build + augment-only layouts
const PRESET = 'ezlol.layout.preset.v4' // user-saved arrangements, restored by Reset

type Layouts = Record<string, Record<string, Pos>>

/** Natural height of a widget's content (ignoring the box it currently sits in). */
function naturalHeight(el: HTMLElement): number {
  const section = el.firstElementChild as HTMLElement | null
  const inner = section?.querySelector<HTMLElement>('.wcontent')
  if (!section || !inner) return 0
  inner.classList.add('measuring')
  const cs = getComputedStyle(section)
  const h = inner.scrollHeight + parseFloat(cs.paddingTop) + parseFloat(cs.paddingBottom)
  inner.classList.remove('measuring')
  return h
}

/**
 * Grid rows needed for a content height. gridstack rows are CELL px tall with the
 * MARGIN taken from inside each item, so an h-row item shows h*CELL - 2*MARGIN px.
 */
function rowsFor(contentHeight: number): number {
  return Math.min(40, Math.max(2, Math.ceil((contentHeight + 2 * MARGIN + 2) / CELL)))
}

function loadJSON(key: string): Layouts {
  try {
    return JSON.parse(localStorage.getItem(key) ?? '{}')
  } catch {
    return {}
  }
}
function saveJSON(key: string, l: Layouts) {
  try {
    localStorage.setItem(key, JSON.stringify(l))
  } catch {
    /* ignore */
  }
}
const loadLayouts = () => loadJSON(STORE)
const saveLayouts = (l: Layouts) => saveJSON(STORE, l)

/**
 * One grid per screen mode (idle / select / game). Widgets register themselves
 * when they mount; positions come from the saved layout for the current mode or,
 * for a widget never placed by the user, from auto-placement with a size derived
 * from its content height. Layout changes are saved on every drag/resize.
 */
export class DashboardGrid implements Dashboard {
  private grid: GridStack | null = null
  private pending: { id: string; el: HTMLElement; opts: WidgetOpts }[] = []
  private layouts = loadLayouts()
  private presets = loadJSON(PRESET)
  private editing = false
  // Widgets without a saved position keep tracking their content height (data and
  // fonts arrive after mount) until the user has arranged the screen.
  private auto = new Map<HTMLElement, ResizeObserver>()
  readonly mounted = reactive(new Set<string>())

  constructor(private mode: () => ModeKey) {}

  private reloading = false

  attach(container: HTMLElement) {
    this.reloading = true
    this.detach()
    this.grid = GridStack.init(
      // auto: false — never adopt the container's children as 1x1 placeholder nodes;
      // those get shoved around by explicit placement and shove real boxes in turn.
      { column: COLUMNS, cellHeight: CELL, margin: MARGIN, float: true, animate: false, staticGrid: !this.editing, minRow: 1, auto: false, resizable: { handles: 'se,e,s' } },
      container,
    )
    const g = this.grid
    if (!g) return
    g.on('change', () => {
      if (this.editing) this.persist()
    })
    // Place everything: widgets queued while there was no grid, plus any item the
    // new container already held (a remount mounts children before this runs).
    const queued = this.pending.splice(0)
    const seen = new Set<HTMLElement>()
    for (const p of queued) {
      seen.add(p.el)
      this.place(p.id, p.el, p.opts)
    }
    for (const child of Array.from(container.querySelectorAll<HTMLElement>(':scope > .grid-stack-item'))) {
      if (seen.has(child)) continue
      const id = child.getAttribute('gs-id')
      if (!id) continue
      let opts: WidgetOpts = {}
      try {
        opts = JSON.parse(child.dataset.gsOpts ?? '{}')
      } catch {
        /* ignore */
      }
      this.place(id, child, opts)
    }
    this.reloading = false
  }

  detach() {
    for (const ro of this.auto.values()) ro.disconnect()
    this.auto.clear()
    if (this.grid) {
      // Unhook first: destroy() fires change events that would persist the
      // outgoing grid's positions over a layout we just restored.
      this.grid.offAll()
      this.grid.destroy(false)
      this.grid = null
    }
  }

  setEditing(on: boolean) {
    this.editing = on
    this.grid?.setStatic(!on)
  }

  /** Snapshot the current arrangement as this screen's saved layout. */
  savePreset() {
    this.persist(true)
    const cur = this.layouts[this.mode()]
    if (!cur) return
    this.presets[this.mode()] = JSON.parse(JSON.stringify(cur))
    saveJSON(PRESET, this.presets)
  }

  hasPreset(): boolean {
    return !!this.presets[this.mode()]
  }

  /** Drop the saved layout for this screen (Reset then falls back to the built-in default). */
  clearPreset() {
    delete this.presets[this.mode()]
    saveJSON(PRESET, this.presets)
  }

  /** Restore this screen's saved layout, or the built-in default if none was saved. Caller remounts. */
  reset() {
    const p = this.presets[this.mode()]
    if (p) this.layouts[this.mode()] = JSON.parse(JSON.stringify(p))
    else delete this.layouts[this.mode()]
    saveLayouts(this.layouts)
  }

  add(id: string, el: HTMLElement, opts: WidgetOpts) {
    if (el.classList.contains('ghost')) {
      // Ghosts live under their own id in the grid so they never collide with the
      // real box (gridstack would rename a duplicate id to "<id>_1"); persist()
      // maps them back to the real id.
      if (!id.startsWith('ghost:')) id = 'ghost:' + id
      el.setAttribute('gs-id', id)
    } else {
      this.mounted.add(id)
    }
    el.dataset.gsOpts = JSON.stringify(opts)
    // No grid yet, or the grid belongs to a container being replaced (remount):
    // queue until attach() runs on the new container.
    if (!this.grid || !this.grid.el.contains(el)) {
      this.pending.push({ id, el, opts })
      return
    }
    // attach() already placed everything the container held; placing it again
    // would collide with itself and shove the box down by its own height.
    if ((el as HTMLElement & { gridstackNode?: unknown }).gridstackNode) return
    this.place(id, el, opts)
  }

  remove(el: HTMLElement) {
    const id = el.getAttribute('gs-id')
    if (id && !el.classList.contains('ghost')) this.mounted.delete(id)
    this.auto.get(el)?.disconnect()
    this.auto.delete(el)
    this.pending = this.pending.filter((p) => p.el !== el)
    if (this.grid && el.classList.contains('grid-stack-item')) this.grid.removeWidget(el, false)
  }

  private place(id: string, el: HTMLElement, opts: WidgetOpts) {
    if (!this.grid) return
    // GridStack.init adopts any .grid-stack-item already in the container as a 1x1
    // node; drop that node so makeWidget can apply our real size and position.
    if ((el as HTMLElement & { gridstackNode?: unknown }).gridstackNode) this.grid.removeWidget(el, false, false)
    const realID = id.startsWith('ghost:') ? id.slice(6) : id
    const saved = this.layouts[this.mode()]?.[realID] ?? DEFAULT_LAYOUTS[this.mode()]?.[realID]
    const w = Math.min(COLUMNS, Math.max(2, saved?.w ?? opts.w ?? 3))
    const spec: GridStackWidget = { id, w }
    if (saved) {
      spec.x = saved.x
      spec.y = saved.y
      spec.h = saved.h
    } else {
      // Not in the catalogue: size to content and drop into the first free spot.
      spec.h = Math.min(40, opts.h ?? this.measureRows(el, w))
      spec.autoPosition = true
    }
    this.grid.makeWidget(el, spec)
    if (el.classList.contains('ghost')) {
      if (this.editing) this.persist()
      return
    }
    if (!saved) this.track(el, 40)
  }


  /** Grow/shrink an auto-sized widget as its content settles; stops once the user edits. */
  private track(el: HTMLElement, maxRows: number) {
    if (!el.firstElementChild) return
    let last = 0
    const tick = () => {
      if (!this.grid || this.editing) return
      const rows = Math.min(maxRows, rowsFor(naturalHeight(el)))
      if (rows === last) return
      last = rows
      const node = (el as HTMLElement & { gridstackNode?: { h?: number } }).gridstackNode
      if (node && node.h !== rows) this.grid.update(el, { h: rows })
    }
    // Content that was mid-load at mount (fonts, data) settles within a few seconds;
    // poll for that window, then leave the layout alone during play.
    const timer = setInterval(tick, 400)
    const ro = { disconnect: () => clearInterval(timer) } as ResizeObserver
    this.auto.set(el, ro)
    setTimeout(() => {
      clearInterval(timer)
      this.auto.delete(el)
    }, 8000)
  }

  /** Rows needed to show the widget's content without an inner scrollbar, at width w. */
  private measureRows(el: HTMLElement, w: number): number {
    const content = el.firstElementChild as HTMLElement | null
    if (!content || !this.grid) return 6
    const colWidth = this.grid.el.clientWidth / COLUMNS
    const prev = { position: el.style.position, width: el.style.width, height: el.style.height }
    el.style.position = 'relative'
    el.style.width = `${Math.floor(colWidth * w)}px`
    el.style.height = 'auto'
    const h = naturalHeight(el)
    el.style.position = prev.position
    el.style.width = prev.width
    el.style.height = prev.height
    return rowsFor(h)
  }

  private persist(force = false) {
    if (!this.grid || this.reloading) return
    if (!this.editing && !force) return
    const nodes = this.grid.save(false) as GridStackWidget[]
    const m: Record<string, Pos> = {}
    for (const n of nodes) {
      if (!n.id) continue
      let id = String(n.id)
      if (id.startsWith('ghost:')) id = id.slice(6)
      // A real box wins over its ghost if both are somehow present.
      if (m[id] && !String(n.id).startsWith('ghost:')) m[id] = { x: n.x ?? 0, y: n.y ?? 0, w: n.w ?? 3, h: n.h ?? 4 }
      else if (!m[id]) m[id] = { x: n.x ?? 0, y: n.y ?? 0, w: n.w ?? 3, h: n.h ?? 4 }
    }
    this.layouts[this.mode()] = m
    saveLayouts(this.layouts)
  }
}

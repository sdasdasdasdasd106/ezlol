<script setup lang="ts">
// Build widgets (items, boots, runes, spells, augments, Arena extras). The
// champion card that drives them is the fixed bar above the grid (ChampionBar).
import Widget from './Widget.vue'
import { computed } from 'vue'
import { winrate } from '../api'
import { buildState as s } from '../build'
import type { Build } from '../types'

const props = defineProps<{ compact?: boolean }>()
const build = computed(() => s.build)
const RARITIES = ['prismatic', 'gold', 'silver'] as const
const augBy = (b: Build, r: string) => (b.augments ?? []).filter((a) => a.rarity === r).slice(0, props.compact && !s.expandAugs ? 6 : 12)
const pctf = (x: number) => `${Math.round(x * 100)}%`
const tierLabel = (t?: number) => (t ? ['', 'S', 'A', 'B', 'C', 'D'][t] ?? String(t) : '')
const STYLE: Record<string, string> = { crit: '暴击流', on_hit: '攻速特效流', ap: '法强流', hybrid: '混伤流', lethality: '穿甲流', tank: '坦克流', bruiser: '战士流', ad: '物理流' }
const RARITY_LABEL: Record<string, string> = { prismatic: '棱彩', gold: '金色', silver: '银色' }
function togglePreview(id: number) {
  s.augPreview = s.augPreview.includes(id) ? s.augPreview.filter((x) => x !== id) : [...s.augPreview, id].slice(-4)
}
</script>

<template>
  <template v-if="build">
    <!-- Augments: one box per rarity -->
    <Widget v-for="r in RARITIES" :key="r" :id="'aug-' + r" :w="3" class="aug-col" :class="[r, { compact }]" v-show="build.augments?.length">
      <h2>{{ RARITY_LABEL[r] }}强化 <span class="muted" style="text-transform: none; letter-spacing: 0; margin-left: 8px">{{ build.augScope === 'champion' ? build.champion.name : '全英雄数据' }} · {{ build.augSource ?? 'aramgg' }}</span></h2>
      <div v-for="a in augBy(build, r)" :key="a.id" class="aug" :class="{ clickable: true, sel: s.augPreview.includes(a.id) }" :title="`${a.desc}\n（点击：在「强化出装」里看选它之后怎么出装）`" @click="togglePreview(a.id)">
        <img v-if="a.icon" :src="a.icon" :alt="a.name" /><span v-else class="noicon" />
        <div class="aug-body">
          <div class="aug-name"><span class="tier-pip" :class="'t' + a.tier">{{ tierLabel(a.tier) || '?' }}</span>{{ a.name }}</div>
          <div class="aug-desc">{{ a.desc }}</div>
        </div>
        <div class="stat" title="胜率 · 场次 · 选取率"><b>{{ pctf(a.winRate) }}</b>{{ a.games }}场 · {{ pctf(a.pickRate) }}</div>
      </div>
      <div v-if="!augBy(build, r).length" class="muted">暂无数据</div>
      <button v-if="compact && (build.augments ?? []).filter((a) => a.rarity === r).length > 6" class="sm" style="margin-top: 6px" @click="s.expandAugs = !s.expandAugs">{{ s.expandAugs ? '只看前 6' : '显示全部' }}</button>
    </Widget>

    <!-- Items -->
    <Widget v-if="build.starting?.length || build.core?.length" id="items" :w="3">
      <h2>出装</h2>
      <template v-if="build.starting?.length">
        <h3>出门装</h3>
        <div class="sets">
          <div v-for="(s, i) in build.starting" :key="i" class="set">
            <div class="items"><img v-for="(it, j) in s.items" :key="j" :src="it.image" :title="it.name" /></div>
            <div class="stat"><b>{{ winrate(s) }}</b>{{ s.games }} 场</div>
          </div>
        </div>
      </template>
      <template v-if="build.core?.length">
        <h3>核心装（按顺序）</h3>
        <div class="sets">
          <div v-for="(s, i) in build.core" :key="i" class="set" :title="build.coreTags?.[i] ? STYLE[build.coreTags[i]] ?? build.coreTags[i] : undefined">
            <div class="items">
              <template v-for="(it, j) in s.items" :key="j">
                <span v-if="j" class="arrow">›</span><img :src="it.image" :title="it.name" />
              </template>
            </div>
            <div class="stat"><b>{{ winrate(s) }}</b>{{ s.games }} 场</div>
          </div>
        </div>
      </template>
    </Widget>

    <Widget v-if="build.boots?.length || build.late?.length" id="boots" :w="3">
      <h2>鞋子与后期装</h2>
      <template v-if="build.boots?.length">
        <h3>鞋子</h3>
        <div class="sets">
          <div v-for="(s, i) in build.boots" :key="i" class="set">
            <div class="items"><img v-for="(it, j) in s.items" :key="j" :src="it.image" :title="it.name" /></div>
            <span>{{ s.items[0]?.name }}</span>
            <div class="stat"><b>{{ winrate(s) }}</b>{{ s.games }} 场</div>
          </div>
        </div>
      </template>
      <template v-if="build.late?.length">
        <h3>第 4–6 件可选</h3>
        <div class="sets">
          <div v-for="(s, i) in build.late" :key="i" class="set">
            <div class="items"><img v-for="(it, j) in s.items" :key="j" :src="it.image" :title="it.name" /></div>
            <span>{{ s.items[0]?.name }}</span>
            <div class="stat"><b>{{ winrate(s) }}</b>{{ s.games }} 场</div>
          </div>
        </div>
      </template>
    </Widget>

  </template>
</template>

<style scoped>
.compact .aug-desc { display: none; }
.compact .aug { padding: 3px 6px; grid-template-columns: 28px 1fr auto; }
.compact .aug img, .compact .noicon { width: 28px; height: 28px; }
.path { display: flex; gap: 3px; flex-wrap: wrap; margin-top: 8px; }
.lv { width: 26px; height: 32px; display: grid; place-items: center; font-family: var(--display); font-weight: 700; font-size: 12px; border: 1px solid var(--gold-deep); background: var(--surface-input); position: relative; }
.lv i { position: absolute; top: 1px; left: 3px; font-size: 8px; font-style: normal; color: var(--dim); }
.lv.q { color: #7fb3ff; } .lv.w { color: #7fe0a0; } .lv.e { color: #f0b232; } .lv.r { color: #e84057; }
.aug-col.prismatic h2 { color: var(--rarity-prismatic); background: linear-gradient(90deg, var(--rarity-prismatic-bg), transparent); }
.aug-col.gold h2 { color: var(--rarity-gold); background: linear-gradient(90deg, var(--rarity-gold-bg), transparent); }
.aug-col.silver h2 { color: var(--rarity-silver); background: linear-gradient(90deg, var(--rarity-silver-bg), transparent); }
button.sm { padding: 4px 8px; font-size: 10px; }
.aug { display: grid; grid-template-columns: 36px 1fr auto; gap: 8px; align-items: center; padding: 5px 6px; margin-bottom: 4px; border: 1px solid var(--line); background: var(--surface-raised); }
.aug.clickable { cursor: pointer; }
.aug.clickable:hover { border-color: var(--gold-deep); }
.aug.sel { border-color: var(--hextech-2); box-shadow: inset 0 0 0 1px var(--accent-glow); }
.aug img { width: 36px; height: 36px; border: 1px solid var(--gold-deep); background: #000; }
.aug-body { min-width: 0; }
.aug-name { font-weight: 600; font-size: 13px; display: flex; align-items: center; gap: 6px; }
.aug-desc { font-size: 11px; color: var(--muted); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.tier-pip { font-family: var(--display); font-size: 10px; width: 16px; height: 16px; display: grid; place-items: center; border: 1px solid var(--gold-deep); color: var(--muted); }
.tier-pip.t1 { color: #ff8a3d; border-color: #ff8a3d; } .tier-pip.t2 { color: var(--gold); border-color: var(--gold); } .tier-pip.t3 { color: var(--hextech-2); border-color: var(--hextech-dim); }
.noicon { width: 36px; height: 36px; border: 1px solid var(--gold-deep); background: #000; }
</style>

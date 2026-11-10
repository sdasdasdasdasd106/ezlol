<script setup lang="ts">
import { dismiss, toasts } from '../toast'
const pctf = (x: number) => `${Math.round(x * 100)}%`
const tierLabel = (t: number) => ['', 'S', 'A', 'B', 'C', 'D'][t] ?? '?'
</script>

<template>
  <div class="toasts" aria-live="polite">
    <TransitionGroup name="toast">
      <div v-for="t in toasts" :key="t.id" class="toast" :class="t.kind">
        <div class="head">
          <span class="title">{{ t.title }}</span>
          <button class="x" title="关闭" @click="dismiss(t.id)">×</button>
        </div>
        <div v-if="t.body" class="body">{{ t.body }}</div>
        <div v-if="t.cards?.length" class="cards">
          <div v-for="(a, i) in t.cards" :key="a.id" class="card" :class="[a.rarity, { best: i === 0 }]" :title="a.desc">
            <div class="ic"><img v-if="a.icon" :src="a.icon" :alt="a.name" /></div>
            <div class="cb">
              <div class="n"><span class="tp" :class="'t' + a.tier">{{ tierLabel(a.tier) }}</span>{{ a.name }}</div>
              <div class="s">{{ a.games ? `胜率 ${pctf(a.winRate)} · ${a.games} 场 · 选取 ${pctf(a.pickRate)}` : '暂无数据' }}</div>
              <div class="d">{{ a.desc }}</div>
            </div>
          </div>
        </div>
        <div v-if="t.actions?.length" class="actions">
          <button v-for="a in t.actions" :key="a.label" :class="{ primary: a.primary }" @click="a.run()">{{ a.label }}</button>
        </div>
      </div>
    </TransitionGroup>
  </div>
</template>

<style scoped>
.toasts { position: fixed; top: 64px; right: 16px; z-index: 200; display: grid; gap: 10px; width: min(440px, calc(100vw - 32px)); pointer-events: none; }
.toast { pointer-events: auto; background: var(--panel); border: 1px solid var(--gold-dark); border-left-width: 4px; box-shadow: 0 10px 30px rgba(0,0,0,.6), 0 0 0 1px #000 inset; padding: 12px 14px; display: grid; gap: 8px; }
.toast.info { border-left-color: var(--hextech-2); }
.toast.success { border-left-color: var(--green); }
.toast.warn { border-left-color: var(--amber); }
.toast.error { border-left-color: var(--red); }
.toast.pick { border-color: var(--hextech-2); border-left-width: 4px; box-shadow: 0 0 24px var(--accent-glow), 0 10px 30px rgba(0,0,0,.6); width: 100%; }
.head { display: flex; align-items: center; gap: 10px; }
.title { font-family: var(--display); font-size: 16px; font-weight: 700; letter-spacing: .06em; color: var(--gold-bright); }
.pick .title { color: var(--hextech-2); font-size: 18px; }
.x { margin-left: auto; padding: 0 8px; font-size: 16px; line-height: 22px; }
.body { font-size: 14px; color: var(--text); }
.actions { display: flex; gap: 8px; }
.cards { display: grid; gap: 6px; }
.card { display: grid; grid-template-columns: 44px 1fr; gap: 8px; align-items: center; padding: 6px; border: 1px solid var(--gold-deep); background: var(--surface-raised); }
.card.best { border-color: var(--hextech-2); box-shadow: inset 0 0 0 1px var(--accent-glow); }
.card.prismatic { border-left: 3px solid #3fb4d8; } .card.gold { border-left: 3px solid var(--gold); } .card.silver { border-left: 3px solid #8b9bb0; }
.card .ic { width: 44px; height: 44px; border: 1px solid var(--gold-deep); background: #000; }
.card .ic img { width: 44px; height: 44px; display: block; }
.card .cb { min-width: 0; }
.card .n { font-weight: 700; font-size: 14px; display: flex; gap: 6px; align-items: center; }
.card .s { font-size: 12px; color: var(--hextech-2); font-family: var(--display); }
.card .d { font-size: 11px; color: var(--muted); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.tp { font-family: var(--display); font-size: 10px; width: 16px; height: 16px; display: grid; place-items: center; border: 1px solid var(--gold-deep); color: var(--muted); }
.tp.t1 { color: #ff8a3d; border-color: #ff8a3d; } .tp.t2 { color: var(--gold); border-color: var(--gold); } .tp.t3 { color: var(--hextech-2); border-color: var(--hextech-dim); }
.toast-enter-active, .toast-leave-active { transition: all .18s ease; }
.toast-enter-from, .toast-leave-to { opacity: 0; transform: translateX(20px); }
</style>

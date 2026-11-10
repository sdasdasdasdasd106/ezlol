<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'

export interface ThemeOption {
  id: string
  label: string
  swatch: [string, string, string] // bg, panel, accent
}

const props = defineProps<{ options: ThemeOption[] }>()
const model = defineModel<string>({ required: true })
const open = ref(false)
const root = ref<HTMLElement | null>(null)
const current = () => props.options.find((o) => o.id === model.value) ?? props.options[0]

function choose(id: string) {
  model.value = id
  open.value = false
}
function onKey(e: KeyboardEvent) {
  if (!open.value) return
  const idx = props.options.findIndex((o) => o.id === model.value)
  if (e.key === 'Escape') open.value = false
  else if (e.key === 'ArrowDown') model.value = props.options[Math.min(props.options.length - 1, idx + 1)].id
  else if (e.key === 'ArrowUp') model.value = props.options[Math.max(0, idx - 1)].id
  else if (e.key === 'Enter') open.value = false
  else return
  e.preventDefault()
}
function onDoc(e: MouseEvent) {
  if (open.value && root.value && !root.value.contains(e.target as Node)) open.value = false
}
onMounted(() => {
  document.addEventListener('mousedown', onDoc)
  document.addEventListener('keydown', onKey)
})
onUnmounted(() => {
  document.removeEventListener('mousedown', onDoc)
  document.removeEventListener('keydown', onKey)
})
</script>

<template>
  <div ref="root" class="tm">
    <button class="tm-btn" :aria-expanded="open" aria-haspopup="listbox" title="主题" @click="open = !open">
      <span class="sw"><i :style="{ background: current().swatch[0] }" /><i :style="{ background: current().swatch[1] }" /><i :style="{ background: current().swatch[2] }" /></span>
      <span class="lbl">{{ current().label }}</span>
      <span class="chev" :class="{ up: open }" />
    </button>
    <ul v-if="open" class="tm-menu" role="listbox">
      <li v-for="o in options" :key="o.id" role="option" :aria-selected="o.id === model" :class="{ sel: o.id === model }" @click="choose(o.id)">
        <span class="sw"><i :style="{ background: o.swatch[0] }" /><i :style="{ background: o.swatch[1] }" /><i :style="{ background: o.swatch[2] }" /></span>
        <span class="lbl">{{ o.label }}</span>
        <span v-if="o.id === model" class="check">✓</span>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.tm { position: relative; }
.tm-btn { display: inline-flex; align-items: center; gap: 10px; min-height: 40px; padding: 6px 12px; }
.sw { display: inline-flex; gap: 2px; }
.sw i { width: 10px; height: 16px; border: 1px solid rgba(0, 0, 0, 0.5); border-radius: 2px; display: block; }
.lbl { min-width: 84px; text-align: left; }
.chev { width: 0; height: 0; border-left: 5px solid transparent; border-right: 5px solid transparent; border-top: 6px solid var(--muted); transition: transform .15s; }
.chev.up { transform: rotate(180deg); }
.tm-menu { position: absolute; top: calc(100% + 6px); right: 0; z-index: 300; margin: 0; padding: 6px; list-style: none; min-width: 200px;
  background: var(--panel-2); border: 1px solid var(--gold-deep); border-radius: var(--radius-sm); box-shadow: var(--shadow-panel); }
.tm-menu li { display: flex; align-items: center; gap: 10px; padding: 8px 10px; cursor: pointer; font-family: var(--display); font-size: 12px; letter-spacing: var(--label-spacing); text-transform: var(--label-transform); color: var(--text); border-radius: var(--radius-sm); }
.tm-menu li:hover { background: var(--gold-soft); color: var(--gold-bright); }
.tm-menu li.sel { color: var(--accent); }
.check { margin-left: auto; color: var(--accent); }
</style>

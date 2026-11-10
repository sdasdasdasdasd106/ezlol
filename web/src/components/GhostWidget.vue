<script setup lang="ts">
// Placeholder for a box that is not on screen right now. Only rendered in edit
// mode, so the user can decide where the box will appear later; its position is
// saved under the real widget's id.
import { inject, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { DASH_KEY, type WidgetSpec } from '../dashboard'

const props = defineProps<{ spec: WidgetSpec }>()
const dash = inject(DASH_KEY)
const el = ref<HTMLElement | null>(null)
onMounted(async () => {
  await nextTick()
  if (el.value) dash?.add(props.spec.id, el.value, { w: props.spec.w, h: props.spec.h })
})
onBeforeUnmount(() => {
  if (el.value) dash?.remove(el.value)
})
</script>

<template>
  <div ref="el" class="grid-stack-item ghost" :gs-id="'ghost:' + spec.id" :data-gs-opts="JSON.stringify({ w: spec.w, h: spec.h })">
    <section class="panel grid-stack-item-content ghost-box">
      <div class="wcontent">
        <div class="g-title">{{ spec.title }}</div>
        <div class="g-when">{{ spec.when ? `出现时机：${spec.when}` : '当前不显示' }}</div>
      </div>
    </section>
  </div>
</template>

<style scoped>
.ghost-box { background: transparent !important; border: 1px dashed var(--muted) !important; box-shadow: none !important; opacity: .8; display: grid; place-items: center; text-align: center; }
.ghost-box::before, .ghost-box::after { display: none !important; }
.g-title { font-family: var(--display); font-size: 13px; letter-spacing: .08em; text-transform: var(--label-transform); color: var(--muted); }
.g-when { font-size: 11px; color: var(--dim); margin-top: 4px; }
</style>

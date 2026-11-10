<script setup lang="ts">
import Widget from './Widget.vue'
import { onMounted, onUnmounted, ref } from 'vue'
import { api } from '../api'
import type { BuildsStatus } from '../types'

const st = ref<BuildsStatus | null>(null)
const err = ref('')
let timer: number | undefined

async function refresh() {
  try {
    st.value = await api.buildsStatus()
  } catch (e) {
    err.value = (e as Error).message
  }
}
async function start(queue: 'ranked' | 'aram') {
  err.value = ''
  try {
    await api.compile(queue)
    await refresh()
  } catch (e) {
    err.value = (e as Error).message
  }
}
async function stop() {
  await api.compileStop()
  await refresh()
}
onMounted(() => {
  refresh()
  timer = window.setInterval(refresh, 3000)
})
onUnmounted(() => clearInterval(timer))
</script>

<template>
  <Widget id="compile" :w="3">
    <h2>出装数据统计（可选）</h2>
    <template v-if="st">
      <div class="kv">
        <span>Riot API key</span><b>{{ st.hasKey ? '已设置' : '未设置' }}</b>
        <span>服务器</span><b>{{ st.platform }}</b>
        <template v-for="p in st.patches ?? []" :key="p.patch">
          <span>版本 {{ p.patch }}</span><b>{{ p.matches }} 场对局 · {{ p.champions }} 个英雄</b>
        </template>
      </div>
      <div v-if="st.progress.running" style="margin-top: 10px">
        <div class="muted">{{ st.progress.queue === 450 ? '大乱斗' : '排位' }} · {{ st.progress.phase }} · 请求 {{ st.progress.requests }} 次 · 出错 {{ st.progress.errors }} 次</div>
        <div class="progress"><div :style="{ width: `${(100 * st.progress.matchesDone) / Math.max(1, st.progress.target)}%` }" /></div>
        <div class="row">
          <span class="muted">{{ st.progress.matchesDone }} / {{ st.progress.target }} 场</span>
          <button class="danger" @click="stop">停止</button>
        </div>
      </div>
      <div v-else class="row" style="margin-top: 10px">
        <button class="primary" :disabled="!st.hasKey" @click="start('ranked')">统计排位</button>
        <button class="primary" :disabled="!st.hasKey" @click="start('aram')">统计大乱斗</button>
        <span v-if="!st.hasKey" class="muted">出装默认用 op.gg 数据，不需要这个。想用东南亚服高分段对局自己统计，在设置里填 Riot API key。</span>
      </div>
      <div v-if="st.progress.lastError" class="note">{{ st.progress.lastError }}</div>
    </template>
    <div v-if="err" class="note">{{ err }}</div>
  </Widget>
</template>

// Look of the app: "clean" (简约, default) or "hextech" (classic League).
import { ref, watch } from 'vue'

export const THEMES = [
  { id: 'clean', label: '简约' },
  { id: 'hextech', label: '海克斯' },
] as const
export type ThemeId = (typeof THEMES)[number]['id']

const KEY = 'ezlol.theme'
// v2: the clean theme became the default; earlier choices (mostly the old
// default, hextech) start over on it once.
const MIGRATED = 'ezlol.theme.v2'

function initial(): ThemeId {
  const param = new URLSearchParams(location.search).get('theme')
  if (THEMES.some((t) => t.id === param)) return param as ThemeId
  try {
    if (!localStorage.getItem(MIGRATED)) {
      localStorage.setItem(MIGRATED, '1')
      return 'clean'
    }
    const saved = localStorage.getItem(KEY)
    if (THEMES.some((t) => t.id === saved)) return saved as ThemeId
  } catch {
    /* storage unavailable */
  }
  return 'clean'
}

export const theme = ref<ThemeId>(initial())
watch(
  theme,
  (t) => {
    document.documentElement.dataset.theme = t
    try {
      localStorage.setItem(KEY, t)
    } catch {
      /* ignore */
    }
  },
  { immediate: true },
)

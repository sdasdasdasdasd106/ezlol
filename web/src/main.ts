import { createApp } from 'vue'
import App from './App.vue'
import Overlay from './components/Overlay.vue'
import { autoscroll } from './autoscroll'
import 'gridstack/dist/gridstack.min.css'
import './style.css'

// Apply the saved theme before first paint to avoid a flash of the default.
try {
  const t = localStorage.getItem('ezlol.theme')
  if (t) document.documentElement.dataset.theme = t
} catch {
  /* ignore */
}

if ((window as unknown as { ezlol?: { electron?: boolean } }).ezlol?.electron || /Electron/.test(navigator.userAgent)) document.body.classList.add('electron')
// ?overlay=1: the always-on-top mini window shown over the game (Electron).
const overlay = new URLSearchParams(location.search).get('overlay') === '1'
if (overlay) document.documentElement.classList.add('overlay')
createApp(overlay ? Overlay : App).directive('autoscroll', autoscroll).mount('#app')

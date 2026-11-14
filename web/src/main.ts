import { createApp } from 'vue'
import App from './App.vue'
import SidePanel from './components/SidePanel.vue'
import { autoscroll } from './autoscroll'
import 'gridstack/dist/gridstack.min.css'
import './style.css'
import './theme' // applies the theme before first paint

if ((window as unknown as { ezlol?: { electron?: boolean } }).ezlol?.electron || /Electron/.test(navigator.userAgent)) document.body.classList.add('electron')
// ?side=1: the small always-on-top panel at the side of the screen during a game (Electron).
const side = new URLSearchParams(location.search).get('side') === '1'
if (side) document.documentElement.classList.add('side')
createApp(side ? SidePanel : App).directive('autoscroll', autoscroll).mount('#app')

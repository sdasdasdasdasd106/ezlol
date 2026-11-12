import { createApp } from 'vue'
import App from './App.vue'
import { autoscroll } from './autoscroll'
import 'gridstack/dist/gridstack.min.css'
import './style.css'
import './theme' // applies the theme before first paint

if ((window as unknown as { ezlol?: { electron?: boolean } }).ezlol?.electron || /Electron/.test(navigator.userAgent)) document.body.classList.add('electron')
createApp(App).directive('autoscroll', autoscroll).mount('#app')

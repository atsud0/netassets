import { createApp } from 'vue'
import 'element-plus/es/components/base/style/css'
import 'element-plus/es/components/message/style/css'
import 'element-plus/es/components/message-box/style/css'
import './styles/index.css'
import './styles/workspace.css'
import App from './App.vue'
import router from './router'
import { pinia } from './stores'

createApp(App).use(pinia).use(router).mount('#app')

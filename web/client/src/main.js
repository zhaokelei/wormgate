import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'

import '@common/theme.css'
import '@common/components.css'
import '@common/responsive.css'
import '@common/i18n'

import { installTheme } from '@common/theme'
import App from './App.vue'
import { session } from './session'

const routes = [
  {
    path: '/login',
    name: 'login',
    component: () => import('./views/LoginView.vue'),
    meta: { public: true, layout: 'plain' }
  },
  {
    path: '/',
    name: 'dashboard',
    component: () => import('./views/DashboardView.vue')
  },
  {
    path: '/tunnels',
    name: 'tunnels',
    component: () => import('./views/TunnelsView.vue')
  },
  {
    path: '/connections',
    name: 'connections',
    component: () => import('./views/ConnectionsView.vue')
  },
  {
    path: '/config',
    name: 'config',
    component: () => import('./views/ConfigView.vue')
  },
  {
    path: '/logs',
    name: 'logs',
    component: () => import('./views/LogsView.vue')
  },
  {
    path: '/security',
    name: 'security',
    component: () => import('./views/SecurityView.vue')
  },
  { path: '/:pathMatch(.*)*', redirect: '/' }
]

const router = createRouter({
  history: createWebHistory('/'),
  routes,
  scrollBehavior: () => ({ top: 0 })
})

/**
 * Route guard.
 *
 * `session.ensure()` performs a single GET /api/session, memoised until the
 * session is invalidated, so navigating between pages costs no extra request.
 * Unknown auth state (first load) falls through to /login without a flash.
 */
router.beforeEach(async (to) => {
  const authed = await session.ensure()
  if (to.meta.public) {
    return authed ? { name: 'dashboard' } : true
  }
  if (!authed) {
    return { name: 'login', query: to.fullPath === '/' ? {} : { redirect: to.fullPath } }
  }
  return true
})

const app = createApp(App)
installTheme(app)
app.use(router)
app.mount('#app')

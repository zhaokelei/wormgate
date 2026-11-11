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
    path: '/clients',
    name: 'clients',
    component: () => import('./views/ClientsView.vue')
  },
  {
    path: '/nodes',
    name: 'nodes',
    component: () => import('./views/NodesView.vue')
  },
  {
    path: '/users',
    name: 'users',
    component: () => import('./views/UsersView.vue'),
    meta: { requiresAdmin: true }
  },
  {
    path: '/audit',
    name: 'audit',
    component: () => import('./views/AuditView.vue'),
    meta: { requiresStaff: true }
  },
  {
    path: '/alerts',
    name: 'alerts',
    component: () => import('./views/AlertsView.vue')
  },
  {
    path: '/security',
    name: 'security',
    component: () => import('./views/SecurityView.vue')
  },
  { path: '/:pathMatch(.*)*', redirect: '/' }
]

const router = createRouter({
  // Served under /admin/ (base in vite.admin.config.js), so history must be
  // rooted there or a deep link would resolve outside the console.
  history: createWebHistory('/admin/'),
  routes,
  scrollBehavior: () => ({ top: 0 })
})

/**
 * Guard: authenticate first, then enforce role requirements.
 *
 * Role gating is defence-in-depth only — every admin endpoint re-checks the
 * role server-side (adminAuth(..., auth.RoleAdmin)), so hiding a menu item is
 * never the actual control.
 */
router.beforeEach(async (to) => {
  const authed = await session.ensure()
  if (to.meta.public) {
    return authed ? { name: 'dashboard' } : true
  }
  if (!authed) {
    return { name: 'login', query: to.fullPath === '/' ? {} : { redirect: to.fullPath } }
  }
  if (to.meta.requiresAdmin && !session.isAdmin) {
    return { name: 'dashboard' }
  }
  if (to.meta.requiresStaff && session.isViewer === false && !session.isAdmin) {
    return { name: 'dashboard' }
  }
  return true
})

const app = createApp(App)
installTheme(app)
app.use(router)
app.mount('#app')

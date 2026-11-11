/**
 * Admin console API surface.
 *
 * Mirrors internal/server/admin.go. Roles are noted per endpoint because the
 * UI has to hide actions a user cannot perform:
 *
 * method path                              handler              role
 * GET    /api/meta                         handleAdminMeta      public
 * POST   /api/login                        handleAdminLogin     public
 * GET    /api/oauth/start                  handleOAuthStart     public (if enabled)
 * POST   /api/logout                       handleAdminLogout     any
 * GET    /api/me                           handleAdminMe         any
 * POST   /api/me/password                  handleChangeOwnPassword any
 * GET    /api/overview                     handleOverview        any (scoped)
 * GET    /api/stats                        handleAdminStats      any (scoped)
 * GET    /api/tunnels                      handleAdminTunnels    any (scoped)
 * DELETE /api/tunnels/{id}                 handleRevokeTunnel    admin|owner
 * GET    /api/clients                      handleAdminClients    any (scoped)
 * POST   /api/clients/{id}/kick            handleKickClient      admin|owner
 * GET    /api/nodes                        handleNodes           any
 * GET    /api/alerts                       handleAlerts          any
 * POST   /api/alerts/{id}/ack              handleAckAlert        admin
 * GET    /api/audit                        handleAudit           admin|viewer
 * GET    /api/encryption                   handleGetEncryption   admin
 * PUT    /api/encryption                   handlePutEncryption   admin
 * GET    /api/users                        handleListUsers       admin
 * POST   /api/users                        handleCreateUser      admin
 * PATCH  /api/users/{id}                   handleUpdateUser      admin
 * DELETE /api/users/{id}                   handleDeleteUser      admin
 * POST   /api/users/{id}/rotate-token      handleRotateToken     admin
 * GET    /api/users/{id}/token             handleRevealToken     admin
 * GET    /api/users/{id}/export            handleExportClientConfig  admin
 * POST   /api/users/{id}/password          handleSetPassword     admin
 * GET    /api/ws                           handleAdminWS         any (scoped)
 */
import { api } from './session'

export const adminApi = {
  // -- session ------------------------------------------------------------
  meta: () => api.get('/meta'),
  me: () => api.get('/me'),
  logout: () => api.post('/logout', {}),
  changePassword: (payload) => api.post('/me/password', payload),

  // -- dashboard ----------------------------------------------------------
  overview: () => api.get('/overview'),
  stats: () => api.get('/stats'),

  // -- inventory ----------------------------------------------------------
  tunnels: () => api.get('/tunnels'),
  revokeTunnel: (id) => api.del(`/tunnels/${encodeURIComponent(id)}`),
  clients: () => api.get('/clients'),
  kickClient: (id) => api.post(`/clients/${encodeURIComponent(id)}/kick`, {}),
  nodes: () => api.get('/nodes'),

  // -- operations ---------------------------------------------------------
  alerts: () => api.get('/alerts'),
  ackAlert: (id) => api.post(`/alerts/${encodeURIComponent(id)}/ack`, {}),
  audit: (limit = 200) => api.get(`/audit?limit=${limit}`),

  // -- stream encryption --------------------------------------------------
  encryption: () => api.get('/encryption'),
  // payload: { generate: true } | { secret: '...' } | { disable: true }
  setEncryption: (payload) => api.put('/encryption', payload),

  // -- users --------------------------------------------------------------
  users: () => api.get('/users'),
  createUser: (payload) => api.post('/users', payload),
  updateUser: (id, payload) => api.patch(`/users/${encodeURIComponent(id)}`, payload),
  deleteUser: (id) => api.del(`/users/${encodeURIComponent(id)}`),
  rotateToken: (id) => api.post(`/users/${encodeURIComponent(id)}/rotate-token`, {}),
  userToken: (id) => api.get(`/users/${encodeURIComponent(id)}/token`),
  exportClient: (id) => api.get(`/users/${encodeURIComponent(id)}/export`),
  setPassword: (id, password) => api.post(`/users/${encodeURIComponent(id)}/password`, { password })
}

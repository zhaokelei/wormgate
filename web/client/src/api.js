/**
 * Client console API surface.
 *
 * Every endpoint here maps 1:1 to a handler in internal/client (api.go,
 * webui.go). Keeping them in one module means the Vue layer never concatenates
 * URLs, and the whole client-side contract can be reviewed in one file.
 *
 * method path                     handler
 * GET    /api/meta                handleMeta          runtime/version info
 * POST   /api/login               handleLogin         secret -> session
 * POST   /api/logout              handleLogout
 * POST   /api/password            handleChangePassword  local console password
 * GET    /api/session             handleSession       CSRF + expiry
 * GET    /api/status              handleStatus        control-plane + tunnels
 * GET    /api/tunnels             handleListTunnels
 * POST   /api/tunnels             handleCreateTunnel
 * PUT    /api/tunnels/{name}      handleUpdateTunnel
 * DELETE /api/tunnels/{name}      handleDeleteTunnel
 * POST   /api/tunnels/{name}/start|stop  handleTunnelEnable
 * POST   /api/reconnect           handleReconnect
 * GET    /api/connections         handleConnections
 * GET    /api/stats               handleStats
 * GET    /api/config              handleGetConfig     YAML text
 * PUT    /api/config              handlePutConfig     YAML text
 * GET    /api/logs                handleLogsSSE       text/event-stream
 * GET    /api/ws                  handleWS            realtime snapshot
 */
import { api } from './session'

export const clientApi = {
  // -- browser login credential -------------------------------------------
  changePassword: (payload) => api.post('/password', payload),

  // -- runtime ------------------------------------------------------------
  meta: () => api.get('/meta'),
  status: () => api.get('/status'),
  reconnect: () => api.post('/reconnect', {}),

  // -- tunnels ------------------------------------------------------------
  listTunnels: () => api.get('/tunnels'),
  createTunnel: (payload) => api.post('/tunnels', payload),
  updateTunnel: (name, payload) => api.put(`/tunnels/${encodeURIComponent(name)}`, payload),
  deleteTunnel: (name) => api.del(`/tunnels/${encodeURIComponent(name)}`),
  startTunnel: (name) => api.post(`/tunnels/${encodeURIComponent(name)}/start`, {}),
  stopTunnel: (name) => api.post(`/tunnels/${encodeURIComponent(name)}/stop`, {}),

  // -- observability ------------------------------------------------------
  connections: () => api.get('/connections'),
  stats: () => api.get('/stats'),

  // -- configuration ------------------------------------------------------
  getConfig: () => api.getText('/config'),
  putConfig: (yaml) => api.putText('/config', yaml),
  getSettings: () => api.get('/settings'),
  putSettings: (payload) => api.put('/settings', payload)
}

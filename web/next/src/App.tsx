import { Spin } from 'antd'
import { Navigate, Route, Routes, useLocation } from 'react-router'
import type { ReactNode } from 'react'

import Shell from './layout/Shell'
import Login from './pages/Login'
import Dashboard from './pages/Dashboard'
import Users from './pages/Users'
import Tunnels from './pages/Tunnels'
import Clients from './pages/Clients'
import Nodes from './pages/Nodes'
import Alerts from './pages/Alerts'
import Audit from './pages/Audit'
import Security from './pages/Security'
import { useAuth } from './auth'

function RequireAuth({ children }: { children: ReactNode }) {
  const { status } = useAuth()
  const location = useLocation()
  if (status === 'unknown') {
    return (
      <div style={{ display: 'grid', placeItems: 'center', minHeight: '100dvh' }}>
        <Spin size="large" />
      </div>
    )
  }
  if (status === 'anon') return <Navigate to="/login" replace state={{ from: location.pathname }} />
  return <>{children}</>
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route
        element={
          <RequireAuth>
            <Shell />
          </RequireAuth>
        }
      >
        <Route index element={<Dashboard />} />
        <Route path="tunnels" element={<Tunnels />} />
        <Route path="users" element={<Users />} />
        <Route path="clients" element={<Clients />} />
        <Route path="nodes" element={<Nodes />} />
        <Route path="alerts" element={<Alerts />} />
        <Route path="audit" element={<Audit />} />
        <Route path="security" element={<Security />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  )
}

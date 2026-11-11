import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { api, setUnauthorizedHandler } from './api'

/**
 * Session state. The server is the authority: this only mirrors the identity so
 * the UI can hide actions, and it never decides authorisation on its own.
 */
export interface AdminUser {
  id: string
  username: string
  display_name?: string
  role: 'admin' | 'user' | 'viewer' | string
}

interface AuthValue {
  status: 'unknown' | 'authed' | 'anon'
  user: AdminUser | null
  isAdmin: boolean
  login: (username: string, password: string) => Promise<void>
  logout: () => Promise<void>
  refresh: () => Promise<void>
}

const AuthCtx = createContext<AuthValue>(null as any)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<AuthValue['status']>('unknown')
  const [user, setUser] = useState<AdminUser | null>(null)

  const refresh = useCallback(async () => {
    try {
      const data: any = await api('/me')
      setUser(data?.user ?? data ?? null)
      setStatus('authed')
    } catch {
      setUser(null)
      setStatus('anon')
    }
  }, [])

  useEffect(() => {
    setUnauthorizedHandler(() => {
      setUser(null)
      setStatus('anon')
    })
    void refresh()
  }, [refresh])

  const login = useCallback(async (username: string, password: string) => {
    const data: any = await api('/login', { method: 'POST', body: { username, password } })
    setUser(data?.user ?? null)
    setStatus('authed')
  }, [])

  const logout = useCallback(async () => {
    try {
      await api('/logout', { method: 'POST', body: {} })
    } finally {
      setUser(null)
      setStatus('anon')
    }
  }, [])

  const value = useMemo<AuthValue>(
    () => ({ status, user, isAdmin: user?.role === 'admin', login, logout, refresh }),
    [status, user, login, logout, refresh]
  )

  return <AuthCtx.Provider value={value}>{children}</AuthCtx.Provider>
}

export function useAuth() {
  return useContext(AuthCtx)
}

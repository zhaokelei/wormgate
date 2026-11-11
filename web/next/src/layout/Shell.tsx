import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { Link, Outlet, useLocation, useNavigate } from 'react-router'
import {
  Avatar,
  Button,
  Dropdown,
  Layout,
  Menu,
  Space,
  Tooltip,
  Typography,
  theme as antdTheme
} from 'antd'
import {
  ApiOutlined,
  BellOutlined,
  BulbOutlined,
  ClusterOutlined,
  DashboardOutlined,
  FileTextOutlined,
  LogoutOutlined,
  SafetyCertificateOutlined,
  TeamOutlined,
  WifiOutlined
} from '@ant-design/icons'
import { useAuth } from '../auth'
import { useI18n } from '../i18n'
import { useThemeMode } from '../theme'
import { APP_VERSION } from '../version'
import LiveDot from '../components/LiveDot'
import PageTransition from '../components/PageTransition'
import SoftTag from '../components/SoftTag'

const { Header, Sider, Content } = Layout

/**
 * Role names are shown in Chinese and translated to English by the i18n layer.
 * The raw value must not be printed next to the username: for the built-in
 * account that reads "admin admin".
 */
const ROLE_TEXT: Record<string, string> = { admin: '管理员', user: '用户', viewer: '只读' }
const ROLE_TONE: Record<string, string> = { admin: 'blue', user: 'gray', viewer: 'purple' }

const NAV = [
  { key: '/', icon: <DashboardOutlined />, label: '总览' },
  { key: '/tunnels', icon: <ApiOutlined />, label: '隧道' },
  { key: '/clients', icon: <WifiOutlined />, label: '客户端' },
  { key: '/nodes', icon: <ClusterOutlined />, label: '节点' },
  { key: '/alerts', icon: <BellOutlined />, label: '告警' },
  { key: '/audit', icon: <FileTextOutlined />, label: '审计' },
  { key: '/users', icon: <TeamOutlined />, label: '用户与租户', adminOnly: true },
  { key: '/security', icon: <SafetyCertificateOutlined />, label: '安全' }
]

export default function Shell() {
  const [collapsed, setCollapsed] = useState(false)
  const [visible, setVisible] = useState(() => !document.hidden)
  const [bar, setBar] = useState({ y: 0, h: 38, ready: false })
  const navRef = useRef<HTMLDivElement>(null)
  const location = useLocation()
  const navigate = useNavigate()
  const { user, logout, isAdmin } = useAuth()
  const { mode, toggle } = useThemeMode()
  const { lang, toggle: toggleLang } = useI18n()
  const { token } = antdTheme.useToken()

  const name = user?.display_name || user?.username || ''
  const role = user?.role || ''
  const roleText = ROLE_TEXT[role] || role

  const items = useMemo(
    () =>
      NAV.filter((item) => !item.adminOnly || isAdmin).map((item) => ({
        key: item.key,
        icon: item.icon,
        label: item.label
      })),
    [isAdmin]
  )

  const selected = useMemo(() => {
    const hit = NAV.filter((item) => !item.adminOnly || isAdmin)
      .map((i) => i.key)
      .filter((key) => (key === '/' ? location.pathname === '/' : location.pathname.startsWith(key)))
      .sort((a, b) => b.length - a.length)[0]
    return hit ? [hit] : ['/']
  }, [location.pathname, isAdmin])

  /**
   * Places the sliding highlight over the selected item.
   *
   * Measuring the DOM is the only reliable way here: antd owns the item box,
   * and the position changes with the collapsed width. It re-measures when the
   * sider finishes collapsing, because during the transition the item is still
   * moving.
   */
  const measure = useCallback(() => {
    const wrap = navRef.current
    const item = wrap?.querySelector<HTMLElement>('.ant-menu-item-selected')
    if (!wrap || !item) return
    const wrapBox = wrap.getBoundingClientRect()
    const itemBox = item.getBoundingClientRect()
    setBar({ y: itemBox.top - wrapBox.top, h: itemBox.height, ready: true })
  }, [])

  useLayoutEffect(() => {
    measure()
    const settle = window.setTimeout(measure, 240)
    return () => window.clearTimeout(settle)
  }, [selected, collapsed, isAdmin, measure])

  useEffect(() => {
    const el = navRef.current
    if (!el || typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(() => measure())
    ro.observe(el)
    return () => ro.disconnect()
  }, [measure])

  useEffect(() => {
    const onVis = () => setVisible(!document.hidden)
    document.addEventListener('visibilitychange', onVis)
    return () => document.removeEventListener('visibilitychange', onVis)
  }, [])

  return (
    <Layout style={{ minHeight: '100dvh', background: token.colorBgLayout }}>
      <Sider
        collapsible
        collapsed={collapsed}
        onCollapse={setCollapsed}
        width={216}
        collapsedWidth={64}
        theme={mode}
        style={{
          borderRight: `1px solid ${token.colorBorder}`,
          background: token.colorBgContainer,
          backdropFilter: 'blur(12px)'
        }}
      >
        <div className="brand" style={{ color: token.colorText }}>
          <span className="brand-logo">W</span>
          {!collapsed && (
            <span>
              Wormgate
              <small style={{ color: token.colorTextTertiary }}>服务端控制台</small>
            </span>
          )}
        </div>

        <div className="nav-wrap" ref={navRef}>
          <span
            className="nav-indicator"
            style={{ transform: `translateY(${bar.y}px)`, height: bar.h, opacity: bar.ready ? 1 : 0 }}
          />
          <Menu
            mode="inline"
            theme={mode}
            selectedKeys={selected}
            items={items}
            onClick={({ key }) => navigate(key)}
            style={{ borderInlineEnd: 'none', background: 'transparent' }}
          />
        </div>
      </Sider>

      <Layout style={{ background: 'transparent' }}>
        <Header
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'flex-end',
            gap: 10,
            paddingInline: 20,
            height: 52,
            lineHeight: '52px',
            background: token.colorBgContainer,
            backdropFilter: 'blur(12px)',
            borderBottom: `1px solid ${token.colorBorder}`,
            position: 'sticky',
            top: 0,
            zIndex: 9
          }}
        >
          <Space size={8}>
            <LiveDot text={visible ? '实时' : '已暂停'} active={visible} />
            <Link to="/">
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                v{APP_VERSION}
              </Typography.Text>
            </Link>
            {/* Same affordance as the Vue consoles: EN/中 sits next to the theme
                switch. The label is marked skip — "中" would otherwise be read
                as an untranslated Chinese fragment and blanked in English. */}
            <Tooltip title={lang === 'zh' ? 'Switch to English' : 'Switch to Chinese'}>
              <Button
                type="text"
                onClick={toggleLang}
                aria-label={lang === 'zh' ? 'Switch to English' : 'Switch to Chinese'}
                style={{ minWidth: 34, paddingInline: 6 }}
              >
                <span data-i18n-skip style={{ fontSize: 11, fontWeight: 600, letterSpacing: 0.4 }}>
                  {lang === 'zh' ? 'EN' : '中'}
                </span>
              </Button>
            </Tooltip>
            <Tooltip title={mode === 'dark' ? '切换到浅色' : '切换到深色'}>
              <Button
                type="text"
                icon={<BulbOutlined />}
                onClick={toggle}
                aria-label={mode === 'dark' ? '切换到浅色' : '切换到深色'}
                className="theme-toggle"
              />
            </Tooltip>
            <Dropdown
              menu={{
                items: [
                  { key: 'legacy', label: <a href="/admin/">切回旧版控制台</a> },
                  { key: 'logout', icon: <LogoutOutlined />, label: '退出登录' }
                ],
                onClick: async ({ key }) => {
                  if (key === 'logout') await logout()
                }
              }}
            >
              <Space style={{ cursor: 'pointer', paddingInline: 6 }} size={8}>
                <Avatar size={26} style={{ background: token.colorPrimary }}>
                  {(name || '?').slice(0, 1).toUpperCase()}
                </Avatar>
                <span style={{ fontSize: 13 }}>{name}</span>
                {roleText && roleText !== name && (
                  <SoftTag tone={ROLE_TONE[role] || 'gray'}>{roleText}</SoftTag>
                )}
              </Space>
            </Dropdown>
          </Space>
        </Header>

        <Content style={{ minWidth: 0 }}>
          <PageTransition routeKey={location.pathname}>
            <Outlet />
          </PageTransition>
        </Content>
      </Layout>
    </Layout>
  )
}

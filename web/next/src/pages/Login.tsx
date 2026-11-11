import { useState } from 'react'
import { Button, Card, Form, Input, Typography, App as AntApp, theme as antdTheme } from 'antd'
import { LockOutlined, UserOutlined } from '@ant-design/icons'
import { Navigate, useNavigate } from 'react-router'
import { useAuth } from '../auth'
import { useI18n } from '../i18n'

/** Sign-in screen: the only route reachable without a session. */
export default function Login() {
  const { status, login } = useAuth()
  const navigate = useNavigate()
  const { message } = AntApp.useApp()
  const { token } = antdTheme.useToken()
  const { lang, toggle: toggleLang } = useI18n()
  const [busy, setBusy] = useState(false)

  if (status === 'authed') return <Navigate to="/" replace />

  async function onFinish(values: { username: string; password: string }) {
    setBusy(true)
    try {
      await login(values.username.trim(), values.password)
      navigate('/', { replace: true })
    } catch (err: any) {
      message.error(err?.message || '登录失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div
      style={{
        minHeight: '100dvh',
        display: 'grid',
        placeItems: 'center',
        background: token.colorBgLayout,
        padding: 20
      }}
    >
      <div style={{ width: 380, maxWidth: '100%' }}>
        <Card style={{ width: '100%' }} variant="borderless">
        <div style={{ textAlign: 'center', marginBottom: 22 }}>
          <div
            style={{
              width: 44,
              height: 44,
              margin: '0 auto 12px',
              borderRadius: 11,
              background: 'linear-gradient(135deg, #4096ff, #1677ff 55%, #9254de)',
              display: 'grid',
              placeItems: 'center',
              color: '#fff',
              fontSize: 20,
              fontWeight: 700
            }}
          >
            W
          </div>
          <Typography.Title level={4} style={{ margin: 0 }}>
            Wormgate 控制台
          </Typography.Title>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            服务端管理 · 新界面预览
          </Typography.Text>
        </div>

        <Form layout="vertical" onFinish={onFinish} requiredMark={false}>
          <Form.Item name="username" rules={[{ required: true, message: '请输入用户名' }]}>
            <Input size="large" prefix={<UserOutlined />} placeholder="用户名" autoComplete="username" autoFocus />
          </Form.Item>
          <Form.Item name="password" rules={[{ required: true, message: '请输入密码' }]}>
            <Input.Password size="large" prefix={<LockOutlined />} placeholder="密码" autoComplete="current-password" />
          </Form.Item>
          <Button type="primary" htmlType="submit" size="large" block loading={busy}>
            登录
          </Button>
        </Form>

        <div style={{ marginTop: 14, textAlign: 'center' }}>
          {/* Chinese runs the two clauses together; English needs a space
              between the question and the link, or they read as one word
              ("Need the legacy UI?Open the Vue console"). */}
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            需要旧版界面？
            {lang === 'en' ? ' ' : null}
            <a href="/admin/">切回 Vue 控制台</a>
          </Typography.Text>
        </div>
        </Card>

        {/* Sits outside the card, mirroring the Vue sign-in page: language is a
            page-level affordance, not part of the form. */}
        <div style={{ textAlign: 'center', marginTop: 10 }}>
          <Button
            type="text"
            size="small"
            onClick={toggleLang}
            aria-label={lang === 'zh' ? 'Switch to English' : 'Switch to Chinese'}
          >
            <span data-i18n-skip style={{ fontSize: 11, opacity: 0.75 }}>
              中 / EN
            </span>
          </Button>
        </div>
      </div>
    </div>
  )
}

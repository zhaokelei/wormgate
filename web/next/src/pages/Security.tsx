import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Alert,
  Button,
  Card,
  Descriptions,
  Form,
  Input,
  Popconfirm,
  Space,
  Typography,
  message
} from 'antd'
import {
  CopyOutlined,
  EyeInvisibleOutlined,
  EyeOutlined,
  KeyOutlined,
  LockOutlined,
  ReloadOutlined,
  ThunderboltOutlined,
  UnlockOutlined
} from '@ant-design/icons'

import { api } from '../api'
import { useAuth } from '../auth'
import SoftTag from '../components/SoftTag'
import { translate, useI18n } from '../i18n'

/**
 * The two long explanations live in constants so their wording matches the
 * catalogue exactly. JSX would merge the source lines into one text node with
 * its own whitespace, and a key that differs by a single space never matches —
 * the paragraph then falls back to word-by-word replacement and turns into
 * "AccountPasswordSign in...".
 */
const SIGN_IN_NOTE =
  '账号密码用于登录管理台 UI，按用户存放并以 argon2id 哈希写入 users.json；客户端令牌 tx_... 用于 fpc 连接 fps 控制面，在「用户与租户」页创建或轮换。改这里不会改变任何令牌。密码修改成功后本浏览器会换发新会话，其他已登录会话立即失效并写入审计日志。'
const ENCRYPTION_NOTE = '可选的 AES-256-GCM 内层加密，位于 TLS 之内。密钥由两端共享口令派生，必须完全一致。'
const ROTATE_NOTE =
  '会话密钥由「会话 + 隧道名」派生，正在运行的客户端会继续使用旧密钥直到重连；客户端读取到新的 stream_secret 后会自动重建隧道。'

/**
 * Security page: two unrelated things sharing a screen.
 *
 *  1. Self-service password change for the signed-in account (any role).
 *     Resetting *somebody else's* password stays admin-only on Users.
 *
 *  2. The stream-encryption key (server.stream_secret, admin only). It keys the
 *     optional AES-256-GCM layer inside each tunnel stream and must be
 *     byte-identical on both ends, so it is shown in clear with copy/generate —
 *     masking it would only push the operator to SSH.
 */
export default function Security() {
  const { isAdmin, user } = useAuth()
  const { lang } = useI18n()
  const qc = useQueryClient()
  const [passwordForm] = Form.useForm()
  const [reveal, setReveal] = useState(false)
  const [draft, setDraft] = useState('')

  const encQuery = useQuery({
    queryKey: ['encryption'],
    queryFn: () => api('/encryption'),
    enabled: isAdmin,
    retry: false
  })
  const enc: any = encQuery.data || null

  const changePassword = useMutation({
    mutationFn: (body: { current_password: string; new_password: string }) =>
      api('/me/password', { method: 'POST', body }),
    onSuccess: () => {
      message.success('密码已更新，其他会话已失效')
      passwordForm.resetFields()
    },
    onError: (err: any) => message.error(err?.message || '修改失败')
  })

  const setEncryption = useMutation({
    mutationFn: (body: Record<string, unknown>) => api('/encryption', { method: 'PUT', body }),
    onSuccess: () => {
      message.success('已写入配置文件并热加载')
      setDraft('')
      setReveal(false)
      void qc.invalidateQueries({ queryKey: ['encryption'] })
      void qc.invalidateQueries({ queryKey: ['tunnels'] })
    },
    onError: (err: any) => message.error(err?.message || '保存失败')
  })

  const draftTrimmed = draft.trim()
  const tooShort = draftTrimmed.length > 0 && draftTrimmed.length < 16
  const matches = draftTrimmed.length > 0 && draftTrimmed === (enc?.secret || '')

  async function copySecret() {
    const value = enc?.secret || ''
    if (!value) return
    try {
      await navigator.clipboard.writeText(value)
      message.success('已复制共享口令')
    } catch {
      message.error('浏览器拒绝了剪贴板访问')
    }
  }

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h2>修改登录密码</h2>
          <p>仅用于登录服务端管理台，不影响客户端令牌。</p>
        </div>
      </div>

      <Card variant="borderless" style={{ marginBottom: 14 }}>
        <Form
          form={passwordForm}
          layout="vertical"
          style={{ maxWidth: 460 }}
          onFinish={(values) =>
            changePassword.mutate({
              current_password: values.current_password,
              new_password: values.new_password
            })
          }
        >
          <Form.Item label="账号">
            <Input value={user?.username || ''} disabled prefix={<KeyOutlined style={{ opacity: 0.5 }} />} />
          </Form.Item>
          <Form.Item
            name="current_password"
            label="当前密码"
            rules={[{ required: true, message: '请输入当前密码' }]}
          >
            <Input.Password autoComplete="current-password" />
          </Form.Item>
          <Form.Item
            name="new_password"
            label="新密码"
            rules={[
              { required: true, message: '请输入新密码' },
              { min: 8, message: '至少 8 位' }
            ]}
          >
            <Input.Password autoComplete="new-password" />
          </Form.Item>
          <Form.Item
            name="confirm"
            label="确认新密码"
            dependencies={['new_password']}
            rules={[
              { required: true, message: '请再次输入新密码' },
              ({ getFieldValue }) => ({
                validator: (_r, value) =>
                  !value || getFieldValue('new_password') === value
                    ? Promise.resolve()
                    : Promise.reject(new Error('两次输入不一致'))
              })
            ]}
          >
            <Input.Password autoComplete="new-password" />
          </Form.Item>
          <Button type="primary" htmlType="submit" loading={changePassword.isPending}>
            更新密码
          </Button>
        </Form>
      </Card>

      <Card variant="borderless" style={{ marginBottom: 14 }}>
        <Typography.Paragraph style={{ fontSize: 12, opacity: 0.7, marginBottom: 0 }}>
          {SIGN_IN_NOTE}
        </Typography.Paragraph>
      </Card>

      <div className="page-head">
        <div>
          <h2>隧道加密</h2>
          <p>{ENCRYPTION_NOTE}</p>
        </div>
        <span style={{ flex: 1 }} />
        {enc?.enabled ? <SoftTag tone="green">已启用</SoftTag> : <SoftTag>未启用</SoftTag>}
      </div>

      {!isAdmin ? (
        <Card variant="borderless">
          <Typography.Text type="secondary">只有管理员可以查看和修改隧道加密口令。</Typography.Text>
        </Card>
      ) : encQuery.isError ? (
        <Alert
          type="error"
          showIcon
          message="读取加密配置失败"
          description={(encQuery.error as any)?.message}
          action={
            <Button size="small" onClick={() => encQuery.refetch()} icon={<ReloadOutlined />}>
              重试
            </Button>
          }
        />
      ) : (
        <Card variant="borderless" loading={encQuery.isLoading}>
          <Descriptions
            size="small"
            column={{ xs: 1, md: 2 }}
            items={[
              {
                key: 'state',
                label: '状态',
                children: enc?.enabled ? (
                  <Space size={6}>
                    <LockOutlined style={{ color: '#52c41a' }} />
                    已启用
                  </Space>
                ) : (
                  <Space size={6}>
                    <UnlockOutlined style={{ opacity: 0.5 }} />
                    未启用（隧道为明文，仍在 TLS 之内）
                  </Space>
                )
              },
              { key: 'fp', label: '密钥指纹', children: <span className="mono">{enc?.fingerprint || '--'}</span> },
              { key: 'enc', label: '加密隧道', children: `${enc?.tunnels_encrypted ?? 0} 条` },
              { key: 'plain', label: '明文隧道', children: `${enc?.tunnels_plain ?? 0} 条` },
              {
                key: 'path',
                label: '配置文件',
                span: 2,
                children: <span className="mono">{enc?.config_path || '--'}</span>
              }
            ]}
          />

          <div style={{ marginTop: 14 }}>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              共享口令
            </Typography.Text>
            <Space.Compact style={{ width: '100%', marginTop: 6 }}>
              {/* An <input value> is not a text node, so the DOM translator
                  never reaches it: translate the label text directly. */}
              <Input
                readOnly
                value={enc?.enabled ? (reveal ? enc?.secret || '' : '••••••••••••••••') : translate('未启用', lang)}
                className="mono"
                addonAfter={
                  enc?.enabled ? (
                    <a onClick={() => setReveal((v) => !v)}>
                      {reveal ? <EyeInvisibleOutlined /> : <EyeOutlined />}
                    </a>
                  ) : null
                }
              />
              <Button icon={<CopyOutlined />} disabled={!enc?.enabled} onClick={copySecret}>
                复制
              </Button>
            </Space.Compact>
          </div>

          <div style={{ marginTop: 16 }}>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              设置新口令（至少 16 位；客户端必须填同一串）
            </Typography.Text>
            <Space.Compact style={{ width: '100%', marginTop: 6 }}>
              <Input
                value={draft}
                onChange={(e) => setDraft(e.target.value)}
                placeholder="输入新的共享口令"
                status={tooShort ? 'error' : matches ? 'warning' : undefined}
                className="mono"
              />
              <Button
                type="primary"
                disabled={!draftTrimmed || tooShort}
                loading={setEncryption.isPending}
                onClick={() => setEncryption.mutate({ secret: draftTrimmed })}
              >
                保存
              </Button>
            </Space.Compact>
            {tooShort && (
              <Typography.Text type="danger" style={{ fontSize: 11 }}>
                {`至少 16 位，当前 ${draftTrimmed.length} 位。`}
              </Typography.Text>
            )}
            {matches && (
              <Typography.Text type="warning" style={{ fontSize: 11 }}>
                与当前口令相同，保存不会产生变化。
              </Typography.Text>
            )}
          </div>

          <Space style={{ marginTop: 16 }} wrap>
            <Button
              icon={<ThunderboltOutlined />}
              loading={setEncryption.isPending}
              onClick={() => setEncryption.mutate({ generate: true })}
            >
              生成随机口令
            </Button>
            <Popconfirm
              title="关闭隧道加密"
              description="已开启加密的客户端会注册失败，直到它们也取消勾选。"
              okText="关闭"
              cancelText="取消"
              okButtonProps={{ danger: true }}
              onConfirm={() => setEncryption.mutate({ disable: true })}
            >
              <Button danger disabled={!enc?.enabled} icon={<UnlockOutlined />}>
                关闭加密
              </Button>
            </Popconfirm>
            <Button icon={<ReloadOutlined className="spin-on-click" />} onClick={() => encQuery.refetch()}>
              重新读取
            </Button>
          </Space>

          <Alert
            type="info"
            showIcon
            style={{ marginTop: 16 }}
            message="改口令后必须让客户端重连"
            description={ROTATE_NOTE}
          />
        </Card>
      )}
    </div>
  )
}

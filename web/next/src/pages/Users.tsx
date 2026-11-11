import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  App as AntApp,
  Avatar,
  Button,
  Card,
  Form,
  Input,
  InputNumber,
  Modal,
  Select,
  Space,
  Switch,
  Table,
  Tooltip,
  Typography
} from 'antd'
import {
  DeleteOutlined,
  DownloadOutlined,
  EditOutlined,
  EyeOutlined,
  KeyOutlined,
  LockOutlined,
  PlusOutlined,
  ReloadOutlined
} from '@ant-design/icons'

import { api } from '../api'
import SoftTag from '../components/SoftTag'

interface UserRow {
  id: string
  username: string
  display_name?: string
  role: string
  disabled: boolean
  token_viewable: boolean
  has_token: boolean
  has_password: boolean
  oauth: boolean
  quota?: Record<string, number>
}

const ROLE_LABEL: Record<string, { text: string; color: string }> = {
  admin: { text: '管理员', color: 'blue' },
  user: { text: '用户', color: 'default' },
  viewer: { text: '只读', color: 'purple' }
}

const ROLE_OPTIONS = [
  { value: 'admin', label: '管理员 · 全部权限' },
  { value: 'user', label: '用户 · 管理自己的隧道' },
  { value: 'viewer', label: '只读 · 仅查看与审计' }
]

/** Users & tenants: token handling is the sensitive part, so it is explicit. */
export default function Users() {
  const { message, modal } = AntApp.useApp()
  const qc = useQueryClient()
  const [secret, setSecret] = useState<{ title: string; value: string; note?: string } | null>(null)
  const [exported, setExported] = useState<{ name: string; yaml: string; filename: string } | null>(null)
  const [editing, setEditing] = useState<UserRow | 'new' | null>(null)
  const [form] = Form.useForm()
  const [pwTarget, setPwTarget] = useState<UserRow | null>(null)
  const [pwForm] = Form.useForm()

  const { data, isLoading } = useQuery({
    queryKey: ['users'],
    queryFn: () => api('/users')
  })

  const rows: UserRow[] = data?.users || []

  const reveal = useMutation({
    mutationFn: (u: UserRow) => api(`/users/${u.id}/token`),
    onSuccess: (payload: any) =>
      setSecret({
        title: '当前客户端令牌',
        value: payload.token,
        note: '这是该账号正在使用的令牌；要作废请用「轮换」。每次查看都会写审计。'
      }),
    onError: (err: any) => message.error(err?.message || '查看令牌失败')
  })

  const rotate = useMutation({
    mutationFn: (u: UserRow) => api(`/users/${u.id}/rotate-token`, { method: 'POST', body: {} }),
    onSuccess: (payload: any) => {
      setSecret({
        title: '新的客户端令牌',
        value: payload.token,
        note: '旧令牌已立即失效，正在连接的客户端会在重连时被拒绝（401）。'
      })
      void qc.invalidateQueries({ queryKey: ['users'] })
    },
    onError: (err: any) => message.error(err?.message || '轮换失败')
  })

  const exportClient = useMutation({
    mutationFn: (u: UserRow) => api(`/users/${u.id}/export`),
    onSuccess: (payload: any, u) =>
      setExported({
        name: u.username,
        yaml: payload.yaml,
        filename: payload.filename || `tunnelx-client-${u.username}.yaml`
      }),
    onError: (err: any) => message.error(err?.message || '导出失败（旧令牌需先轮换一次）')
  })

  function confirmRotate(u: UserRow) {
    modal.confirm({
      title: `轮换 ${u.username} 的令牌？`,
      content: '旧令牌立即失效，该账号下所有客户端会在重连时被拒绝，隧道随之中断，直到把新令牌填进客户端。',
      okText: '轮换',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: () => rotate.mutateAsync(u)
    })
  }

  // -- create / edit / delete / password -----------------------------------
  const save = useMutation({
    mutationFn: (body: any) => {
      const isNew = editing === 'new'
      const quota = {
        max_tunnels: Number(body.max_tunnels) || 0,
        max_connections: Number(body.max_connections) || 0,
        daily_traffic_bytes: Number(body.daily_traffic_gb || 0) * 1024 * 1024 * 1024
      }
      if (isNew) {
        return api('/users', {
          method: 'POST',
          body: {
            username: body.username.trim(),
            display_name: body.display_name || '',
            password: body.password || '',
            role: body.role,
            quota
          }
        })
      }
      const target = editing as UserRow
      return api(`/users/${target.id}`, {
        method: 'PATCH',
        body: {
          display_name: body.display_name || '',
          role: body.role,
          disabled: !!body.disabled,
          quota
        }
      })
    },
    onSuccess: () => {
      message.success(editing === 'new' ? '账号已创建' : '已保存')
      setEditing(null)
      void qc.invalidateQueries({ queryKey: ['users'] })
    },
    onError: (err: any) => message.error(err?.message || '保存失败')
  })

  const remove = useMutation({
    mutationFn: (u: UserRow) => api(`/users/${u.id}`, { method: 'DELETE' }),
    onSuccess: () => {
      message.success('账号已删除')
      void qc.invalidateQueries({ queryKey: ['users'] })
    },
    onError: (err: any) => message.error(err?.message || '删除失败')
  })

  const setPassword = useMutation({
    mutationFn: (body: { id: string; password: string }) =>
      api(`/users/${body.id}/password`, { method: 'POST', body: { password: body.password } }),
    onSuccess: () => {
      message.success('密码已重置')
      setPwTarget(null)
      pwForm.resetFields()
    },
    onError: (err: any) => message.error(err?.message || '重置失败')
  })

  function openEditor(u: UserRow | 'new') {
    setEditing(u)
    const init =
      u === 'new'
        ? { role: 'user', display_name: '', max_tunnels: 0, max_connections: 0, daily_traffic_gb: 0, disabled: false }
        : {
            username: u.username,
            display_name: u.display_name || '',
            role: u.role,
            disabled: u.disabled,
            max_tunnels: u.quota?.max_tunnels || 0,
            max_connections: u.quota?.max_connections || 0,
            daily_traffic_gb: Number(((u.quota?.daily_traffic_bytes || 0) / 1024 ** 3).toFixed(2))
          }
    form.setFieldsValue(init)
  }

  function confirmDelete(u: UserRow) {
    modal.confirm({
      title: `删除账号 ${u.username}？`,
      content: '该账号的令牌立即失效，正在连接的客户端会被拒绝。审计记录会保留。',
      okText: '删除',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: () => remove.mutateAsync(u)
    })
  }

  function download() {
    if (!exported) return
    const blob = new Blob([exported.yaml], { type: 'text/yaml;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = exported.filename
    a.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  }

  const columns = [
    {
      title: '账号',
      key: 'name',
      render: (_: any, u: UserRow) => (
        <Space>
          <Avatar size={26} style={{ background: '#1677ff' }}>
            {(u.display_name || u.username).slice(0, 1).toUpperCase()}
          </Avatar>
          <span>
            <div style={{ fontWeight: 600 }}>{u.username}</div>
            {u.display_name && (
              <Typography.Text type="secondary" style={{ fontSize: 11 }}>
                {u.display_name}
              </Typography.Text>
            )}
          </span>
        </Space>
      )
    },
    {
      title: '角色',
      dataIndex: 'role',
      key: 'role',
      width: 100,
      render: (role: string) => {
        const meta = ROLE_LABEL[role] || { text: role, color: 'default' }
        return <SoftTag tone={meta.color}>{meta.text}</SoftTag>
      }
    },
    {
      title: '状态',
      key: 'status',
      width: 100,
      render: (_: any, u: UserRow) =>
        u.disabled ? <SoftTag tone="red">已停用</SoftTag> : <SoftTag tone="green">正常</SoftTag>
    },
    {
      title: '令牌',
      key: 'token',
      width: 110,
      render: (_: any, u: UserRow) =>
        u.token_viewable ? <SoftTag tone="green">可查看</SoftTag> : <SoftTag>需轮换</SoftTag>
    },
    {
      title: '操作',
      key: 'actions',
      width: 210,
      render: (_: any, u: UserRow) => (
        <Space size={2}>
          <Tooltip title={u.token_viewable ? '查看当前令牌' : '该令牌早于查看功能，轮换一次即可查看'}>
            <Button aria-label={u.token_viewable ? '查看当前令牌' : '该令牌早于查看功能，轮换一次即可查看'}
              type="text"
              size="small"
              icon={<EyeOutlined />}
              disabled={!u.token_viewable}
              loading={reveal.isPending && reveal.variables?.id === u.id}
              onClick={() => reveal.mutate(u)}
            />
          </Tooltip>
          <Tooltip title="导出客户端配置（地址 + 令牌，可直接导入客户端）">
            <Button aria-label="导出客户端配置（地址 + 令牌，可直接导入客户端）"
              type="text"
              size="small"
              icon={<DownloadOutlined />}
              loading={exportClient.isPending && exportClient.variables?.id === u.id}
              onClick={() => exportClient.mutate(u)}
            />
          </Tooltip>
          <Tooltip title="轮换令牌">
            <Button aria-label="轮换令牌"
              type="text"
              size="small"
              icon={<KeyOutlined />}
              loading={rotate.isPending && rotate.variables?.id === u.id}
              onClick={() => confirmRotate(u)}
            />
          </Tooltip>
          <Tooltip title="编辑账号">
            <Button aria-label="编辑账号" type="text" size="small" icon={<EditOutlined />} onClick={() => openEditor(u)} />
          </Tooltip>
          <Tooltip title={u.oauth ? 'OAuth 账号没有本地密码' : '重置登录密码'}>
            <Button aria-label={u.oauth ? 'OAuth 账号没有本地密码' : '重置登录密码'}
              type="text"
              size="small"
              icon={<LockOutlined />}
              disabled={u.oauth}
              onClick={() => {
                setPwTarget(u)
                pwForm.resetFields()
              }}
            />
          </Tooltip>
          <Tooltip title="删除账号">
            <Button aria-label="删除账号" type="text" size="small" danger icon={<DeleteOutlined />} onClick={() => confirmDelete(u)} />
          </Tooltip>
        </Space>
      )
    }
  ]

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h2>用户与租户</h2>
          <p>每个账号对应一个客户端令牌；查看与导出都会写审计</p>
        </div>
        <span style={{ flex: 1 }} />
        <Space size={8}>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => openEditor('new')}>
            新建账号
          </Button>
          <Button icon={<ReloadOutlined className="spin-on-click" />} onClick={() => qc.invalidateQueries({ queryKey: ['users'] })}>
            刷新
          </Button>
        </Space>
      </div>

      <Card variant="borderless">
        <Table
          rowKey="id"
          size="middle"
          scroll={{ x: 'max-content' }}
          loading={isLoading}
          dataSource={rows}
          columns={columns as any}
          pagination={false}
        />
      </Card>

      <Modal
        open={!!secret}
        title={secret?.title}
        onCancel={() => setSecret(null)}
        onOk={() => setSecret(null)}
        okText="已记录"
        cancelButtonProps={{ style: { display: 'none' } }}
        destroyOnHidden
      >
        <Typography.Paragraph type="secondary" style={{ fontSize: 12 }}>
          {secret?.note}
        </Typography.Paragraph>
        <Typography.Paragraph className="mono" copyable style={{ wordBreak: 'break-all', marginBottom: 0 }}>
          {secret?.value}
        </Typography.Paragraph>
      </Modal>

      <Modal
        open={!!exported}
        title={`导出客户端配置 · ${exported?.name || ''}`}
        width={720}
        onCancel={() => setExported(null)}
        footer={[
          <Button key="close" onClick={() => setExported(null)}>
            关闭
          </Button>,
          <Button
            key="copy"
            onClick={async () => {
              await navigator.clipboard.writeText(exported?.yaml || '')
              message.success('已复制')
            }}
          >
            复制
          </Button>,
          <Button key="dl" type="primary" icon={<DownloadOutlined />} onClick={download}>
            下载 .yaml
          </Button>
        ]}
      >
        <Typography.Paragraph type="secondary" style={{ fontSize: 12 }}>
          在客户端控制台「配置 → 一键导入」选择该文件即可完成地址与令牌配置。文件含令牌，等同于密码。
        </Typography.Paragraph>
        <pre
          className="mono"
          style={{
            margin: 0,
            padding: 12,
            borderRadius: 8,
            maxHeight: 320,
            overflow: 'auto',
            fontSize: 12,
            lineHeight: 1.7,
            background: 'rgba(127,127,127,.08)'
          }}
        >
          {exported?.yaml}
        </pre>
      </Modal>

      {/* create / edit */}
      <Modal
        open={!!editing}
        title={editing === 'new' ? '新建账号' : `编辑 ${(editing as UserRow)?.username || ''}`}
        okText={editing === 'new' ? '创建' : '保存'}
        cancelText="取消"
        confirmLoading={save.isPending}
        onCancel={() => setEditing(null)}
        onOk={() => form.submit()}
        destroyOnHidden
      >
        <Form form={form} layout="vertical" onFinish={(v) => save.mutate(v)} preserve={false}>
          {editing === 'new' && (
            <Form.Item
              name="username"
              label="用户名"
              rules={[
                { required: true, message: '请输入用户名' },
                { pattern: /^[a-zA-Z0-9._-]{3,32}$/, message: '3-32 位字母、数字、点、下划线或短横线' }
              ]}
            >
              <Input autoComplete="off" placeholder="例如 demo" />
            </Form.Item>
          )}
          <Form.Item name="display_name" label="显示名">
            <Input placeholder="可选，用于界面展示" />
          </Form.Item>
          {editing === 'new' && (
            <Form.Item name="password" label="登录密码" rules={[{ min: 8, message: '至少 8 位' }]}>
              <Input.Password autoComplete="new-password" placeholder="留空则不设密码（仅令牌使用）" />
            </Form.Item>
          )}
          <Form.Item name="role" label="角色" rules={[{ required: true, message: '请选择角色' }]}>
            <Select options={ROLE_OPTIONS} />
          </Form.Item>
          {editing !== 'new' && (
            <Form.Item name="disabled" label="停用账号" valuePropName="checked">
              <Switch />
            </Form.Item>
          )}
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            配额（0 表示不限）
          </Typography.Text>
          <Space size={8} style={{ width: '100%', marginTop: 6 }}>
            <Form.Item name="max_tunnels" label="隧道数" style={{ marginBottom: 0 }}>
              <InputNumber min={0} style={{ width: 120 }} />
            </Form.Item>
            <Form.Item name="max_connections" label="并发连接" style={{ marginBottom: 0 }}>
              <InputNumber min={0} style={{ width: 120 }} />
            </Form.Item>
            <Form.Item name="daily_traffic_gb" label="日流量 GB" style={{ marginBottom: 0 }}>
              <InputNumber min={0} step={1} style={{ width: 130 }} />
            </Form.Item>
          </Space>
        </Form>
      </Modal>

      {/* reset password */}
      <Modal
        open={!!pwTarget}
        title={`重置 ${pwTarget?.username || ''} 的登录密码`}
        okText="重置"
        cancelText="取消"
        confirmLoading={setPassword.isPending}
        onCancel={() => setPwTarget(null)}
        onOk={() => pwForm.submit()}
        destroyOnHidden
      >
        <Form
          form={pwForm}
          layout="vertical"
          onFinish={(v) => pwTarget && setPassword.mutate({ id: pwTarget.id, password: v.password })}
        >
          <Form.Item
            name="password"
            label="新密码"
            rules={[
              { required: true, message: '请输入新密码' },
              { min: 8, message: '至少 8 位' }
            ]}
          >
            <Input.Password autoComplete="new-password" />
          </Form.Item>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            重置后该账号的旧会话立即失效；客户端令牌不受影响。
          </Typography.Text>
        </Form>
      </Modal>
    </div>
  )
}

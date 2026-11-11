import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Alert,
  Badge,
  Button,
  Card,
  Empty,
  Input,
  Modal,
  Space,
  Table,
  Tooltip,
  Typography,
  message
} from 'antd'
import { PoweroffOutlined, ReloadOutlined, SearchOutlined, WifiOutlined } from '@ant-design/icons'

import { api } from '../api'
import { useAuth } from '../auth'
import { formatUptime } from '../format'
import SoftTag from '../components/SoftTag'

/**
 * Connected clients = one authenticated control WebSocket each.
 *
 * `data_ready` is the field operators misread most: a client can hold the
 * control channel open while its yamux data plane never attached, in which case
 * the tunnel is registered but no byte can flow. It gets its own column.
 */
export default function Clients() {
  const { isAdmin, user } = useAuth()
  const qc = useQueryClient()
  const [query, setQuery] = useState('')
  const [pendingKick, setPendingKick] = useState<any | null>(null)

  const { data, isLoading, isFetching, refetch } = useQuery({
    queryKey: ['clients'],
    queryFn: () => api('/clients'),
    refetchInterval: 5000
  })

  const kick = useMutation({
    mutationFn: (id: string) => api(`/clients/${encodeURIComponent(id)}/kick`, { method: 'POST', body: {} }),
    onSuccess: (_r, id) => {
      message.success('已强制下线')
      setPendingKick(null)
      void qc.invalidateQueries({ queryKey: ['clients'] })
      void id
    },
    onError: (err: any) => message.error(err?.message || '下线失败')
  })

  const clients: any[] = data?.clients || []

  const rows = useMemo(() => {
    const q = query.trim().toLowerCase()
    const now = Date.now()
    return clients
      // Age is frozen at render time: recomputing every second would churn the
      // table without adding information, and the poll refreshes it anyway.
      .map((c) => ({ ...c, age: (now - new Date(c.connected_at).getTime()) / 1000 }))
      .filter((c) =>
        !q
          ? true
          : [c.client_name, c.owner, c.remote_addr, c.session_id]
              .filter(Boolean)
              .some((v: string) => String(v).toLowerCase().includes(q))
      )
      .sort((a, b) => new Date(b.connected_at).getTime() - new Date(a.connected_at).getTime())
  }, [clients, query])

  const readyCount = clients.filter((c) => c.data_ready).length

  const columns = [
    {
      title: '客户端',
      key: 'name',
      render: (_: any, c: any) => (
        <Space size={8}>
          <Badge status={c.data_ready ? 'success' : 'warning'} />
          <div style={{ minWidth: 0 }}>
            <div style={{ fontWeight: 600 }}>{c.client_name || '未命名客户端'}</div>
            <Typography.Text type="secondary" className="mono" style={{ fontSize: 11 }}>
              {String(c.session_id).slice(0, 12)}
            </Typography.Text>
          </div>
        </Space>
      )
    },
    { title: '属主', dataIndex: 'owner', key: 'owner', width: 110, render: (v: string) => <SoftTag>{v}</SoftTag> },
    {
      title: '来源地址',
      dataIndex: 'remote_addr',
      key: 'addr',
      width: 170,
      render: (v: string) => (
        <Typography.Text className="mono" style={{ fontSize: 12 }}>
          {v}
        </Typography.Text>
      )
    },
    { title: '隧道', dataIndex: 'tunnels', key: 'tunnels', width: 80, align: 'right' as const },
    {
      title: '数据面',
      dataIndex: 'data_ready',
      key: 'data',
      width: 100,
      render: (v: boolean) =>
        v ? <SoftTag tone="green">已就绪</SoftTag> : <SoftTag tone="orange">未就绪</SoftTag>
    },
    {
      title: '在线时长',
      dataIndex: 'age',
      key: 'age',
      width: 120,
      render: (v: number) => <span className="mono">{formatUptime(v)}</span>
    },
    {
      title: '最近心跳',
      dataIndex: 'last_seen',
      key: 'seen',
      width: 110,
      render: (v: string) => (
        <Typography.Text type="secondary" style={{ fontSize: 11 }}>
          {v ? new Date(v).toLocaleTimeString('zh-CN') : '--'}
        </Typography.Text>
      )
    },
    {
      title: '操作',
      key: 'actions',
      width: 80,
      align: 'center' as const,
      render: (_: any, c: any) =>
        isAdmin || c.owner_id === user?.id ? (
          <Tooltip title="强制下线">
            <Button aria-label="强制下线" danger type="text" size="small" icon={<PoweroffOutlined />} onClick={() => setPendingKick(c)} />
          </Tooltip>
        ) : (
          <Typography.Text type="secondary" style={{ fontSize: 11 }}>
            只读
          </Typography.Text>
        )
    }
  ]

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h2>在线客户端</h2>
          <p>
            {`${clients.length} 个会话 · 数据面就绪 ${readyCount} 个 · 每 5 秒刷新`}
          </p>
        </div>
        <span style={{ flex: 1 }} />
        <Input
          allowClear
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="搜索名称 / 属主 / 来源 IP"
          prefix={<SearchOutlined style={{ opacity: 0.5 }} />}
          style={{ width: 240 }}
        />
        <Button icon={<ReloadOutlined className="spin-on-click" />} loading={isFetching} onClick={() => refetch()}>
          刷新
        </Button>
      </div>

      {clients.length > 0 && readyCount < clients.length && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12 }}
          message={`${clients.length - readyCount} 个会话的数据面未就绪`}
          description="控制通道已连上但数据面没有附着，这些会话上的隧道无法转发流量。通常是客户端刚重连，或 yamux 通道被中间设备阻断。"
        />
      )}

      <Card variant="borderless">
        <Table
          rowKey="session_id"
          size="middle"
          scroll={{ x: 'max-content' }}
          loading={isLoading}
          dataSource={rows}
          columns={columns as any}
          pagination={false}
          locale={{
            emptyText: (
              <Empty
                image={<WifiOutlined style={{ fontSize: 34, opacity: 0.25 }} />}
                description={clients.length ? '没有匹配的客户端' : '当前没有在线客户端'}
              />
            )
          }}
        />
      </Card>

      <Modal
        open={!!pendingKick}
        title="强制下线客户端"
        okText="确认下线"
        okButtonProps={{ danger: true, loading: kick.isPending }}
        cancelText="取消"
        onCancel={() => setPendingKick(null)}
        onOk={() => pendingKick && kick.mutate(pendingKick.session_id)}
      >
        <p>
          {pendingKick?.client_name}（{pendingKick?.remote_addr}）
        </p>
        <Typography.Paragraph type="secondary" style={{ fontSize: 12 }}>
          将关闭控制通道并撤销该会话的全部隧道，客户端会收到下线原因并按退避策略重连。操作会写入审计日志。
        </Typography.Paragraph>
      </Modal>
    </div>
  )
}

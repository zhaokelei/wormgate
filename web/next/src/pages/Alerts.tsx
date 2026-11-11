import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Badge,
  Button,
  Card,
  Empty,
  Segmented,
  Space,
  Table,
  Typography,
  message
} from 'antd'
import { BellOutlined, CheckOutlined, ReloadOutlined } from '@ant-design/icons'

import { api } from '../api'
import { useAuth } from '../auth'
import SoftTag from '../components/SoftTag'

/**
 * Alerts, newest first.
 *
 * Only server-side failures reach this list: routine client disconnects and
 * administrator-initiated kicks are classified as benign server-side, so an
 * operator can treat every unacked row as actionable. Acking is admin-only.
 */
const LEVELS = [
  { label: '全部', value: 'all' },
  { label: '严重', value: 'error' },
  { label: '警告', value: 'warning' },
  { label: '信息', value: 'info' }
] as const

const TONE: Record<string, string> = { error: 'red', warning: 'gold', info: 'blue' }
const LABEL: Record<string, string> = { error: '严重', warning: '警告', info: '信息' }

export default function Alerts() {
  const { isAdmin } = useAuth()
  const qc = useQueryClient()
  const [level, setLevel] = useState<string>('all')

  const { data, isLoading, refetch, isFetching } = useQuery({
    queryKey: ['alerts'],
    queryFn: () => api('/alerts'),
    refetchInterval: 5000
  })

  const ack = useMutation({
    mutationFn: (id: string) => api(`/alerts/${encodeURIComponent(id)}/ack`, { method: 'POST', body: {} }),
    onSuccess: () => {
      message.success('已确认')
      void qc.invalidateQueries({ queryKey: ['alerts'] })
    },
    onError: (err: any) => message.error(err?.message || '确认失败')
  })

  const alerts: any[] = data?.alerts || []
  const rows = useMemo(
    () => (level === 'all' ? alerts : alerts.filter((a) => a.level === level)),
    [alerts, level]
  )
  const unacked = alerts.filter((a) => !a.ack).length

  const columns = [
    {
      title: '级别',
      dataIndex: 'level',
      key: 'level',
      width: 90,
      render: (v: string) => <SoftTag tone={TONE[v] || 'default'}>{LABEL[v] || v}</SoftTag>
    },
    {
      title: '事件',
      key: 'event',
      width: 300,
      render: (_: any, a: any) => (
        <div>
          <Typography.Text code style={{ fontSize: 11 }}>
            {a.code}
          </Typography.Text>
          <div style={{ fontSize: 13, marginTop: 2 }}>{a.message}</div>
        </div>
      )
    },
    {
      title: '时间',
      dataIndex: 'time',
      key: 'time',
      width: 160,
      render: (v: string) => (
        <Typography.Text type="secondary" className="mono" style={{ fontSize: 11 }}>
          {new Date(v).toLocaleString('zh-CN')}
        </Typography.Text>
      )
    },
    {
      title: '状态',
      dataIndex: 'ack',
      key: 'ack',
      width: 100,
      render: (v: boolean) =>
        v ? (
          <Badge status="default" text="已确认" />
        ) : (
          <Badge status="processing" color={undefined} text="待处理" />
        )
    },
    {
      title: '操作',
      key: 'actions',
      width: 90,
      align: 'center' as const,
      render: (_: any, a: any) =>
        a.ack ? (
          <Typography.Text type="secondary" style={{ fontSize: 11 }}>
            --
          </Typography.Text>
        ) : isAdmin ? (
          <Button
            size="small"
            type="text"
            icon={<CheckOutlined />}
            loading={ack.isPending && ack.variables === a.id}
            onClick={() => ack.mutate(a.id)}
          >
            确认
          </Button>
        ) : (
          <Typography.Text type="secondary" style={{ fontSize: 11 }}>
            需管理员
          </Typography.Text>
        )
    }
  ]

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h2>告警</h2>
          <p>
            {`共 ${alerts.length} 条 · 待处理 ${unacked} 条 · 每 5 秒刷新`}
          </p>
        </div>
        <span style={{ flex: 1 }} />
        <Space size={8}>
          <Segmented options={LEVELS as any} value={level} onChange={(v) => setLevel(String(v))} />
          <Button icon={<ReloadOutlined className="spin-on-click" />} loading={isFetching} onClick={() => refetch()}>
            刷新
          </Button>
        </Space>
      </div>

      <Card variant="borderless">
        <Table
          rowKey="id"
          size="middle"
          loading={isLoading}
          dataSource={rows}
          columns={columns as any}
          pagination={{ pageSize: 15, hideOnSinglePage: true }}
          locale={{
            emptyText: (
              <Empty
                image={<BellOutlined style={{ fontSize: 34, opacity: 0.25 }} />}
                description={alerts.length ? '该级别下没有告警' : '没有告警，服务端运行正常'}
              />
            )
          }}
        />
      </Card>
    </div>
  )
}

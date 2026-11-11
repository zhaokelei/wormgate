import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  Card,
  Input,
  Space,
  Table,
  Typography
} from 'antd'
import { FileTextOutlined, ReloadOutlined, SearchOutlined } from '@ant-design/icons'

import { api } from '../api'
import SoftTag from '../components/SoftTag'

/**
 * Audit trail. Every mutating admin action lands here with actor, target and
 * source IP, so the table is the first place to look when something changed and
 * nobody admits to it.
 */
const SEVERITY: Record<string, string> = { info: 'blue', warning: 'gold', critical: 'red', error: 'red' }

export default function Audit() {
  const [query, setQuery] = useState('')
  const [limit, setLimit] = useState(200)

  const { data, isLoading, refetch, isFetching } = useQuery({
    queryKey: ['audit', limit],
    queryFn: () => api(`/audit?limit=${limit}`)
  })

  const events: any[] = data?.events || []
  const rows = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return events
    return events.filter((e) =>
      [e.actor, e.action, e.target, e.detail, e.ip]
        .filter(Boolean)
        .some((v: string) => String(v).toLowerCase().includes(q))
    )
  }, [events, query])

  const columns = [
    {
      title: '时间',
      dataIndex: 'time',
      key: 'time',
      width: 170,
      render: (v: string) => (
        <Typography.Text type="secondary" className="mono" style={{ fontSize: 11 }}>
          {new Date(v).toLocaleString('zh-CN')}
        </Typography.Text>
      )
    },
    {
      title: '操作者',
      dataIndex: 'actor',
      key: 'actor',
      width: 130,
      render: (v: string) => <SoftTag>{v || 'system'}</SoftTag>
    },
    {
      title: '动作',
      dataIndex: 'action',
      key: 'action',
      width: 200,
      render: (v: string) => (
        <Typography.Text code style={{ fontSize: 11 }}>
          {v}
        </Typography.Text>
      )
    },
    { title: '对象', dataIndex: 'target', key: 'target', width: 160, render: (v: string) => v || '--' },
    {
      title: '详情',
      dataIndex: 'detail',
      key: 'detail',
      render: (v: string) => (
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          {v || '--'}
        </Typography.Text>
      )
    },
    {
      title: '来源 IP',
      dataIndex: 'ip',
      key: 'ip',
      width: 140,
      render: (v: string) => (
        <Typography.Text className="mono" style={{ fontSize: 11 }}>
          {v || '--'}
        </Typography.Text>
      )
    },
    {
      title: '级别',
      dataIndex: 'severity',
      key: 'severity',
      width: 90,
      render: (v: string) => <SoftTag tone={SEVERITY[v] || 'default'}>{v}</SoftTag>
    }
  ]

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h2>审计</h2>
          <p>{`最近 ${events.length} 条操作记录`}</p>
        </div>
        <span style={{ flex: 1 }} />
        <Space size={8}>
          <Input
            allowClear
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="搜索操作者 / 动作 / 对象 / IP"
            prefix={<SearchOutlined style={{ opacity: 0.5 }} />}
            style={{ width: 260 }}
          />
          <a onClick={() => refetch()}>
            <ReloadOutlined spin={isFetching} className="spin-on-click" /> 刷新
          </a>
        </Space>
      </div>

      <Card variant="borderless">
        <Table
          rowKey="id"
          size="middle"
          loading={isLoading}
          dataSource={rows}
          columns={columns as any}
          pagination={{
            pageSize: 20,
            hideOnSinglePage: true,
            showSizeChanger: true,
            onShowSizeChange: (_p, size) => setLimit(Math.min(1000, size * 10))
          }}
          locale={{
            emptyText: <FileTextOutlined style={{ fontSize: 34, opacity: 0.25 }} />
          }}
        />
      </Card>
    </div>
  )
}

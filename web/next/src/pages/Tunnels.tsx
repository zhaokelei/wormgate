import { useQuery } from '@tanstack/react-query'
import {
  Card,
  Space,
  Table,
  Typography
} from 'antd'
import {
  LockOutlined,
  ReloadOutlined,
  SafetyOutlined,
  UnlockOutlined
} from '@ant-design/icons'

import { api } from '../api'
import { formatBytes, formatNumber } from '../format'
import SoftTag from '../components/SoftTag'

/** Registered tunnels, scoped by the server to what the account may see. */
export default function Tunnels() {
  const { data, isLoading, refetch, isFetching } = useQuery({
    queryKey: ['tunnels'],
    queryFn: () => api('/tunnels'),
    refetchInterval: 5000
  })

  const rows = data?.tunnels || []

  const columns = [
    {
      title: '隧道',
      key: 'name',
      width: 320,
      render: (_: any, t: any) => (
        <div>
          <Space size={6}>
            <strong style={{ whiteSpace: 'nowrap' }}>{t.name}</strong>
            <SoftTag>{t.type}</SoftTag>
            {/* Two separate layers, so show them separately: the control/data
                channel is always TLS, the AES-256-GCM inner layer is opt-in
                per tunnel. One lone "明文" badge read as "no encryption at
                all", which was wrong on both counts. */}
            <SoftTag
              icon={<LockOutlined />}
              title="客户端 ↔ 服务器通道：TLS 1.3，始终开启"
            >
              TLS
            </SoftTag>
            {t.encryption ? (
              <SoftTag
                tone="green"
                icon={<SafetyOutlined />}
                title="隧道内层：AES-256-GCM，密钥来自两端共享的 stream_secret"
              >
                隧道加密
              </SoftTag>
            ) : (
              <SoftTag
                tone="orange"
                icon={<UnlockOutlined />}
                title="隧道内层未加密，只有外层 TLS 保护"
              >
                内层明文
              </SoftTag>
            )}
          </Space>
          <Typography.Text type="secondary" className="mono" style={{ display: 'block', fontSize: 11 }}>
            {t.public_addr}
          </Typography.Text>
        </div>
      )
    },
    { title: '属主', dataIndex: 'owner', key: 'owner', width: 110 },
    { title: '客户端', dataIndex: 'client', key: 'client', width: 160 },
    {
      title: '活跃连接',
      dataIndex: 'active_connections',
      key: 'active',
      width: 100,
      align: 'right' as const,
      render: (v: number) => <span className="mono">{formatNumber(v)}</span>
    },
    {
      title: '入站',
      dataIndex: 'bytes_in',
      key: 'in',
      width: 110,
      align: 'right' as const,
      render: (v: number) => <span className="mono">{formatBytes(v)}</span>
    },
    {
      title: '出站',
      dataIndex: 'bytes_out',
      key: 'out',
      width: 110,
      align: 'right' as const,
      render: (v: number) => <span className="mono">{formatBytes(v)}</span>
    }
  ]

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h2>隧道</h2>
          <p>{`共 ${rows.length} 条 · 每 5 秒刷新`}</p>
        </div>
        <span style={{ flex: 1 }} />
        <a onClick={() => refetch()}>
          <ReloadOutlined spin={isFetching} className="spin-on-click" /> 刷新
        </a>
      </div>

      <Card variant="borderless">
        <Table rowKey="id" size="middle" loading={isLoading} dataSource={rows} columns={columns as any} pagination={false} />
      </Card>
    </div>
  )
}

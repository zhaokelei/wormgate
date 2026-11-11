import { useQuery } from '@tanstack/react-query'
import {
  Alert,
  Card,
  Col,
  Progress,
  Row,
  Space,
  Statistic,
  Table,
  Typography
} from 'antd'
import { ClusterOutlined, ReloadOutlined } from '@ant-design/icons'

import { api } from '../api'
import { formatBytes, formatNumber, formatUptime, percent } from '../format'
import SoftTag from '../components/SoftTag'
import { usageColor } from '../usage'

/**
 * Node inspection: host resources first (that is what an operator is paged
 * about), then the Go runtime. `host_known` is false on platforms without a
 * sampler (macOS builds, unsupported targets), and in that case the host cards
 * are suppressed rather than showing a fake zero.
 */
export default function Nodes() {
  const { data, isLoading, refetch, isFetching } = useQuery({
    queryKey: ['nodes'],
    queryFn: () => api('/nodes'),
    refetchInterval: 5000
  })

  const nodes: any[] = data?.nodes || []

  const columns = [
    {
      title: '节点',
      key: 'name',
      render: (_: any, n: any) => (
        <Space size={6}>
          <ClusterOutlined style={{ opacity: 0.6 }} />
          <strong>{n.name}</strong>
          <SoftTag>{n.kind}</SoftTag>
        </Space>
      )
    },
    { title: '版本', dataIndex: 'version', key: 'version', width: 130, render: (v: string) => <span className="mono">{v}</span> },
    {
      title: '运行时长',
      dataIndex: 'uptime_sec',
      key: 'uptime',
      width: 150,
      render: (v: number) => <span className="mono">{formatUptime(v)}</span>
    },
    { title: '客户端', dataIndex: 'clients', key: 'clients', width: 90, align: 'right' as const },
    { title: '隧道', dataIndex: 'tunnels', key: 'tunnels', width: 90, align: 'right' as const },
    {
      title: '主机 CPU',
      dataIndex: 'cpu_percent',
      key: 'cpu',
      width: 100,
      align: 'right' as const,
      render: (v: number, n: any) => (
        <span className="mono">{n.host_known ? `${Number(v || 0).toFixed(1)}%` : '--'}</span>
      )
    },
    {
      title: '主机内存',
      dataIndex: 'mem_used',
      key: 'mem',
      width: 170,
      align: 'right' as const,
      render: (v: number, n: any) => (
        <span className="mono" style={{ whiteSpace: 'nowrap' }}>{n.host_known ? `${formatBytes(v)} / ${formatBytes(n.mem_total)}` : '--'}</span>
      )
    },
    {
      title: 'Goroutine',
      dataIndex: 'goroutines',
      key: 'g',
      width: 110,
      align: 'right' as const,
      render: (v: number) => <span className="mono">{formatNumber(v)}</span>
    },
    {
      title: '堆占用',
      dataIndex: 'heap_alloc',
      key: 'heap',
      width: 120,
      align: 'right' as const,
      render: (v: number) => <span className="mono">{formatBytes(v)}</span>
    },
    { title: 'GC', dataIndex: 'num_gc', key: 'gc', width: 80, align: 'right' as const }
  ]

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h2>节点</h2>
          <p>主机资源与 Go 运行时 · 每 5 秒刷新</p>
        </div>
        <span style={{ flex: 1 }} />
        <a onClick={() => refetch()}>
          <ReloadOutlined spin={isFetching} className="spin-on-click" /> 刷新
        </a>
      </div>

      {nodes.map((n) => {
        const memPct = percent(n.mem_used, n.mem_total)
        const heapPct = percent(n.heap_alloc, n.heap_sys)
        return (
          <div key={n.name} style={{ marginBottom: 14 }}>
            {!n.host_known && (
              <Alert
                type="info"
                showIcon
                style={{ marginBottom: 12 }}
                message="此平台未提供主机采样"
                description="CPU / 内存采样依赖 /proc（Linux）或 GetSystemTimes（Windows）；当前构建只上报 Go 运行时数据。"
              />
            )}

            <Row gutter={[12, 12]}>
              {n.host_known && (
                <>
                  <Col xs={24} md={12} xl={6}>
                    <Card variant="borderless">
                      <Statistic title="主机 CPU" value={Number(n.cpu_percent || 0)} precision={1} suffix="%" />
                      <Progress
                        percent={Math.min(100, Math.round(Number(n.cpu_percent || 0)))}
                        showInfo={false}
                        size="small"
                        strokeColor={usageColor(Math.round(Number(n.cpu_percent || 0)))}
                        style={{ marginTop: 8 }}
                      />
                    </Card>
                  </Col>
                  <Col xs={24} md={12} xl={6}>
                    <Card variant="borderless">
                      <Statistic title="内存占用" value={formatBytes(n.mem_used)} suffix={`/ ${formatBytes(n.mem_total)}`} />
                      <Progress percent={memPct} showInfo={false} size="small" strokeColor={usageColor(memPct)} style={{ marginTop: 8 }} />
                      <Typography.Text type="secondary" style={{ fontSize: 11 }}>
                        {`已用 ${memPct}%`}
                      </Typography.Text>
                    </Card>
                  </Col>
                </>
              )}
              <Col xs={24} md={12} xl={n.host_known ? 6 : 12}>
                <Card variant="borderless">
                  <Statistic title="Go 堆" value={formatBytes(n.heap_alloc)} suffix={`/ ${formatBytes(n.heap_sys)}`} />
                  {/* alloc/sys is not saturation — a healthy arena sits near 100% after GC. */}
                  <Progress percent={heapPct} showInfo={false} size="small" strokeColor="#9254de" style={{ marginTop: 8 }} />
                  <Typography.Text type="secondary" style={{ fontSize: 11 }}>
                    GC 次数 {formatNumber(n.num_gc)}
                  </Typography.Text>
                </Card>
              </Col>
              <Col xs={24} md={12} xl={n.host_known ? 6 : 12}>
                <Card variant="borderless">
                  <Statistic title="Goroutine" value={formatNumber(n.goroutines)} />
                  <Typography.Text type="secondary" style={{ fontSize: 11, display: 'block', marginTop: 6 }}>
                    运行时长 {formatUptime(n.uptime_sec)}
                  </Typography.Text>
                </Card>
              </Col>
            </Row>

            <Card variant="borderless" style={{ marginTop: 12 }} loading={isLoading}>
              <Table rowKey="name" size="middle"
          scroll={{ x: 'max-content' }} dataSource={[n]} columns={columns as any} pagination={false} />
            </Card>
          </div>
        )
      })}

      {nodes.length === 0 && !isLoading && (
        <Card variant="borderless">
          <Typography.Text type="secondary">没有可上报的节点。</Typography.Text>
        </Card>
      )}
    </div>
  )
}

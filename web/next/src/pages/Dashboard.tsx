import { useEffect, useMemo, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Button, Card, Col, Progress, Row, Segmented, Table, Typography, theme as antdTheme } from 'antd'
import {
  ApiOutlined,
  ArrowDownOutlined,
  ArrowUpOutlined,
  CloudServerOutlined,
  DashboardOutlined,
  ReloadOutlined,
  ThunderboltOutlined,
  WifiOutlined
} from '@ant-design/icons'
import uPlot from 'uplot'
import 'uplot/dist/uPlot.min.css'

import { api } from '../api'
import { formatBytes, formatNumber, formatUptime, percent } from '../format'
import AnimatedNumber from '../components/AnimatedNumber'
import EmptyTraffic from '../components/EmptyTraffic'
import SoftTag from '../components/SoftTag'
import { usageColor } from '../usage'



/** Fleet snapshot, polled every 2s. */
function useOverview() {
  return useQuery({
    queryKey: ['overview'],
    queryFn: () => api('/overview'),
    refetchInterval: 2000
  })
}

/**
 * uplot traffic chart.
 *
 * uplot is what the reference panel uses: ~40 KB and it repaints two series at
 * 1 Hz without the layout cost of a full charting framework. Data is accumulated
 * client-side from the overview poll, so no extra endpoint is needed.
 */
function clockLabel(sec: number): string {
  const d = new Date(sec * 1000)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

/** Picks one unit for the whole axis so the ticks do not mix B and KB. */
function byteAxis(max: number): (n: number) => string {
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let scale = 1
  while (max / scale >= 1024 && i < units.length - 1) {
    scale *= 1024
    i += 1
  }
  return (n) => `${Math.round(n / scale)} ${units[i]}`
}

/**
 * Vertical gradient for an area series.
 *
 * uPlot accepts a CanvasGradient, so the fill can fade out with the height of
 * the plot instead of ending on a hard edge — a flat `rgba()` block reads as a
 * bar chart with a line on top, which is what made this card feel stiff.
 */
function areaGradient(u: uPlot, color: [number, number, number], top: number) {
  const { top: y0, height } = u.bbox
  const g = u.ctx.createLinearGradient(0, y0, 0, y0 + height)
  g.addColorStop(0, `rgba(${color.join(',')},${top})`)
  g.addColorStop(1, `rgba(${color.join(',')},0)`)
  return g
}

function TrafficChart({
  samples,
  rangeSec
}: {
  samples: { t: number; in: number; out: number }[]
  rangeSec: number
}) {
  const host = useRef<HTMLDivElement>(null)
  const chart = useRef<uPlot | null>(null)
  const { token } = antdTheme.useToken()

  useEffect(() => {
    if (!host.current) return
    const smooth = uPlot.paths.spline!()
    const opts: uPlot.Options = {
      width: host.current.clientWidth || 800,
      height: 240,
      padding: [12, 8, 0, 0],
      cursor: { show: true, y: false },
      // uplot's built-in legend prints "--" until the cursor hovers a point,
      // which looked like missing data. A plain footer with the latest sample
      // is clearer, and it stays in sync with the last poll.
      legend: { show: false },
      scales: {
        x: { time: true },
        // An idle tunnel graph maxes out at ~100 B, which renders a silly axis.
        y: { range: (_u, _min, max) => [0, Math.max(max, 4096)] }
      },
      axes: [
        {
          stroke: token.colorTextQuaternary,
          // Dashed hairlines read as a hint rather than a table: a solid grid
          // at this size competed with the two curves.
          grid: { stroke: token.colorBorderSecondary, width: 1, dash: [4, 6] },
          ticks: { show: false },
          font: '10px system-ui',
          // With a short window uplot asks for more splits than there are
          // whole seconds, which printed the same timestamp three times.
          space: 82,
          values: (_u, splits) => {
            const step = Math.max(1, Math.ceil(splits.length / 7))
            return splits.map((v, i) => (i % step === 0 ? clockLabel(v) : ''))
          }
        },
        {
          stroke: token.colorTextQuaternary,
          grid: { stroke: token.colorBorderSecondary, width: 1, dash: [4, 6] },
          ticks: { show: false },
          font: '10px system-ui',
          size: 58,
          values: (_u, splits) => {
            if (!splits.length) return []
            const fmt = byteAxis(Math.max(...splits))
            return splits.map(fmt)
          }
        }
      ],
      series: [
        {},
        {
          label: '入站',
          stroke: token.colorPrimary,
          width: 1.5,
          // A spline through 1 Hz samples is what stops the trace from looking
          // like a sawtooth; the points themselves stay untouched.
          paths: smooth,
          points: { show: false },
          fill: (u: uPlot) => areaGradient(u, [22, 119, 255], 0.22)
        },
        {
          label: '出站',
          stroke: '#9254de',
          width: 1.5,
          paths: smooth,
          points: { show: false },
          fill: (u: uPlot) => areaGradient(u, [146, 84, 222], 0.2)
        }
      ]
    }
    chart.current = new uPlot(opts, [[], [], []], host.current)

    const onResize = () => {
      if (!host.current || !chart.current) return
      chart.current.setSize({ width: host.current.clientWidth, height: 240 })
    }
    window.addEventListener('resize', onResize)
    return () => {
      window.removeEventListener('resize', onResize)
      chart.current?.destroy()
      chart.current = null
    }
  }, [token.colorPrimary, token.colorBorderSecondary, token.colorTextQuaternary])

  useEffect(() => {
    if (!chart.current || !samples.length) return
    const last = samples[samples.length - 1].t
    chart.current.setData([
      samples.map((s) => s.t),
      samples.map((s) => s.in),
      samples.map((s) => s.out)
    ])
    // Pin the window to the selected span instead of to the sample count:
    // two points would otherwise squash the axis into a two-second range.
    chart.current.setScale('x', { min: last - rangeSec, max: last })
  }, [samples, rangeSec])

  const last = samples[samples.length - 1]
  return (
    <div>
      <div className="u-plot-wrap" ref={host} />
      <div className="chart-legend">
        <span className="chart-legend-time">{last ? clockLabel(last.t) : '--'}</span>
        <span>
          <i style={{ background: token.colorPrimary }} />
          {`入站 ${formatBytes(last?.in ?? 0, 1)}/s`}
        </span>
        <span>
          <i style={{ background: '#9254de' }} />
          {`出站 ${formatBytes(last?.out ?? 0, 1)}/s`}
        </span>
      </div>
    </div>
  )
}

export default function Dashboard() {
  const { token } = antdTheme.useToken()
  const qc = useQueryClient()
  const { data, isLoading } = useOverview()
  const [range, setRange] = useState(120)
  const [samples, setSamples] = useState<{ t: number; in: number; out: number }[]>([])

  const node = data?.node || {}
  const tunnels = data?.top_tunnels || []

  // Keep a rolling window on the client; the API only reports current rates.
  useEffect(() => {
    if (!data) return
    const point = { t: Math.floor(Date.now() / 1000), in: data.rate_in || 0, out: data.rate_out || 0 }
    setSamples((prev) => [...prev, point].slice(-300))
  }, [data])

  const visible = useMemo(() => samples.slice(-range), [samples, range])
  const idle = visible.length < 2 || visible.every((s) => s.in === 0 && s.out === 0)
  const fleetRate = (data?.rate_in || 0) + (data?.rate_out || 0)
  const cpu = Math.round(node.cpu_percent || 0)
  const memPct = percent(node.mem_used, node.mem_total)
  const heapPct = percent(node.heap_alloc, node.heap_sys)

  const kpis = [
    {
      label: '在线客户端',
      raw: data?.clients ?? 0,
      format: formatNumber,
      hint: `隧道 ${data?.tunnels ?? 0} 条`,
      icon: <WifiOutlined />
    },
    {
      label: '活跃连接',
      raw: data?.active_connections ?? 0,
      format: formatNumber,
      hint: `累计 ${formatNumber(data?.connections_total)} 次`,
      icon: <ApiOutlined />
    },
    {
      label: '全部隧道实时合计',
      raw: fleetRate,
      format: (n: number) => formatBytes(n, 1),
      suffix: '/s',
      hint: `入 ${formatBytes(data?.rate_in, 1)}/s · 出 ${formatBytes(data?.rate_out, 1)}/s`,
      icon: <ThunderboltOutlined />
    },
    {
      label: '累计入站',
      raw: data?.total_in ?? 0,
      format: formatBytes,
      hint: '所有隧道相加',
      icon: <ArrowDownOutlined />
    },
    {
      label: '累计出站',
      raw: data?.total_out ?? 0,
      format: formatBytes,
      hint: '所有隧道相加',
      icon: <ArrowUpOutlined />
    },
    {
      label: 'HTTP 请求',
      raw: data?.http_requests ?? 0,
      format: formatNumber,
      hint: '公网入口计数',
      icon: <DashboardOutlined />
    }
  ]

  const columns = [
    {
      title: '隧道',
      key: 'name',
      render: (_: any, r: any) => (
        <div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <strong>{r.name}</strong>
            <SoftTag tone="blue">{r.owner}</SoftTag>
          </div>
          <Typography.Text type="secondary" className="mono" style={{ fontSize: 11 }}>
            {r.public_addr}
          </Typography.Text>
        </div>
      )
    },
    {
      title: '入站',
      dataIndex: 'bytes_in',
      key: 'in',
      width: 130,
      render: (v: number) => <span className="mono">{formatBytes(v)}</span>
    },
    {
      title: '出站',
      dataIndex: 'bytes_out',
      key: 'out',
      width: 130,
      render: (v: number) => <span className="mono">{formatBytes(v)}</span>
    },
    {
      title: '合计',
      key: 'sum',
      width: 120,
      render: (_: any, r: any) => (
        <div>
          <span className="mono">{formatBytes((r.bytes_in || 0) + (r.bytes_out || 0))}</span>
          <div style={{ fontSize: 11, opacity: 0.6 }}>{`${r.active_connections} 连接`}</div>
        </div>
      )
    }
  ]

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h2>总览</h2>
          <p>
            {/* One template string, not JSX interpolation: the translator works
                per text node, and an interpolated sentence arrives in pieces
                ("节点运行 " + "19s" + " · " + "0" …) that no phrase rule can
                match. */}
            {`节点运行 ${formatUptime(data?.uptime_sec)} · ${formatNumber(data?.connections_total)} 次累计连接`}
          </p>
        </div>
        <span style={{ flex: 1 }} />
        <Button
          size="small"
          type="text"
          icon={<ReloadOutlined className="spin-on-click" />}
          loading={isLoading}
          onClick={() => qc.invalidateQueries({ queryKey: ['overview'] })}
        >
          刷新
        </Button>
      </div>

      <Row gutter={[12, 12]}>
        {kpis.map((k) => (
          // Six equal cards: 6+6+6+6 at xl. `flex="1"` is deliberately absent —
          // combined with a gutter it overflows the row by the negative margin
          // and clips the last card at the right edge.
          <Col key={k.label} xs={12} sm={12} md={8} xl={4}>
            <Card variant="borderless" styles={{ body: { padding: '14px 16px' } }} loading={isLoading}>
              <div className="kpi-label">
                {k.icon}
                {k.label}
              </div>
              <div className="kpi-value mono">
                <AnimatedNumber value={k.raw} format={k.format} />
                {k.suffix && <span className="kpi-suffix">{k.suffix}</span>}
              </div>
              <div className="kpi-hint">{k.hint}</div>
            </Card>
          </Col>
        ))}
      </Row>

      <Card
        style={{ marginTop: 12 }}
        variant="borderless"
        title={
          <span>
            全局流量 <span className="card-sub">{`每秒采样 · 最近 ${range} 秒`}</span>
          </span>
        }
        extra={
          <Segmented
            size="small"
            value={range}
            onChange={(v) => setRange(Number(v))}
            options={[
              { label: '1 分钟', value: 60 },
              { label: '2 分钟', value: 120 },
              { label: '5 分钟', value: 300 }
            ]}
          />
        }
      >
        {/* Zero traffic is an empty state, not a flat line at the bottom of an
            axis: the illustration says "waiting", the line says "broken". */}
        {idle ? <EmptyTraffic /> : <TrafficChart samples={visible} rangeSec={range} />}
      </Card>

      <Row gutter={[12, 12]} style={{ marginTop: 12 }}>
        <Col xs={24} lg={14}>
          <Card
            variant="borderless"
            title={
              <span>
                Top 隧道 <span className="card-sub">按累计流量排序</span>
              </span>
            }
          >
            {tunnels.length ? (
              <Table
                size="small"
                rowKey="id"
                dataSource={tunnels}
                columns={columns as any}
                pagination={false}
              />
            ) : (
              <EmptyTraffic
                compact
                title="暂无隧道"
                hint="客户端连上并注册隧道后，这里会按累计流量排序"
              />
            )}
          </Card>
        </Col>

        <Col xs={24} lg={10}>
          <Card
            variant="borderless"
            title={
              <span>
                节点资源 <span className="card-sub">{node.name || '本机'}</span>
              </span>
            }
          >
            <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
              <div style={{ display: 'grid', gridTemplateColumns: '56px 1fr auto', gap: 10, alignItems: 'center' }}>
                <span style={{ fontSize: 12, opacity: 0.7 }}>
                  <CloudServerOutlined /> CPU
                </span>
                <Progress percent={cpu} size="small" showInfo={false} strokeColor={usageColor(cpu)} />
                <span className="mono" style={{ fontSize: 11, opacity: 0.65 }}>
                  {cpu}%
                </span>
              </div>
              <div style={{ display: 'grid', gridTemplateColumns: '56px 1fr auto', gap: 10, alignItems: 'center' }}>
                <span style={{ fontSize: 12, opacity: 0.7 }}>内存</span>
                <Progress percent={memPct} size="small" showInfo={false} strokeColor={usageColor(memPct)} />
                <span className="mono" style={{ fontSize: 11, opacity: 0.65 }}>
                  {formatBytes(node.mem_used)} / {formatBytes(node.mem_total)}
                </span>
              </div>
              <div style={{ display: 'grid', gridTemplateColumns: '56px 1fr auto', gap: 10, alignItems: 'center' }}>
                <span style={{ fontSize: 12, opacity: 0.7 }}>堆内存</span>
                {/* alloc/sys is not a saturation signal: right after a GC the
                    arena is nearly full by design. Colouring it red would fire
                    a false alarm on every healthy process, so it keeps one
                    colour and only CPU/memory use the threshold scale. */}
                <Progress percent={heapPct} size="small" showInfo={false} strokeColor="#9254de" />
                <span className="mono" style={{ fontSize: 11, opacity: 0.65 }}>
                  {formatBytes(node.heap_alloc)}
                </span>
              </div>
            </div>

            <Row gutter={[8, 4]} style={{ marginTop: 16, fontSize: 12 }}>
              <Col span={12} style={{ opacity: 0.6 }}>
                版本
              </Col>
              <Col span={12}>{node.version || '--'}</Col>
              <Col span={12} style={{ opacity: 0.6 }}>
                运行时长
              </Col>
              <Col span={12}>{formatUptime(node.uptime_sec)}</Col>
              <Col span={12} style={{ opacity: 0.6 }}>
                协程
              </Col>
              <Col span={12}>{formatNumber(node.goroutines)}</Col>
              <Col span={12} style={{ opacity: 0.6 }}>
                GC 次数
              </Col>
              <Col span={12}>{formatNumber(node.num_gc)}</Col>
            </Row>
          </Card>
        </Col>
      </Row>
    </div>
  )
}

<script setup>
/**
 * Traffic chart (in/out area lines) — the signature widget of both consoles.
 *
 * Data contract, shared by client and server:
 *   [{ timestamp: <unix ms>, bytes_in: <bytes/s>, bytes_out: <bytes/s> }, ...]
 *
 * Implementation notes:
 *  - echarts is imported through a hand-written core build (common/echarts.js)
 *    so only the used chart/component modules are bundled (~90KB gz instead of
 *    the 300KB+ full build).
 *  - `notMerge: false` + update animation gives smooth 2 s pushes. Recreating
 *    the option each frame would reset the animation and look like flicker.
 *  - `progressive` + `large` are intentionally NOT set: 300 points per series
 *    is tiny and animation quality matters more here.
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { echarts } from '@common/echarts'
import { formatBytes, formatClock } from '@common/format'
import Icon from './Icon.vue'

const props = defineProps({
  samples: { type: Array, default: () => [] },
  height: { type: Number, default: 260 },
  compact: { type: Boolean, default: false },
  /** Averaged over N samples to produce the optional subtitle rate. */
  showRate: { type: Boolean, default: false }
})

/**
 * The stats ring buffer keeps 300 one-second samples even while a tunnel is
 * idle, so `samples.length` alone cannot detect "no traffic". An all-zero
 * window would otherwise draw a flat line that reads as a broken/empty chart.
 */
const isEmpty = computed(() => {
  const data = props.samples || []
  if (!data.length) return true
  return data.every((s) => !(s.bytes_in > 0) && !(s.bytes_out > 0))
})
const emptyHint = computed(() =>
  props.compact
    ? '该隧道最近 5 分钟没有传输记录'
    : '向已发布的隧道发起请求，约 2 秒后这里会出现曲线'
)

const root = ref(null)
let chart = null
let observer = null

// Chart palette mirrors the antd tokens in theme.css: primary blue, purple
// second series, neutral grid, opaque tooltip. Kept in JS because ECharts cannot
// read CSS custom properties.
const CSS = {
  in: '#1677ff',
  out: '#9254de',
  gridLine: 'rgba(255,255,255,.06)',
  axis: 'rgba(255,255,255,.12)',
  label: '#8b929e',
  tooltipBg: '#161a21',
  tooltipBd: '#232833',
  text: '#e6e8ec'
}

function series() {
  const data = props.samples || []
  return {
    inbound: data.map((s) => [s.timestamp, s.bytes_in || 0]),
    outbound: data.map((s) => [s.timestamp, s.bytes_out || 0])
  }
}

function option() {
  const { inbound, outbound } = series()
  return {
    animationDuration: 420,
    animationDurationUpdate: 260,
    animationEasing: 'cubicOut',
    animationEasingUpdate: 'cubicOut',
    grid: {
      left: props.compact ? 2 : 6,
      right: 8,
      top: props.compact ? 6 : 26,
      bottom: 2,
      containLabel: true
    },
    tooltip: {
      trigger: 'axis',
      backgroundColor: CSS.tooltipBg,
      borderColor: CSS.tooltipBd,
      borderWidth: 1,
      padding: [9, 12],
      textStyle: { color: CSS.text, fontSize: 12 },
      axisPointer: {
        type: 'line',
        lineStyle: { color: 'rgba(255,255,255,.24)', type: 'dashed', width: 1 }
      },
      formatter(params) {
        const first = params?.[0]
        if (!first) return ''
        const rows = params
          .map(
            (item) => `<div style="display:flex;align-items:center;gap:8px;margin-top:5px">
              <span style="width:7px;height:7px;border-radius:50%;background:${item.color}"></span>
              <span style="color:#9aa4b2">${item.seriesName}</span>
              <span style="margin-left:auto;font-family:ui-monospace,Consolas,monospace;color:#e6e8ec">${formatBytes(
                item.value[1],
                1
              )}/s</span>
            </div>`
          )
          .join('')
        return `<div style="font-size:11px;color:#9aa4b2;letter-spacing:.04em">${formatClock(
          first.value[0]
        )}</div>${rows}`
      }
    },
    legend: props.compact
      ? { show: false }
      : {
          right: 4,
          top: 0,
          icon: 'circle',
          itemWidth: 7,
          itemHeight: 7,
          itemGap: 16,
          textStyle: { color: CSS.label, fontSize: 11 },
          data: ['入站', '出站']
        },
    xAxis: {
      type: 'time',
      boundaryGap: false,
      axisLine: { lineStyle: { color: CSS.axis } },
      axisTick: { show: false },
      axisLabel: {
        color: CSS.label,
        fontSize: 10,
        hideOverlap: true,
        formatter: (value) =>
          new Date(value).toLocaleTimeString('zh-CN', { hour12: false, hour: '2-digit', minute: '2-digit' })
      },
      splitLine: { show: false }
    },
    yAxis: {
      type: 'value',
      splitNumber: props.compact ? 2 : 4,
      axisLine: { show: false },
      axisTick: { show: false },
      axisLabel: {
        color: CSS.label,
        fontSize: 10,
        // formatBytes() normalises numbers, numeric strings and the
        // `[value, index]` tuple some ECharts builds pass to formatters.
        formatter: (value) => formatBytes(value, 0)
      },
      splitLine: { lineStyle: { color: CSS.gridLine } }
    },
    series: [
      {
        name: '入站',
        type: 'line',
        data: inbound,
        smooth: 0.3,
        showSymbol: false,
        symbol: 'circle',
        symbolSize: 5,
        lineStyle: { width: 1.9, color: CSS.in },
        itemStyle: { color: CSS.in },
        areaStyle: {
          color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
            { offset: 0, color: 'rgba(22,119,255,.28)' },
            { offset: 1, color: 'rgba(22,119,255,0)' }
          ])
        },
        emphasis: { focus: 'series' }
      },
      {
        name: '出站',
        type: 'line',
        data: outbound,
        smooth: 0.3,
        showSymbol: false,
        symbol: 'circle',
        symbolSize: 5,
        lineStyle: { width: 1.9, color: CSS.out },
        itemStyle: { color: CSS.out },
        areaStyle: {
          color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
            { offset: 0, color: 'rgba(146,84,222,.24)' },
            { offset: 1, color: 'rgba(146,84,222,0)' }
          ])
        },
        emphasis: { focus: 'series' }
      }
    ]
  }
}

function render() {
  if (!chart) return
  chart.setOption(option(), { notMerge: false, lazyUpdate: true })
}

onMounted(() => {
  if (!root.value) return
  chart = echarts.init(root.value, null, { renderer: 'canvas' })
  render()
  // ResizeObserver instead of window.onresize: the rail can collapse and the
  // chart must follow without a resize event.
  observer = new ResizeObserver(() => chart?.resize())
  observer.observe(root.value)
})

watch(() => props.samples, render, { deep: false })

onBeforeUnmount(() => {
  observer?.disconnect()
  chart?.dispose()
  chart = null
})
</script>

<template>
  <div class="wg-chart" :style="{ height: `${height}px`, width: '100%' }">
    <div ref="root" class="wg-chart-canvas" />
    <div v-if="isEmpty" class="wg-chart-empty">
      <div class="wg-chart-empty-icon"><Icon name="activity" :size="20" /></div>
      <strong>暂无流量</strong>
      <span>{{ emptyHint }}</span>
    </div>
  </div>
</template>

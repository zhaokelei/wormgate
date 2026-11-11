/**
 * Breathing status dot.
 *
 * "Live" here means the 2 s overview poll is still landing: the dot goes flat
 * grey when the page is hidden or the poll has stalled, so the pulse never
 * claims freshness that is not there.
 */
export default function LiveDot({ text = '实时', active = true }: { text?: string; active?: boolean }) {
  return (
    <span className={`live-dot ${active ? '' : 'is-idle'}`} title={active ? '正在轮询服务端数据' : '已暂停'}>
      <i />
      {text}
    </span>
  )
}

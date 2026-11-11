import { useId } from 'react'

/**
 * Quiet empty state for the traffic card.
 *
 * The previous version drew a framed wireframe plot with axis rules, three
 * ripple rings and drifting sparks. At card size that read as a schematic —
 * busy and hard-edged next to the rest of the dashboard — so this one keeps a
 * single soft area, one curve and one slow pulse: the same "instrument is on
 * and waiting" idea, at a much lower volume.
 *
 * The same drawing serves the tunnels ranking card at a smaller size, so the
 * two empty areas do not look like they came from two products.
 */
export default function EmptyTraffic({
  title = '等待隧道请求接入中…',
  hint = '隧道被访问后，实时速率会在这里画出曲线',
  compact = false
}: {
  title?: string
  hint?: string
  compact?: boolean
}) {
  // Two instances are on the dashboard at once; a shared gradient id would let
  // the compact one inherit the large one's stops.
  const gid = useId().replace(/[:]/g, '')

  return (
    <div className={`empty-traffic${compact ? ' is-compact' : ''}`}>
      <svg viewBox="0 0 220 92" role="img" aria-label={title}>
        <defs>
          <linearGradient id={`et-${gid}`} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" style={{ stopColor: 'var(--accent)', stopOpacity: 0.16 }} />
            <stop offset="100%" style={{ stopColor: 'var(--accent)', stopOpacity: 0 }} />
          </linearGradient>
        </defs>
        <path
          d="M6 78 C 38 78, 54 58, 84 56 S 128 68, 156 44 S 194 32, 214 30 L 214 86 L 6 86 Z"
          fill={`url(#et-${gid})`}
        />
        <path
          d="M6 78 C 38 78, 54 58, 84 56 S 128 68, 156 44 S 194 32, 214 30"
          className="curve"
        />
        <line x1="6" y1="86" x2="214" y2="86" className="baseline" />
        <circle cx="156" cy="44" r="9" className="ripple" />
        <circle cx="156" cy="44" r="3" className="dot" />
      </svg>
      <div className="empty-traffic-title">{title}</div>
      <div className="empty-traffic-hint">{hint}</div>
    </div>
  )
}

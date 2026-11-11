import type { ReactNode } from 'react'

/**
 * Replays the fade-in-up stagger on every route change.
 *
 * Keying the wrapper on the pathname is what restarts the animation: React
 * throws the old subtree away and mounts a new one, so the CSS animation in
 * styles.css runs again. The stagger itself (60 ms steps) is pure CSS on the
 * children of `.page`.
 */
export default function PageTransition({
  routeKey,
  children
}: {
  routeKey: string
  children: ReactNode
}) {
  return (
    <div className="page-enter" key={routeKey}>
      {children}
    </div>
  )
}

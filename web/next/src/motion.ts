/**
 * Motion primitives shared by every animated element.
 *
 * `EASE` mirrors the `--ease` custom property in styles.css: one curve for CSS
 * and JS keeps the console feeling like a single product instead of a set of
 * pages that each picked their own timing.
 */
import { useEffect, useRef, useState } from 'react'

export const EASE = 'cubic-bezier(0.4, 0, 0.2, 1)'

const REDUCED_QUERY = '(prefers-reduced-motion: reduce)'

export function prefersReducedMotion(): boolean {
  if (typeof window === 'undefined' || !window.matchMedia) return false
  return window.matchMedia(REDUCED_QUERY).matches
}

/** Subscribes to the OS setting so a mid-session change is honoured too. */
export function useReducedMotion(): boolean {
  const [reduced, setReduced] = useState(prefersReducedMotion)

  useEffect(() => {
    if (!window.matchMedia) return
    const mq = window.matchMedia(REDUCED_QUERY)
    const onChange = () => setReduced(mq.matches)
    mq.addEventListener('change', onChange)
    return () => mq.removeEventListener('change', onChange)
  }, [])

  return reduced
}

function easeOutCubic(t: number): number {
  return 1 - Math.pow(1 - t, 3)
}

/**
 * Counts a number up from its previous value.
 *
 * Every metric on the dashboard ticks on a 2 s poll, so re-animating from zero
 * each time would look like a flicker; the animation starts at the value that
 * is already on screen instead.
 */
export function useCountUp(target: number, duration = 700): number {
  const reduced = useReducedMotion()
  const [value, setValue] = useState(0)
  const fromRef = useRef(0)
  const frameRef = useRef(0)

  useEffect(() => {
    const to = Number.isFinite(target) ? target : 0
    if (reduced) {
      fromRef.current = to
      setValue(to)
      return
    }

    const from = fromRef.current
    if (from === to) return

    const start = performance.now()
    const tick = (now: number) => {
      const t = Math.min(1, (now - start) / duration)
      const next = from + (to - from) * easeOutCubic(t)
      setValue(next)
      if (t < 1) {
        frameRef.current = requestAnimationFrame(tick)
      } else {
        fromRef.current = to
        setValue(to)
      }
    }

    cancelAnimationFrame(frameRef.current)
    frameRef.current = requestAnimationFrame(tick)

    return () => cancelAnimationFrame(frameRef.current)
  }, [target, duration, reduced])

  return value
}

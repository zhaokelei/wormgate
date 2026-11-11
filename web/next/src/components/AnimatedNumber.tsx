import { useCountUp } from '../motion'

/**
 * Metric value that counts up to the polled number.
 *
 * The formatter is passed in rather than baked in because the same component
 * renders counts ("2 客户端") and byte rates ("1.2 MB/s").
 */
export default function AnimatedNumber({
  value,
  format,
  className
}: {
  value: number
  format: (n: number) => string
  className?: string
}) {
  const animated = useCountUp(value)
  return <span className={className}>{format(animated)}</span>
}

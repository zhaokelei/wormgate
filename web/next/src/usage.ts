/**
 * Shared usage thresholds.
 *
 * One definition so "orange" means the same thing on the dashboard and on the
 * nodes page: 70% is worth a look, 88% is worth acting on.
 */
export function usageColor(pct: number): string {
  if (pct >= 88) return '#ff4d4f'
  if (pct >= 70) return '#fa8c16'
  return '#1677ff'
}

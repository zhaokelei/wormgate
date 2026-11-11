import type { ReactNode } from 'react'

export type Tone = 'blue' | 'green' | 'orange' | 'red' | 'purple' | 'gray'

/** antd's colour names, so call sites can keep the vocabulary they already use. */
const ALIAS: Record<string, Tone> = {
  blue: 'blue',
  geekblue: 'blue',
  success: 'green',
  green: 'green',
  warning: 'orange',
  orange: 'orange',
  gold: 'orange',
  error: 'red',
  red: 'red',
  danger: 'red',
  purple: 'purple',
  magenta: 'purple',
  default: 'gray',
  gray: 'gray'
}

/**
 * Tag with a 10%-ish tint of its own colour and matching text instead of
 * antd's solid fill: 16 of these sit inside tables, and solid blocks turned the
 * rows into a colour chart. Tints and text colours are defined per theme in
 * styles.css so light mode keeps enough contrast.
 */
export default function SoftTag({
  tone = 'gray',
  icon,
  children,
  title
}: {
  tone?: Tone | string
  icon?: ReactNode
  children: ReactNode
  title?: string
}) {
  const resolved = ALIAS[tone] ?? 'gray'
  return (
    <span className={`soft-tag ${resolved}`} title={title}>
      {icon}
      {children}
    </span>
  )
}

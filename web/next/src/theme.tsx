import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { theme as antdTheme } from 'antd'

/**
 * Dark is the default (same rule as the Vue console): the panel usually sits
 * next to a terminal. The choice persists and is mirrored onto <html
 * data-theme> so plain-CSS pieces stay in sync with antd's algorithm.
 */
type Mode = 'dark' | 'light'
const STORAGE_KEY = 'tunnelx.next.theme'

interface ThemeCtxValue {
  mode: Mode
  toggle: () => void
  themeConfig: any
}

const ThemeCtx = createContext<ThemeCtxValue>({ mode: 'dark', toggle: () => {}, themeConfig: {} })

function initialMode(): Mode {
  const saved = localStorage.getItem(STORAGE_KEY)
  if (saved === 'dark' || saved === 'light') return saved
  return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
}

function buildTheme(mode: Mode) {
  const dark = mode === 'dark'
  return {
    algorithm: dark ? antdTheme.darkAlgorithm : antdTheme.defaultAlgorithm,
    token: {
      colorPrimary: '#1677ff',
      borderRadius: 8,
      fontFamily:
        "-apple-system, BlinkMacSystemFont, 'Segoe UI', 'PingFang SC', 'Hiragino Sans GB', 'Microsoft YaHei', 'Noto Sans SC', Roboto, Helvetica, Arial, sans-serif",
      // One easing curve across the whole console; every antd transition reads
      // this token, so buttons, dropdowns and modals match the custom CSS.
      motionEaseInOut: 'cubic-bezier(0.4, 0, 0.2, 1)',
      motionDurationMid: '0.24s',
      ...(dark
        ? {
            // Near-black canvas with translucent cards: the panel should feel
            // like layered glass over the page, not a set of flat grey boxes.
            colorBgLayout: '#0a0a0a',
            colorBgContainer: 'rgba(255, 255, 255, 0.03)',
            colorBgElevated: '#15171c',
            colorBorder: 'rgba(255, 255, 255, 0.08)',
            colorBorderSecondary: 'rgba(255, 255, 255, 0.06)'
          }
        : { colorBgLayout: '#f5f5f5' })
    },
    components: {
      Layout: {
        bodyBg: 'transparent',
        headerBg: 'transparent',
        siderBg: dark ? 'rgba(255, 255, 255, 0.02)' : '#ffffff'
      },
      Menu: {
        itemBg: 'transparent',
        subMenuItemBg: 'transparent',
        darkItemBg: 'transparent',
        darkSubMenuItemBg: 'transparent',
        // The sliding indicator in Shell.tsx is the only highlight; antd's own
        // selected background would sit underneath it and double the block.
        itemSelectedBg: 'transparent',
        darkItemSelectedBg: 'transparent',
        itemSelectedColor: '#1677ff',
        darkItemSelectedColor: '#ffffff',
        itemHeight: 38,
        itemMarginInline: 8,
        itemBorderRadius: 8
      },
      Table: {
        headerBg: dark ? 'rgba(255, 255, 255, 0.02)' : '#fafafa',
        headerSplitColor: 'transparent',
        rowHoverBg: 'transparent'
      },
      Progress: { defaultColor: '#1677ff' }
    }
  }
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [mode, setMode] = useState<Mode>(initialMode)
  const animTimer = useRef<number | undefined>(undefined)

  useEffect(() => {
    document.documentElement.dataset.theme = mode
    localStorage.setItem(STORAGE_KEY, mode)
  }, [mode])

  /**
   * Cross-fades the whole palette for 0.5 s. The class goes on <html> because
   * the transition has to reach antd's portal layers (.ant-modal, dropdowns)
   * too, and those are not inside the React root.
   */
  const toggle = useCallback(() => {
    const root = document.documentElement
    root.classList.add('theme-anim')
    window.clearTimeout(animTimer.current)
    animTimer.current = window.setTimeout(() => root.classList.remove('theme-anim'), 560)
    setMode((m) => (m === 'dark' ? 'light' : 'dark'))
  }, [])

  useEffect(() => () => window.clearTimeout(animTimer.current), [])

  const value = useMemo<ThemeCtxValue>(
    () => ({ mode, toggle, themeConfig: buildTheme(mode) }),
    [mode, toggle]
  )

  return <ThemeCtx.Provider value={value}>{children}</ThemeCtx.Provider>
}

export function useThemeMode() {
  return useContext(ThemeCtx)
}
